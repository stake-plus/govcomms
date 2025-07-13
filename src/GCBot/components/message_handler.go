package components

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/redis/go-redis/v9"
	"github.com/stake-plus/govcomms/src/GCApi/data"
	"github.com/stake-plus/govcomms/src/GCApi/types"
	"github.com/stake-plus/govcomms/src/GCBot/api"
	"github.com/stake-plus/govcomms/src/GCBot/network"
	"github.com/stake-plus/govcomms/src/GCBot/referendum"
	"gorm.io/gorm"
)

type Config struct {
	DB             *gorm.DB
	Redis          *redis.Client
	NetworkManager *network.Manager
	RefHandler     *referendum.Handler
	APIClient      *api.Client
	FeedbackRoleID string
	GuildID        string
}

type Handler struct {
	config      Config
	rateLimiter *RateLimiter
}

func NewHandler(config Config) *Handler {
	return &Handler{
		config:      config,
		rateLimiter: NewRateLimiter(5 * time.Minute),
	}
}

func (h *Handler) HandleMessage(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == s.State.User.ID {
		return
	}

	if !strings.HasPrefix(m.Content, "!feedback") {
		return
	}

	// Rate limit check
	if !h.rateLimiter.CanUse(m.Author.ID) {
		timeLeft := h.rateLimiter.TimeUntilNext(m.Author.ID)
		minutes := int(timeLeft.Minutes())
		seconds := int(timeLeft.Seconds()) % 60
		msg := fmt.Sprintf("Please wait %d minutes and %d seconds before using this command again.",
			minutes, seconds)
		s.ChannelMessageSend(m.ChannelID, msg)
		return
	}

	// Check role
	if !h.hasRole(s, m.GuildID, m.Author.ID, h.config.FeedbackRoleID) {
		s.ChannelMessageSend(m.ChannelID, "You don't have permission to use this command.")
		return
	}

	// Parse command
	parts := strings.SplitN(m.Content, " ", 3)
	if len(parts) < 3 {
		s.ChannelMessageSend(m.ChannelID, "Usage: !feedback network/ref_number Your feedback message")
		return
	}

	proposalRef := parts[1]
	feedbackMsg := parts[2]

	// Validate
	refParts := strings.Split(proposalRef, "/")
	if len(refParts) != 2 {
		s.ChannelMessageSend(m.ChannelID, "Invalid format. Use: network/ref_number (e.g., polkadot/123)")
		return
	}

	networkName := strings.ToLower(refParts[0])
	refNum, err := strconv.ParseUint(refParts[1], 10, 64)
	if err != nil || refNum == 0 || refNum > 1000000 {
		s.ChannelMessageSend(m.ChannelID, "Invalid referendum number")
		return
	}

	// Validate message length
	if len(feedbackMsg) < 10 || len(feedbackMsg) > 5000 {
		s.ChannelMessageSend(m.ChannelID, "Feedback message must be between 10 and 5000 characters")
		return
	}

	// Get DAO member
	var daoMember types.DaoMember
	if err := h.config.DB.Where("discord = ?", m.Author.Username).First(&daoMember).Error; err != nil {
		s.ChannelMessageSend(m.ChannelID,
			"Your Discord account is not linked to a Polkadot address. Please contact an admin.")
		return
	}

	// Process feedback
	if err := h.processFeedback(s, m, networkName, refNum, feedbackMsg, &daoMember); err != nil {
		log.Printf("Error processing feedback: %v", err)
		s.ChannelMessageSend(m.ChannelID, "Failed to process feedback. Please try again.")
	}
}

func (h *Handler) processFeedback(s *discordgo.Session, m *discordgo.MessageCreate,
	networkName string, refNum uint64, feedbackMsg string, daoMember *types.DaoMember) error {

	// Find network
	net := h.config.NetworkManager.GetByName(networkName)
	if net == nil {
		return fmt.Errorf("unknown network: %s", networkName)
	}

	// Get or create referendum
	ref, err := h.config.RefHandler.GetOrCreateRef(net.ID, refNum, daoMember.Address, daoMember.IsAdmin)
	if err != nil {
		return err
	}

	// Store message
	var msgID uint64
	err = h.config.DB.Transaction(func(tx *gorm.DB) error {
		// Create/update proponent
		proponent := types.RefProponent{
			RefID:   ref.ID,
			Address: daoMember.Address,
			Role:    "dao_member",
			Active:  1,
		}
		if err := tx.FirstOrCreate(&proponent,
			types.RefProponent{RefID: ref.ID, Address: daoMember.Address}).Error; err != nil {
			return err
		}

		// Create message
		msg := types.RefMessage{
			RefID:     ref.ID,
			Author:    daoMember.Address,
			Body:      feedbackMsg,
			CreatedAt: time.Now(),
			Internal:  true,
		}
		if err := tx.Create(&msg).Error; err != nil {
			return err
		}

		msgID = msg.ID
		return nil
	})

	if err != nil {
		return err
	}

	// Count messages
	var msgCount int64
	h.config.DB.Model(&types.RefMessage{}).Where("ref_id = ?", ref.ID).Count(&msgCount)

	// Build response
	embed := h.buildResponseEmbed(networkName, refNum, msgCount, m.Author.Username)
	s.ChannelMessageSendEmbed(m.ChannelID, embed)

	// Post to Polkassembly if first message
	if msgCount == 1 && net.PolkassemblyPrefix != "" {
		go h.postToPolkassembly(net, refNum, feedbackMsg)
	}

	// Publish to Redis
	if h.config.Redis != nil {
		h.publishToRedis(networkName, refNum, daoMember.Address, feedbackMsg, msgID)
	}

	log.Printf("Feedback submitted by %s (%s) for %s/%d: %d chars",
		m.Author.Username, daoMember.Address, networkName, refNum, len(feedbackMsg))

	return nil
}

func (h *Handler) hasRole(s *discordgo.Session, guildID, userID, roleID string) bool {
	member, err := s.GuildMember(guildID, userID)
	if err != nil {
		return false
	}

	for _, role := range member.Roles {
		if role == roleID {
			return true
		}
	}
	return false
}

func (h *Handler) buildResponseEmbed(network string, refNum uint64, msgCount int64, username string) *discordgo.MessageEmbed {
	gcURL := data.GetSetting("gc_url")
	if gcURL == "" {
		gcURL = "http://localhost:3000"
	}
	link := fmt.Sprintf("%s/%s/%d", gcURL, network, refNum)

	return &discordgo.MessageEmbed{
		Title:       "Feedback Submitted",
		Description: fmt.Sprintf("Your feedback for %s/%d has been submitted.", network, refNum),
		Color:       0x00ff00,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:  "Message Count",
				Value: fmt.Sprintf("This is message #%d for this proposal", msgCount),
			},
			{
				Name:  "Continue Discussion",
				Value: fmt.Sprintf("[Click here](%s) to continue the conversation", link),
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("Submitted by %s", username),
		},
		Timestamp: time.Now().Format(time.RFC3339),
	}
}

func (h *Handler) postToPolkassembly(net *types.Network, refNum uint64, message string) {
	// TODO: Implement Polkassembly integration
	log.Printf("Would post to Polkassembly: %s/%d", net.Name, refNum)
}

func (h *Handler) publishToRedis(network string, refNum uint64, author, body string, msgID uint64) {
	_ = data.PublishMessage(context.Background(), h.config.Redis, map[string]interface{}{
		"proposal": fmt.Sprintf("%s/%d", network, refNum),
		"author":   author,
		"body":     body,
		"time":     time.Now().Unix(),
		"id":       msgID,
		"network":  network,
		"ref_id":   refNum,
	})
}
