package discord

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/stake-plus/govcomms/src/GCApi/data"
	"github.com/stake-plus/govcomms/src/GCApi/types"
	"github.com/stake-plus/govcomms/src/GCBot/components/network"
	"github.com/stake-plus/govcomms/src/GCBot/components/referendum"
	"gorm.io/gorm"
)

type MonitorConfig struct {
	DB             *gorm.DB
	NetworkManager *network.Manager
	RefManager     *referendum.Manager
	Session        *discordgo.Session
	GuildID        string
}

type MessageMonitor struct {
	config    MonitorConfig
	lastCheck time.Time
}

func NewMessageMonitor(config MonitorConfig) *MessageMonitor {
	return &MessageMonitor{
		config:    config,
		lastCheck: time.Now(),
	}
}

func (m *MessageMonitor) Start(ctx context.Context) {
	log.Println("Starting Discord message monitor")
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("Stopping Discord message monitor")
			return
		case <-ticker.C:
			if err := m.checkNewMessages(); err != nil {
				log.Printf("Error checking new messages: %v", err)
			}
		}
	}
}

func (m *MessageMonitor) checkNewMessages() error {
	// Query for new external messages
	var messages []types.RefMessage
	err := m.config.DB.Where("created_at > ? AND internal = ?", m.lastCheck, false).
		Order("created_at ASC").
		Find(&messages).Error
	if err != nil {
		return err
	}

	for _, msg := range messages {
		if err := m.postMessage(msg); err != nil {
			log.Printf("Error posting message %d: %v", msg.ID, err)
		}
	}

	if len(messages) > 0 {
		m.lastCheck = time.Now()
	}

	return nil
}

func (m *MessageMonitor) postMessage(msg types.RefMessage) error {
	// Get referendum info
	var ref types.Ref
	if err := m.config.DB.First(&ref, msg.RefID).Error; err != nil {
		return fmt.Errorf("get ref: %w", err)
	}

	// Get network
	net := m.config.NetworkManager.GetByID(ref.NetworkID)
	if net == nil || net.DiscordChannelID == "" {
		return fmt.Errorf("no channel for network %d", ref.NetworkID)
	}

	// Find thread
	thread, err := m.config.RefManager.FindThread(
		m.config.Session,
		m.config.GuildID,
		net.DiscordChannelID,
		int(ref.RefID),
	)
	if err != nil {
		return fmt.Errorf("find thread: %w", err)
	}

	// Build and send embed
	embed := m.buildMessageEmbed(msg, ref, net)
	_, err = m.config.Session.ChannelMessageSendEmbed(thread.ID, embed)
	return err
}

func (m *MessageMonitor) buildMessageEmbed(msg types.RefMessage, ref types.Ref, net *types.Network) *discordgo.MessageEmbed {
	gcURL := data.GetSetting("gc_url")
	if gcURL == "" {
		gcURL = "http://localhost:3000"
	}

	return &discordgo.MessageEmbed{
		Author: &discordgo.MessageEmbedAuthor{
			Name: fmt.Sprintf("Message from %s", formatAddress(msg.Author)),
		},
		Description: msg.Body,
		Color:       0x0099ff,
		Timestamp:   msg.CreatedAt.Format(time.RFC3339),
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("Via GovComms | %s #%d", net.Name, ref.RefID),
		},
		Fields: []*discordgo.MessageEmbedField{
			{
				Name: "Continue Discussion",
				Value: fmt.Sprintf("[Click here](%s/%s/%d)",
					gcURL, strings.ToLower(net.Name), ref.RefID),
			},
		},
	}
}

func formatAddress(addr string) string {
	if len(addr) > 16 {
		return addr[:8] + "..." + addr[len(addr)-8:]
	}
	return addr
}
