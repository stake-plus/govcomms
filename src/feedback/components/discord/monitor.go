package discord

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/stake-plus/govcomms/src/bot/components/network"
	"github.com/stake-plus/govcomms/src/bot/components/referendum"
	"github.com/stake-plus/govcomms/src/bot/types"
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
	log.Println("Starting Discord message monitor for Polkassembly replies")

	ticker := time.NewTicker(30 * time.Second)
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
	// Query for new external messages (replies from Polkassembly)
	var messages []types.RefMessage
	err := m.config.DB.Where("created_at > ? AND internal = ? AND polkassembly_user_id IS NOT NULL",
		m.lastCheck, false).
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
	var ref types.Ref
	if err := m.config.DB.First(&ref, msg.RefID).Error; err != nil {
		return fmt.Errorf("get ref: %w", err)
	}

	net := m.config.NetworkManager.GetByID(ref.NetworkID)
	if net == nil || net.DiscordChannelID == "" {
		return fmt.Errorf("no channel for network %d", ref.NetworkID)
	}

	// Get thread info for this referendum
	thread, err := m.config.RefManager.GetThreadInfo(ref.NetworkID, uint32(ref.RefID))
	if err != nil {
		return fmt.Errorf("find thread: %w", err)
	}

	embed := m.buildMessageEmbed(msg, ref, net)
	_, err = m.config.Session.ChannelMessageSendEmbed(thread.ThreadID, embed)
	return err
}

func (m *MessageMonitor) buildMessageEmbed(msg types.RefMessage, ref types.Ref, net *types.Network) *discordgo.MessageEmbed {
	author := msg.Author
	if msg.PolkassemblyUsername != "" {
		author = msg.PolkassemblyUsername
	}

	return &discordgo.MessageEmbed{
		Author: &discordgo.MessageEmbedAuthor{
			Name: fmt.Sprintf("Reply from %s (via Polkassembly)", author),
		},
		Description: msg.Body,
		Color:       0x0099ff,
		Timestamp:   msg.CreatedAt.Format(time.RFC3339),
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("Via Polkassembly | %s #%d", net.Name, ref.RefID),
		},
		Fields: []*discordgo.MessageEmbedField{
			{
				Name: "View on Polkassembly",
				Value: fmt.Sprintf("[Click here](https://%s.polkassembly.io/referendum/%d)",
					strings.ToLower(net.Name), ref.RefID),
			},
		},
	}
}
