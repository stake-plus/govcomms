package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/redis/go-redis/v9"
	"github.com/stake-plus/polkadot-gov-comms/src/api/config"
	"github.com/stake-plus/polkadot-gov-comms/src/api/data"
	"github.com/stake-plus/polkadot-gov-comms/src/api/types"
	"gorm.io/gorm"
)

type DiscordBot struct {
	session         *discordgo.Session
	rdb             *redis.Client
	db              *gorm.DB
	feedbackRoleID  string
	feedbackCommand string
	apiURL          string
	guildID         string
	channels        map[uint8]string // networkID -> channelID
}

type StreamMessage struct {
	Proposal  string
	Author    string
	Body      string
	Time      int64
	ID        uint64
	Network   string
	NetworkID uint8
	RefID     uint64
}

func NewDiscordBot(token, feedbackRoleID, guildID, apiURL string, rdb *redis.Client, db *gorm.DB) (*DiscordBot, error) {
	dg, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, err
	}

	bot := &DiscordBot{
		session:         dg,
		rdb:             rdb,
		db:              db,
		feedbackRoleID:  feedbackRoleID,
		feedbackCommand: "!feedback",
		apiURL:          apiURL,
		guildID:         guildID,
		channels:        make(map[uint8]string),
	}

	// Load channel configuration
	if err := bot.loadChannelConfig(); err != nil {
		return nil, err
	}

	dg.AddHandler(bot.handleMessageCreate)
	dg.AddHandler(bot.handleReady)

	dg.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentsMessageContent

	return bot, nil
}

func (b *DiscordBot) Start() error {
	return b.session.Open()
}

func (b *DiscordBot) Stop() error {
	return b.session.Close()
}

func (b *DiscordBot) loadChannelConfig() error {
	var networks []types.Network
	if err := b.db.Where("discord_channel_id IS NOT NULL AND discord_channel_id != ''").Find(&networks).Error; err != nil {
		return err
	}

	for _, net := range networks {
		b.channels[net.ID] = net.DiscordChannelID
	}

	if len(b.channels) == 0 {
		log.Println("No channels configured, using defaults")
	}

	return nil
}

func (b *DiscordBot) handleReady(s *discordgo.Session, event *discordgo.Ready) {
	log.Printf("Discord bot logged in as %s", event.User.Username)

	// Setup channels if not configured
	if len(b.channels) == 0 {
		b.setupDefaultChannels(s)
	}
}

func (b *DiscordBot) handleMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == s.State.User.ID {
		return
	}

	if !strings.HasPrefix(m.Content, b.feedbackCommand) {
		return
	}

	// Check if user has feedback role
	member, err := s.GuildMember(b.guildID, m.Author.ID)
	if err != nil {
		return
	}

	hasRole := false
	for _, roleID := range member.Roles {
		if roleID == b.feedbackRoleID {
			hasRole = true
			break
		}
	}

	if !hasRole {
		s.ChannelMessageSend(m.ChannelID, "You don't have permission to use this command.")
		return
	}

	// Parse feedback command: !feedback polkadot/123 Your feedback message here
	parts := strings.SplitN(m.Content, " ", 3)
	if len(parts) < 3 {
		s.ChannelMessageSend(m.ChannelID, "Usage: !feedback network/ref_number Your feedback message")
		return
	}

	proposalRef := parts[1]
	feedbackMsg := parts[2]

	// Validate proposal reference format
	refParts := strings.Split(proposalRef, "/")
	if len(refParts) != 2 {
		s.ChannelMessageSend(m.ChannelID, "Invalid format. Use: network/ref_number (e.g., polkadot/123)")
		return
	}

	network := strings.ToLower(refParts[0])
	if network != "polkadot" && network != "kusama" {
		s.ChannelMessageSend(m.ChannelID, "Network must be 'polkadot' or 'kusama'")
		return
	}

	refNum, err := strconv.Atoi(refParts[1])
	if err != nil || refNum < 0 {
		s.ChannelMessageSend(m.ChannelID, "Invalid referendum number")
		return
	}

	// Get Discord user's linked address from dao_members table
	var daoMember types.DaoMember
	if err := b.db.Where("discord = ?", m.Author.Username).First(&daoMember).Error; err != nil {
		s.ChannelMessageSend(m.ChannelID, "Your Discord account is not linked to a Polkadot address. Please contact an admin.")
		return
	}

	// Get proposal
	netID := uint8(1)
	if network == "kusama" {
		netID = 2
	}

	var ref types.Ref
	if err := b.db.First(&ref, "network_id = ? AND ref_id = ?", netID, refNum).Error; err != nil {
		// Create proposal if it doesn't exist
		ref = types.Ref{
			NetworkID: netID,
			RefID:     uint64(refNum),
			Submitter: daoMember.Address,
			Status:    "Unknown",
			Title:     fmt.Sprintf("%s Referendum #%d", strings.Title(network), refNum),
		}
		if err := b.db.Create(&ref).Error; err != nil {
			s.ChannelMessageSend(m.ChannelID, "Failed to create proposal record. Please try again.")
			return
		}
	}

	// Create proponent record
	proponent := types.RefProponent{
		RefID:   ref.ID,
		Address: daoMember.Address,
		Role:    "dao_member",
		Active:  1,
	}
	b.db.FirstOrCreate(&proponent, types.RefProponent{RefID: ref.ID, Address: daoMember.Address})

	// Store message
	msg := types.RefMessage{
		RefID:     ref.ID,
		Author:    daoMember.Address,
		Body:      feedbackMsg,
		CreatedAt: time.Now(),
		Internal:  true,
	}
	if err := b.db.Create(&msg).Error; err != nil {
		s.ChannelMessageSend(m.ChannelID, "Failed to store message. Please try again.")
		return
	}

	// Check if this is the first message
	var msgCount int64
	b.db.Model(&types.RefMessage{}).Where("ref_id = ?", ref.ID).Count(&msgCount)

	// Store link to frontend
	link := fmt.Sprintf("%s/%s/%d", b.apiURL, network, refNum)

	// Publish to Redis stream for Polkassembly posting if first message
	if msgCount == 1 {
		_ = b.rdb.XAdd(context.Background(), &redis.XAddArgs{
			Stream: "govcomms.discord.feedback",
			Values: map[string]interface{}{
				"proposal":   proposalRef,
				"author":     m.Author.Username,
				"body":       feedbackMsg,
				"time":       time.Now().Unix(),
				"channel":    m.ChannelID,
				"message_id": m.ID,
				"is_first":   "true",
			},
		}).Err()
	}

	// Send success message with link
	embed := &discordgo.MessageEmbed{
		Title:       "Feedback Submitted",
		Description: fmt.Sprintf("Your feedback for %s has been submitted.", proposalRef),
		Color:       0x00ff00,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:  "Continue Discussion",
				Value: fmt.Sprintf("[Click here](%s) to continue the conversation", link),
			},
		},
		Timestamp: time.Now().Format(time.RFC3339),
	}

	s.ChannelMessageSendEmbed(m.ChannelID, embed)
}

