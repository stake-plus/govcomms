package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/stake-plus/polkadot-gov-comms/src/api/data"
	"github.com/stake-plus/polkadot-gov-comms/src/api/types"
	"gorm.io/gorm"
)

type DiscordBot struct {
	session         *discordgo.Session
	db              *gorm.DB
	feedbackRoleID  string
	feedbackCommand string
	guildID         string
	channels        map[uint8]string // networkID -> channelID
	apiClient       *APIClient
}

type APIClient struct {
	baseURL string
	token   string
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

func NewDiscordBot(token, feedbackRoleID, guildID string, db *gorm.DB) (*DiscordBot, error) {
	// Load settings
	if err := data.LoadSettings(db); err != nil {
		return nil, fmt.Errorf("load settings: %w", err)
	}

	dg, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, err
	}

	apiURL := data.GetSetting("gcapi_url")
	if apiURL == "" {
		apiURL = "http://localhost:443" // development default
	}

	bot := &DiscordBot{
		session:         dg,
		db:              db,
		feedbackRoleID:  feedbackRoleID,
		feedbackCommand: "!feedback",
		guildID:         guildID,
		channels:        make(map[uint8]string),
		apiClient:       &APIClient{baseURL: apiURL},
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

	// Store link to frontend using gc_url from settings
	gcURL := data.GetSetting("gc_url")
	if gcURL == "" {
		gcURL = "http://localhost:3000" // development default
	}
	link := fmt.Sprintf("%s/%s/%d", gcURL, network, refNum)

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

	// Get gc_url from settings
	gcURL := data.GetSetting("gc_url")
	if gcURL == "" {
		gcURL = "http://localhost:3000" // development default
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
				Value: fmt.Sprintf("[Click here](%s/%s/%d)", gcURL, msg.Network, msg.RefID),
			},
		},
	}

	_, err = b.session.ChannelMessageSendEmbed(thread.ID, embed)
	return err
}

func (b *DiscordBot) postMessageToAPI(proposalRef, author, body string) error {
	url := fmt.Sprintf("%s/v1/messages", b.apiClient.baseURL)

	payload := map[string]interface{}{
		"proposalRef": proposalRef,
		"body":        body,
		"emails":      []string{},
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+b.apiClient.token)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	return nil
}

func (b *DiscordBot) listenForMessages(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Poll for new messages from API
			if err := b.pollNewMessages(); err != nil {
				log.Printf("Error polling messages: %v", err)
			}
		}
	}
}

func (b *DiscordBot) pollNewMessages() error {
	// This would need to be implemented to poll the API for new messages
	// and post them to Discord threads
	// For now, this is a placeholder
	return nil
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

	// Connect to database using environment variable or default
	mysqlDSN := os.Getenv("MYSQL_DSN")
	if mysqlDSN == "" {
		mysqlDSN = "govcomms:DK3mfv93jf4m@tcp(172.16.254.7:3306)/govcomms"
	}
	db := data.MustMySQL(mysqlDSN)

	// Ensure tables exist
	if err := db.AutoMigrate(&types.Network{}, &types.DaoMember{}, &types.Ref{}, &types.RefMessage{}, &types.RefProponent{}); err != nil {
		log.Fatalf("Failed to migrate tables: %v", err)
	}

	bot, err := NewDiscordBot(token, feedbackRoleID, guildID, db)
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
