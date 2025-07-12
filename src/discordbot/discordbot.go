package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
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
}

type StreamMessage struct {
	Proposal string `json:"proposal"`
	Author   string `json:"author"`
	Body     string `json:"body"`
	Time     int64  `json:"time"`
	ID       uint64 `json:"id"`
	Network  string `json:"network"`
	RefID    uint64 `json:"ref_id"`
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
	}

	dg.AddHandler(bot.messageCreate)
	dg.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentsMessageContent

	return bot, nil
}

func (b *DiscordBot) Start() error {
	return b.session.Open()
}

func (b *DiscordBot) Stop() error {
	return b.session.Close()
}

func (b *DiscordBot) messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
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

	var prop types.Proposal
	if err := b.db.First(&prop, "network_id = ? AND ref_id = ?", netID, refNum).Error; err != nil {
		// Create proposal if it doesn't exist
		prop = types.Proposal{
			NetworkID: netID,
			RefID:     uint64(refNum),
			Submitter: daoMember.Address,
			Status:    "Unknown",
			Title:     fmt.Sprintf("%s Referendum #%d", strings.Title(network), refNum),
		}
		if err := b.db.Create(&prop).Error; err != nil {
			s.ChannelMessageSend(m.ChannelID, "Failed to create proposal record. Please try again.")
			return
		}
	}

	// Create participant record
	participant := types.ProposalParticipant{
		ProposalID: prop.ID,
		Address:    daoMember.Address,
		Role:       "dao_member",
	}
	b.db.FirstOrCreate(&participant, types.ProposalParticipant{ProposalID: prop.ID, Address: daoMember.Address})

	// Store message
	msg := types.Message{
		ProposalID: prop.ID,
		Author:     daoMember.Address,
		Body:       feedbackMsg,
		CreatedAt:  time.Now(),
	}
	if err := b.db.Create(&msg).Error; err != nil {
		s.ChannelMessageSend(m.ChannelID, "Failed to store message. Please try again.")
		return
	}

	// Check if this is the first message
	var msgCount int64
	b.db.Model(&types.Message{}).Where("proposal_id = ?", prop.ID).Count(&msgCount)

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

func (b *DiscordBot) listenForMessages(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			// Read from Redis stream
			streams, err := b.rdb.XRead(ctx, &redis.XReadArgs{
				Streams: []string{"govcomms.messages", "$"},
				Block:   5 * time.Second,
			}).Result()

			if err != nil && err != redis.Nil {
				log.Printf("Error reading stream: %v", err)
				continue
			}

			for _, stream := range streams {
				for _, msg := range stream.Messages {
					var m StreamMessage
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
					}
					if refIDStr, ok := msg.Values["ref_id"].(string); ok {
						if r, err := strconv.ParseUint(refIDStr, 10, 64); err == nil {
							m.RefID = r
						}
					}

					// Post to appropriate Discord channel
					b.postToDiscord(m)
				}
			}
		}
	}
}

func (b *DiscordBot) postToDiscord(msg StreamMessage) {
	// Find the appropriate channel/thread for this referendum
	channelName := fmt.Sprintf("ref-%s-%d", msg.Network, msg.RefID)

	guilds := b.session.State.Guilds
	var targetChannel *discordgo.Channel

	for _, guild := range guilds {
		channels, _ := b.session.GuildChannels(guild.ID)
		for _, channel := range channels {
			if strings.ToLower(channel.Name) == channelName {
				targetChannel = channel
				break
			}
		}
		if targetChannel != nil {
			break
		}
	}

	if targetChannel == nil {
		log.Printf("Channel not found for %s", msg.Proposal)
		return
	}

	// Format the address
	shortAddr := msg.Author
	if len(shortAddr) > 16 {
		shortAddr = shortAddr[:8] + "..." + shortAddr[len(shortAddr)-8:]
	}

	// Create embed for the message
	embed := &discordgo.MessageEmbed{
		Author: &discordgo.MessageEmbedAuthor{
			Name: fmt.Sprintf("Response from %s", shortAddr),
		},
		Description: msg.Body,
		Color:       0x0099ff,
		Timestamp:   time.Unix(msg.Time, 0).Format(time.RFC3339),
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("Via GovComms | %s #%d", msg.Network, msg.RefID),
		},
	}

	_, err := b.session.ChannelMessageSendEmbed(targetChannel.ID, embed)
	if err != nil {
		log.Printf("Failed to send message to Discord: %v", err)
	}
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