func (b *DiscordBot) setupDefaultChannels(s *discordgo.Session) {
	channels, err := s.GuildChannels(b.guildID)
	if err != nil {
		log.Printf("Failed to get guild channels: %v", err)
		return
	}

	for _, ch := range channels {
		lowerName := strings.ToLower(ch.Name)
		if strings.Contains(lowerName, "polkadot") && strings.Contains(lowerName, "referendum") {
			b.channels[1] = ch.ID
			b.saveChannelConfig(1, ch.ID)
			log.Printf("Set Polkadot referenda channel: %s", ch.Name)
		} else if strings.Contains(lowerName, "kusama") && strings.Contains(lowerName, "referendum") {
			b.channels[2] = ch.ID
			b.saveChannelConfig(2, ch.ID)
			log.Printf("Set Kusama referenda channel: %s", ch.Name)
		}
	}
}

func (b *DiscordBot) saveChannelConfig(networkID uint8, channelID string) {
	b.db.Model(&types.Network{}).Where("id = ?", networkID).Update("discord_channel_id", channelID)
}

func (b *DiscordBot) findReferendumThread(channelID string, refNum int) (*discordgo.Channel, error) {
	// Get all threads in the channel
	threads, err := b.session.GuildThreadsActive(b.guildID)
	if err != nil {
		return nil, err
	}

	// Pattern to match thread names like "1655: title" or "1655 - title"
	pattern := fmt.Sprintf(`^%d\s*[:|-]`, refNum)
	re := regexp.MustCompile(pattern)

	for _, thread := range threads.Threads {
		if thread.ParentID == channelID && re.MatchString(thread.Name) {
			return thread, nil
		}
	}

	// Check archived threads using public archive threads
	publicThreads, err := b.session.ThreadsArchived(channelID, nil, 100)
	if err == nil {
		for _, thread := range publicThreads.Threads {
			if re.MatchString(thread.Name) {
				// Unarchive the thread
				_, err := b.session.ChannelEdit(thread.ID, &discordgo.ChannelEdit{
					Archived: boolPtr(false),
				})
				if err == nil {
					return thread, nil
				}
			}
		}
	}

	// Check private archived threads
	privateThreads, err := b.session.ThreadsPrivateArchived(channelID, nil, 100)
	if err == nil {
		for _, thread := range privateThreads.Threads {
			if re.MatchString(thread.Name) {
				// Unarchive the thread
				_, err := b.session.ChannelEdit(thread.ID, &discordgo.ChannelEdit{
					Archived: boolPtr(false),
				})
				if err == nil {
					return thread, nil
				}
			}
		}
	}

	return nil, fmt.Errorf("thread not found for referendum %d", refNum)
}

