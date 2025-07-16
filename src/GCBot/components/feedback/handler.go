package feedback

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/redis/go-redis/v9"
	"github.com/stake-plus/govcomms/src/GCApi/data"
	"github.com/stake-plus/govcomms/src/GCApi/types"
	"github.com/stake-plus/govcomms/src/GCBot/api"
	"github.com/stake-plus/govcomms/src/GCBot/components/network"
	"github.com/stake-plus/govcomms/src/GCBot/components/referendum"
	"gorm.io/gorm"
)

type Config struct {
	DB             *gorm.DB
	Redis          *redis.Client
	NetworkManager *network.Manager
	RefManager     *referendum.Manager
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

	log.Printf("Feedback command received from %s in channel %s", m.Author.Username, m.ChannelID)

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
	if !h.hasRole(s, h.config.GuildID, m.Author.ID, h.config.FeedbackRoleID) {
		s.ChannelMessageSend(m.ChannelID, "You don't have permission to use this command.")
		return
	}

	// Parse command
	parts := strings.SplitN(m.Content, " ", 2)
	if len(parts) < 2 {
		s.ChannelMessageSend(m.ChannelID, "Usage: !feedback Your feedback message")
		return
	}

	feedbackMsg := parts[1]

	// Validate message length
	if len(feedbackMsg) < 10 || len(feedbackMsg) > 5000 {
		s.ChannelMessageSend(m.ChannelID, "Feedback message must be between 10 and 5000 characters")
		return
	}

	// Check if we're in a thread
	threadInfo := h.config.RefManager.GetThreadInfo(m.ChannelID)
	if threadInfo == nil {
		log.Printf("Channel %s is not a recognized referendum thread", m.ChannelID)
		s.ChannelMessageSend(m.ChannelID, "This command must be used in a referendum thread.")
		return
	}

	// Get DAO member by Discord username
	var daoMember types.DaoMember
	if err := h.config.DB.Where("discord = ?", m.Author.Username).First(&daoMember).Error; err != nil {
		log.Printf("Discord user %s not found in dao_members", m.Author.Username)
		s.ChannelMessageSend(m.ChannelID, "Your Discord account is not linked to a Polkadot address. Please contact an admin.")
		return
	}

	// Get network info
	network := h.config.NetworkManager.GetByID(threadInfo.NetworkID)
	if network == nil {
		log.Printf("Network not found for ID %d", threadInfo.NetworkID)
		s.ChannelMessageSend(m.ChannelID, "Failed to process feedback. Please try again.")
		return
	}

	// Process the feedback using the thread info
	if err := h.processFeedbackFromThread(s, m, threadInfo, network, feedbackMsg, &daoMember); err != nil {
		log.Printf("Error processing feedback: %v", err)
		s.ChannelMessageSend(m.ChannelID, "Failed to process feedback. Please try again.")
		return
	}
}

func (h *Handler) processFeedbackFromThread(s *discordgo.Session, m *discordgo.MessageCreate,
	threadInfo *referendum.ThreadInfo, network *types.Network, feedbackMsg string, daoMember *types.DaoMember) error {

	log.Printf("Processing feedback for %s ref #%d from %s", network.Name, threadInfo.RefID, daoMember.Address)

	var msgID uint64
	err := h.config.DB.Transaction(func(tx *gorm.DB) error {
		// Get the referendum
		var ref types.Ref
		if err := tx.First(&ref, threadInfo.RefDBID).Error; err != nil {
			return fmt.Errorf("referendum not found: %w", err)
		}

		// Create/update proponent
		proponent := types.RefProponent{
			RefID:   ref.ID,
			Address: daoMember.Address,
			Role:    "dao_member",
			Active:  1,
		}
		if err := tx.FirstOrCreate(&proponent, types.RefProponent{RefID: ref.ID, Address: daoMember.Address}).Error; err != nil {
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

	// Check if this is the first message
	var msgCount int64
	h.config.DB.Model(&types.RefMessage{}).Where("ref_id = ?", threadInfo.RefDBID).Count(&msgCount)

	// Build response
	gcURL := data.GetSetting("gc_url")
	if gcURL == "" {
		gcURL = "http://localhost:3000"
	}

	link := fmt.Sprintf("%s/%s/%d", gcURL, strings.ToLower(network.Name), threadInfo.RefID)

	// Create embed response
	embed := &discordgo.MessageEmbed{
		Title:       "Feedback Submitted",
		Description: fmt.Sprintf("Your feedback for %s/%d has been submitted.", network.Name, threadInfo.RefID),
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
			Text: fmt.Sprintf("Submitted by %s", m.Author.Username),
		},
		Timestamp: time.Now().Format(time.RFC3339),
	}

	// If first message and we have Polkassembly integration, post it
	polkassemblyAPIKey := data.GetSetting("polkassembly_api_key")
	if msgCount == 1 && polkassemblyAPIKey != "" && network.PolkassemblyPrefix != "" {
		go h.postToPolkassembly(strings.ToLower(network.Name), threadInfo.RefID, feedbackMsg, link, network.PolkassemblyPrefix)
	}

	// Publish to Redis
	if h.config.Redis != nil {
		_ = data.PublishMessage(context.Background(), h.config.Redis, map[string]interface{}{
			"proposal": fmt.Sprintf("%s/%d", strings.ToLower(network.Name), threadInfo.RefID),
			"author":   daoMember.Address,
			"body":     feedbackMsg,
			"time":     time.Now().Unix(),
			"id":       msgID,
			"network":  strings.ToLower(network.Name),
			"ref_id":   threadInfo.RefID,
		})
	}

	s.ChannelMessageSendEmbed(m.ChannelID, embed)

	log.Printf("Feedback submitted by %s (%s) for %s/%d: %d chars",
		m.Author.Username, daoMember.Address, network.Name, threadInfo.RefID, len(feedbackMsg))

	return nil
}

func (h *Handler) postToPolkassembly(network string, refNum uint64, message, link, polkassemblyPrefix string) {
	log.Printf("Would post to Polkassembly: %s/%d", network, refNum)
	// TODO: Implement actual Polkassembly API integration
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
