package feedback

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/stake-plus/govcomms/src/feedback/components/network"
	"github.com/stake-plus/govcomms/src/feedback/components/polkassembly"
	"github.com/stake-plus/govcomms/src/feedback/components/referendum"
	"github.com/stake-plus/govcomms/src/feedback/types"
	"gorm.io/gorm"
)

type Config struct {
	DB                  *gorm.DB
	NetworkManager      *network.Manager
	RefManager          *referendum.Manager
	FeedbackRoleID      string
	GuildID             string
	PolkassemblyService *polkassembly.Service
}

type Handler struct {
	config       Config
	rateLimiter  *RateLimiter
	polkassembly *polkassembly.Service
}

func NewHandler(config Config) *Handler {
	return &Handler{
		config:       config,
		rateLimiter:  NewRateLimiter(30 * time.Second),
		polkassembly: config.PolkassemblyService,
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

	if !h.rateLimiter.CanUse(m.Author.ID) {
		timeLeft := h.rateLimiter.TimeUntilNext(m.Author.ID)
		minutes := int(timeLeft.Minutes())
		seconds := int(timeLeft.Seconds()) % 60
		msg := fmt.Sprintf("Please wait %d minutes and %d seconds before using this command again.",
			minutes, seconds)
		s.ChannelMessageSend(m.ChannelID, msg)
		return
	}

	if !h.hasRole(s, h.config.GuildID, m.Author.ID, h.config.FeedbackRoleID) {
		s.ChannelMessageSend(m.ChannelID, "You don't have permission to use this command.")
		return
	}

	parts := strings.SplitN(m.Content, " ", 2)
	if len(parts) < 2 {
		s.ChannelMessageSend(m.ChannelID, "Usage: !feedback Your feedback message")
		return
	}

	feedbackMsg := parts[1]
	if len(feedbackMsg) < 10 || len(feedbackMsg) > 5000 {
		s.ChannelMessageSend(m.ChannelID, "Feedback message must be between 10 and 5000 characters")
		return
	}

	// Try to find thread info from channel ID
	threadInfo, err := h.config.RefManager.FindThread(m.ChannelID)
	if err != nil || threadInfo == nil {
		log.Printf("Channel %s is not a recognized referendum thread", m.ChannelID)
		s.ChannelMessageSend(m.ChannelID, "This command must be used in a referendum thread.")
		return
	}

	network := h.config.NetworkManager.GetByID(threadInfo.NetworkID)
	if network == nil {
		log.Printf("Network not found for ID %d", threadInfo.NetworkID)
		s.ChannelMessageSend(m.ChannelID, "Failed to process feedback. Please try again.")
		return
	}

	if err := h.processFeedbackFromThread(s, m, threadInfo, network, feedbackMsg); err != nil {
		log.Printf("Error processing feedback: %v", err)
		s.ChannelMessageSend(m.ChannelID, "Failed to process feedback. Please try again.")
		return
	}
}

func (h *Handler) processFeedbackFromThread(s *discordgo.Session, m *discordgo.MessageCreate,
	threadInfo *referendum.ThreadInfo, network *types.Network, feedbackMsg string) error {

	log.Printf("Processing feedback for %s ref #%d", network.Name, threadInfo.RefID)

	author := "DAO Feedback"
	var isFirstMessage bool
	var commentID string
	var msgID uint64

	// First transaction - save the message
	err := h.config.DB.Transaction(func(tx *gorm.DB) error {
		var ref types.Ref
		if err := tx.First(&ref, threadInfo.RefDBID).Error; err != nil {
			return fmt.Errorf("referendum not found: %w", err)
		}

		// Check if this is the first internal message
		var msgCount int64
		tx.Model(&types.RefMessage{}).Where("ref_id = ? AND internal = ?", ref.ID, true).Count(&msgCount)
		isFirstMessage = msgCount == 0

		log.Printf("Internal message count for ref %d: %d, isFirstMessage: %v", ref.ID, msgCount, isFirstMessage)

		msg := types.RefMessage{
			RefID:     ref.ID,
			Author:    author,
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

	// Post to Polkassembly if first message (outside transaction)
	if isFirstMessage {
		if h.polkassembly != nil {
			log.Printf("Attempting to post first message to Polkassembly for %s ref #%d", network.Name, threadInfo.RefID)
			commentID, err = h.polkassembly.PostFirstMessage(strings.ToLower(network.Name), int(threadInfo.RefID), feedbackMsg)
			if err != nil {
				log.Printf("Failed to post to Polkassembly: %v", err)
				// Check if it's a timeout - the post might have succeeded
				if strings.Contains(err.Error(), "timeout") || strings.Contains(err.Error(), "deadline exceeded") {
					log.Printf("Timeout posting to Polkassembly - will need to manually check for comment ID for ref %d", threadInfo.RefID)
				}
			} else if commentID != "" {
				// Update the message with the comment ID
				if err := h.config.DB.Model(&types.RefMessage{}).Where("id = ?", msgID).Update("polkassembly_comment_id", commentID).Error; err != nil {
					log.Printf("Failed to store Polkassembly comment ID in message: %v", err)
				} else {
					log.Printf("Stored Polkassembly comment ID %s for message %d", commentID, msgID)
				}
			}
		} else {
			log.Printf("Polkassembly service is not available - skipping Polkassembly post")
		}
	} else {
		log.Printf("Not the first message for ref %d - skipping Polkassembly post", threadInfo.RefID)
	}

	// Send Discord response
	embed := &discordgo.MessageEmbed{
		Title:       "Feedback Submitted",
		Description: fmt.Sprintf("Your feedback for %s/%d has been submitted.", network.Name, threadInfo.RefID),
		Color:       0x00ff00,
		Footer: &discordgo.MessageEmbedFooter{
			Text: "Submitted via DAO Feedback",
		},
		Timestamp: time.Now().Format(time.RFC3339),
	}

	if isFirstMessage && commentID != "" {
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:  "Polkassembly",
			Value: fmt.Sprintf("✅ Posted to Polkassembly with comment ID %s", commentID),
		})

		// Add link to the comment
		polkassemblyURL := fmt.Sprintf("https://%s.polkassembly.io/referenda/%d#%s", strings.ToLower(network.Name), threadInfo.RefID, commentID)
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:  "View on Polkassembly",
			Value: fmt.Sprintf("[Direct link to comment](%s)", polkassemblyURL),
		})
	} else if isFirstMessage && h.polkassembly != nil && commentID == "" {
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:  "Polkassembly",
			Value: "⚠️ Posted to Polkassembly but couldn't confirm comment ID (timeout)",
		})
	} else if isFirstMessage && h.polkassembly == nil {
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:  "Polkassembly",
			Value: "❌ Polkassembly service unavailable",
		})
	}

	s.ChannelMessageSendEmbed(m.ChannelID, embed)
	log.Printf("Feedback submitted for %s/%d: %d chars", network.Name, threadInfo.RefID, len(feedbackMsg))

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