func (b *DiscordBot) postToReferendumThread(msg StreamMessage) error {
	// Get the channel for this network
	channelID, exists := b.channels[uint8(msg.NetworkID)]
	if !exists {
		return fmt.Errorf("no channel configured for network %d", msg.NetworkID)
	}

	// Find the thread
	thread, err := b.findReferendumThread(channelID, int(msg.RefID))
	if err != nil {
		return fmt.Errorf("find thread: %w", err)
	}

	// Format the message
	embed := &discordgo.MessageEmbed{
		Author: &discordgo.MessageEmbedAuthor{
			Name: fmt.Sprintf("Message from %s", formatAddress(msg.Author)),
		},
		Description: msg.Body,
		Color:       0x0099ff,
		Timestamp:   time.Unix(msg.Time, 0).Format(time.RFC3339),
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("Via GovComms | %s #%d", msg.Network, msg.RefID),
		},
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:  "Continue Discussion",
				Value: fmt.Sprintf("[Click here](%s/%s/%d)", b.apiURL, msg.Network, msg.RefID),
			},
		},
	}

	_, err = b.session.ChannelMessageSendEmbed(thread.ID, embed)
	return err
}

func (b *DiscordBot) listenForMessages(ctx context.Context) {
	// Initialize last ID to start from the beginning
	lastID := "0"

	for {
		select {
		case <-ctx.Done():
			return
		default:
			// Read from Redis stream
			streams, err := b.rdb.XRead(ctx, &redis.XReadArgs{
				Streams: []string{"govcomms.messages", lastID},
				Count:   10,
				Block:   5 * time.Second,
			}).Result()

			if err != nil {
				if err != redis.Nil {
					log.Printf("Error reading stream: %v", err)
				}
				continue
			}

			for _, stream := range streams {
				for _, msg := range stream.Messages {
					var m StreamMessage

					// Parse message fields
					if proposal, ok := msg.Values["proposal"].(string); ok {
						m.Proposal = proposal
					}
					if author, ok := msg.Values["author"].(string); ok {
						m.Author = author
					}
					if body, ok := msg.Values["body"].(string); ok {
						m.Body = body
					}
					if timeStr, ok := msg.Values["time"].(string); ok {
						if t, err := strconv.ParseInt(timeStr, 10, 64); err == nil {
							m.Time = t
						}
					}
					if network, ok := msg.Values["network"].(string); ok {
						m.Network = network
						if network == "polkadot" {
							m.NetworkID = 1
						} else if network == "kusama" {
							m.NetworkID = 2
						}
					}
					if refIDStr, ok := msg.Values["ref_id"].(string); ok {
						if r, err := strconv.ParseUint(refIDStr, 10, 64); err == nil {
							m.RefID = r
						}
					}

					// Post to Discord
					if err := b.postToReferendumThread(m); err != nil {
						log.Printf("Failed to post to Discord: %v", err)
					} else {
						log.Printf("Posted message to Discord for %s #%d", m.Network, m.RefID)
					}

					// Update last ID
					lastID = msg.ID
				}
			}
		}
	}
}

func formatAddress(addr string) string {
	if len(addr) > 16 {
		return addr[:8] + "..." + addr[len(addr)-8:]
	}
	return addr
}

func boolPtr(b bool) *bool {
	return &b
}

func main() {
	// Load config from env
	token := os.Getenv("DISCORD_TOKEN")
	if token == "" {
		log.Fatal("DISCORD_TOKEN not set")
	}

	feedbackRoleID := os.Getenv("FEEDBACK_ROLE_ID")
	if feedbackRoleID == "" {
		log.Fatal("FEEDBACK_ROLE_ID not set")
	}

	guildID := os.Getenv("GUILD_ID")
	if guildID == "" {
		log.Fatal("GUILD_ID not set")
	}

	apiURL := os.Getenv("FRONTEND_URL")
	if apiURL == "" {
		apiURL = "https://govcomms.chaosdao.org"
	}

	cfg := config.Load()
	rdb := data.MustRedis(cfg.RedisURL)
	db := data.MustMySQL(cfg.MySQLDSN)

	// Ensure tables exist
	if err := db.AutoMigrate(&types.Network{}, &types.DaoMember{}, &types.Ref{}, &types.RefMessage{}, &types.RefProponent{}); err != nil {
		log.Fatalf("Failed to migrate tables: %v", err)
	}

	bot, err := NewDiscordBot(token, feedbackRoleID, guildID, apiURL, rdb, db)
	if err != nil {
		log.Fatalf("Failed to create bot: %v", err)
	}

	if err := bot.Start(); err != nil {
		log.Fatalf("Failed to start bot: %v", err)
	}

	log.Println("Discord bot is running. Press CTRL-C to exit.")

	// Start listening for messages from the API
	ctx, cancel := context.WithCancel(context.Background())
	go bot.listenForMessages(ctx)

	// Wait for interrupt
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc

	cancel()
	bot.Stop()
}
