package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/redis/go-redis/v9"
	"github.com/stake-plus/govcomms/src/GCApi/data"
	"github.com/stake-plus/govcomms/src/GCApi/types"
	"gorm.io/gorm"
)

type DiscordBot struct {
	session         *discordgo.Session
	db              *gorm.DB
	rdb             *redis.Client
	feedbackRoleID  string
	feedbackCommand string
	guildID         string
	networks        map[uint8]*types.Network // Store full network info
	apiClient       *APIClient
	rateLimiter     *UserRateLimiter
	mu              sync.RWMutex
}

type APIClient struct {
	baseURL string
	token   string
	client  *http.Client
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

type UserRateLimiter struct {
	users map[string]time.Time
	mu    sync.Mutex
	limit time.Duration
}

func NewUserRateLimiter(limit time.Duration) *UserRateLimiter {
	return &UserRateLimiter{
		users: make(map[string]time.Time),
		limit: limit,
	}
}

func (rl *UserRateLimiter) CanUse(userID string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	lastUse, exists := rl.users[userID]
	if !exists || time.Since(lastUse) >= rl.limit {
		rl.users[userID] = time.Now()
		return true
	}

	return false
}

func (rl *UserRateLimiter) TimeUntilNext(userID string) time.Duration {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	lastUse, exists := rl.users[userID]
	if !exists {
		return 0
	}

	elapsed := time.Since(lastUse)
	if elapsed >= rl.limit {
		return 0
	}

	return rl.limit - elapsed
}

func NewDiscordBot(token, feedbackRoleID, guildID string, db *gorm.DB, rdb *redis.Client) (*DiscordBot, error) {
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
		networks:        make(map[uint8]*types.Network),
		apiClient: &APIClient{
			baseURL: apiURL,
			client:  &http.Client{Timeout: 30 * time.Second},
		},
		rateLimiter: NewUserRateLimiter(5 * time.Minute),
		rdb:         rdb,
	}

	// Load network configuration
	if err := bot.loadNetworkConfig(); err != nil {
		return nil, err
	}

	dg.AddHandler(bot.handleMessageCreate)
	dg.AddHandler(bot.handleReady)
	dg.AddHandler(bot.handleThreadUpdate)

	dg.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentsMessageContent | discordgo.IntentsGuilds

	return bot, nil
}

func (b *DiscordBot) Start() error {
	return b.session.Open()
}

func (b *DiscordBot) Stop() error {
	return b.session.Close()
}

func (b *DiscordBot) loadNetworkConfig() error {
	var networks []types.Network
	if err := b.db.Find(&networks).Error; err != nil {
		return err
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	for i := range networks {
		net := &networks[i]
		b.networks[net.ID] = net
		log.Printf("Loaded network: %s (ID: %d, Channel: %s)", net.Name, net.ID, net.DiscordChannelID)
	}

	return nil
}

func (b *DiscordBot) handleReady(s *discordgo.Session, event *discordgo.Ready) {
	log.Printf("Discord bot logged in as %s", event.User.Username)

	// Start monitoring for new messages from the API
	go b.monitorAPIMessages(context.Background())
}

func (b *DiscordBot) handleThreadUpdate(s *discordgo.Session, t *discordgo.ThreadUpdate) {
	// Monitor for new threads in referendum channels
	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, network := range b.networks {
		if t.ParentID == network.DiscordChannelID {
			// Extract referendum ID from thread name
			refID := b.extractRefIDFromThreadName(t.Name)
			if refID > 0 {
				log.Printf("Detected referendum thread: %s (Ref #%d) in %s channel", t.Name, refID, network.Name)
			}
		}
	}
}

func (b *DiscordBot) extractRefIDFromThreadName(name string) uint64 {
	// Common patterns: "1655: title", "1655 - title", "#1655 title"
	patterns := []string{
		`^#?(\d+)\s*[:|-]`,
		`^#?(\d+)\s+`,
		`\[(\d+)\]`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(name)
		if len(matches) > 1 {
			if id, err := strconv.ParseUint(matches[1], 10, 64); err == nil {
				return id
			}
		}
	}

	return 0
}

func (b *DiscordBot) handleMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == s.State.User.ID {
		return
	}

	if !strings.HasPrefix(m.Content, b.feedbackCommand) {
		return
	}

	// Check rate limit
	if !b.rateLimiter.CanUse(m.Author.ID) {
		timeLeft := b.rateLimiter.TimeUntilNext(m.Author.ID)
		minutes := int(timeLeft.Minutes())
		seconds := int(timeLeft.Seconds()) % 60
		msg := fmt.Sprintf("Please wait %d minutes and %d seconds before using this command again.", minutes, seconds)
		s.ChannelMessageSend(m.ChannelID, msg)
		return
	}

	// Check if user has feedback role
	member, err := s.GuildMember(b.guildID, m.Author.ID)
	if err != nil {
		log.Printf("Failed to get guild member: %v", err)
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

	// Parse command
	parts := strings.SplitN(m.Content, " ", 3)
	if len(parts) < 3 {
		s.ChannelMessageSend(m.ChannelID, "Usage: !feedback network/ref_number Your feedback message")
		return
	}

	proposalRef := parts[1]
	feedbackMsg := parts[2]

	// Validate proposal reference
	refParts := strings.Split(proposalRef, "/")
	if len(refParts) != 2 {
		s.ChannelMessageSend(m.ChannelID, "Invalid format. Use: network/ref_number (e.g., polkadot/123)")
		return
	}

	network := strings.ToLower(refParts[0])
	refNum, err := strconv.ParseUint(refParts[1], 10, 64)
	if err != nil || refNum == 0 || refNum > 1000000 {
		s.ChannelMessageSend(m.ChannelID, "Invalid referendum number")
		return
	}

	// Validate feedback length
	if len(feedbackMsg) < 10 || len(feedbackMsg) > 5000 {
		s.ChannelMessageSend(m.ChannelID, "Feedback message must be between 10 and 5000 characters")
		return
	}

	// Get DAO member by Discord username
	var daoMember types.DaoMember
	if err := b.db.Where("discord = ?", m.Author.Username).First(&daoMember).Error; err != nil {
		s.ChannelMessageSend(m.ChannelID, "Your Discord account is not linked to a Polkadot address. Please contact an admin.")
		return
	}

	// Process the feedback
	if err := b.processFeedback(s, m, network, refNum, feedbackMsg, &daoMember); err != nil {
		log.Printf("Error processing feedback: %v", err)
		s.ChannelMessageSend(m.ChannelID, "Failed to process feedback. Please try again.")
		return
	}
}

func (b *DiscordBot) processFeedback(s *discordgo.Session, m *discordgo.MessageCreate, network string, refNum uint64, feedbackMsg string, daoMember *types.DaoMember) error {
	// Find network ID
	var netID uint8
	var networkInfo *types.Network

	b.mu.RLock()
	for _, net := range b.networks {
		if strings.ToLower(net.Name) == network || strings.ToLower(net.PolkassemblyPrefix) == network {
			netID = net.ID
			networkInfo = net
			break
		}
	}
	b.mu.RUnlock()

	if netID == 0 {
		return fmt.Errorf("unknown network: %s", network)
	}

	// Store feedback in database
	var ref types.Ref
	err := b.db.Transaction(func(tx *gorm.DB) error {
		// Check if referendum exists
		if err := tx.First(&ref, "network_id = ? AND ref_id = ?", netID, refNum).Error; err != nil {
			if err == gorm.ErrRecordNotFound && daoMember.IsAdmin {
				// Create if admin
				ref = types.Ref{
					NetworkID: netID,
					RefID:     refNum,
					Submitter: daoMember.Address,
					Status:    "Unknown",
					Title:     fmt.Sprintf("%s Referendum #%d", strings.Title(network), refNum),
				}
				if err := tx.Create(&ref).Error; err != nil {
					return err
				}
			} else {
				return err
			}
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

		return nil
	})

	if err != nil {
		return err
	}

	// Check if this is the first message
	var msgCount int64
	b.db.Model(&types.RefMessage{}).Where("ref_id = ?", ref.ID).Count(&msgCount)

	// Build response
	gcURL := data.GetSetting("gc_url")
	if gcURL == "" {
		gcURL = "http://localhost:3000"
	}

	link := fmt.Sprintf("%s/%s/%d", gcURL, network, refNum)

	// Create embed response
	embed := &discordgo.MessageEmbed{
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
			Text: fmt.Sprintf("Submitted by %s", m.Author.Username),
		},
		Timestamp: time.Now().Format(time.RFC3339),
	}

	// If first message and we have Polkassembly integration, post it
	polkassemblyAPIKey := data.GetSetting("polkassembly_api_key")
	if msgCount == 1 && polkassemblyAPIKey != "" && networkInfo != nil && networkInfo.PolkassemblyPrefix != "" {
		go b.postToPolkassembly(network, refNum, feedbackMsg, link, networkInfo.PolkassemblyPrefix)
	}

	// Publish to Redis for potential other consumers
	if b.rdb != nil {
		_ = data.PublishMessage(context.Background(), b.rdb, map[string]interface{}{
			"proposal": fmt.Sprintf("%s/%d", network, refNum),
			"author":   daoMember.Address,
			"body":     feedbackMsg,
			"time":     time.Now().Unix(),
			"id":       msgCount,
			"network":  network,
			"ref_id":   refNum,
		})
	}

	s.ChannelMessageSendEmbed(m.ChannelID, embed)

	log.Printf("Feedback submitted by %s (%s) for %s/%d: %d chars",
		m.Author.Username, daoMember.Address, network, refNum, len(feedbackMsg))

	return nil
}

func (b *DiscordBot) postToPolkassembly(network string, refNum uint64, message, link, polkassemblyPrefix string) {
	// This would integrate with Polkassembly API
	// Implementation depends on Polkassembly API documentation
	log.Printf("Would post to Polkassembly: %s/%d", network, refNum)
}

func (b *DiscordBot) monitorAPIMessages(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	lastCheck := time.Now()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Query for new messages since last check
			messages, err := b.fetchNewMessages(lastCheck)
			if err != nil {
				log.Printf("Error fetching new messages: %v", err)
				continue
			}

			for _, msg := range messages {
				if err := b.postMessageToDiscord(msg); err != nil {
					log.Printf("Error posting message to Discord: %v", err)
				}
			}

			lastCheck = time.Now()
		}
	}
}

func (b *DiscordBot) fetchNewMessages(since time.Time) ([]StreamMessage, error) {
	// Query database for new external messages
	var messages []types.RefMessage
	err := b.db.Where("created_at > ? AND internal = ?", since, false).
		Order("created_at ASC").
		Find(&messages).Error

	if err != nil {
		return nil, err
	}

	// Convert to stream messages
	var streamMessages []StreamMessage
	for _, msg := range messages {
		var ref types.Ref
		if err := b.db.First(&ref, msg.RefID).Error; err != nil {
			continue
		}

		network := b.getNetworkByID(ref.NetworkID)
		if network == nil {
			continue
		}

		streamMessages = append(streamMessages, StreamMessage{
			Proposal:  fmt.Sprintf("%s/%d", network.Name, ref.RefID),
			Author:    msg.Author,
			Body:      msg.Body,
			Time:      msg.CreatedAt.Unix(),
			ID:        msg.ID,
			Network:   strings.ToLower(network.Name),
			NetworkID: network.ID,
			RefID:     ref.RefID,
		})
	}

	return streamMessages, nil
}

func (b *DiscordBot) postMessageToDiscord(msg StreamMessage) error {
	b.mu.RLock()
	network := b.networks[msg.NetworkID]
	b.mu.RUnlock()

	if network == nil || network.DiscordChannelID == "" {
		return fmt.Errorf("no channel configured for network %d", msg.NetworkID)
	}

	// Find the referendum thread
	thread, err := b.findReferendumThread(network.DiscordChannelID, int(msg.RefID))
	if err != nil {
		return fmt.Errorf("find thread: %w", err)
	}

	// Format the message
	gcURL := data.GetSetting("gc_url")
	if gcURL == "" {
		gcURL = "http://localhost:3000"
	}

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

func (b *DiscordBot) findReferendumThread(channelID string, refNum int) (*discordgo.Channel, error) {
	// Get all threads in the channel
	threads, err := b.session.GuildThreadsActive(b.guildID)
	if err != nil {
		return nil, err
	}

	// Pattern to match thread names
	patterns := []string{
		fmt.Sprintf(`^#?%d\s*[:|-]`, refNum),
		fmt.Sprintf(`^#?%d\s+`, refNum),
		fmt.Sprintf(`\[%d\]`, refNum),
	}

	for _, thread := range threads.Threads {
		if thread.ParentID == channelID {
			for _, pattern := range patterns {
				if matched, _ := regexp.MatchString(pattern, thread.Name); matched {
					return thread, nil
				}
			}
		}
	}

	// Check archived threads
	publicThreads, err := b.session.ThreadsArchived(channelID, nil, 100)
	if err == nil {
		for _, thread := range publicThreads.Threads {
			for _, pattern := range patterns {
				if matched, _ := regexp.MatchString(pattern, thread.Name); matched {
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
	}

	return nil, fmt.Errorf("thread not found for referendum %d", refNum)
}

func (b *DiscordBot) getNetworkByID(id uint8) *types.Network {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.networks[id]
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
	// Connect to database first
	mysqlDSN := os.Getenv("MYSQL_DSN")
	if mysqlDSN == "" {
		mysqlDSN = "govcomms:DK3mfv93jf4m@tcp(127.0.0.1:3306)/govcomms"
	}

	db := data.MustMySQL(mysqlDSN)

	// Load settings from database
	if err := data.LoadSettings(db); err != nil {
		log.Printf("Failed to load settings: %v", err)
	}

	// Get configuration from database with env fallbacks
	token := data.GetSetting("discord_token")
	if token == "" {
		token = os.Getenv("DISCORD_TOKEN")
		if token == "" {
			log.Fatal("DISCORD_TOKEN not set in database or environment")
		}
	}

	feedbackRoleID := data.GetSetting("feedback_role_id")
	if feedbackRoleID == "" {
		feedbackRoleID = os.Getenv("FEEDBACK_ROLE_ID")
		if feedbackRoleID == "" {
			log.Fatal("FEEDBACK_ROLE_ID not set in database or environment")
		}
	}

	guildID := data.GetSetting("guild_id")
	if guildID == "" {
		guildID = os.Getenv("GUILD_ID")
		if guildID == "" {
			log.Fatal("GUILD_ID not set in database or environment")
		}
	}

	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://127.0.0.1:6379/0"
	}

	// Connect to Redis
	rdb := data.MustRedis(redisURL)

	// Create bot
	bot, err := NewDiscordBot(token, feedbackRoleID, guildID, db, rdb)
	if err != nil {
		log.Fatalf("Failed to create bot: %v", err)
	}

	if err := bot.Start(); err != nil {
		log.Fatalf("Failed to start bot: %v", err)
	}

	log.Println("Discord bot is running. Press CTRL-C to exit.")

	// Wait for interrupt
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc

	bot.Stop()
	log.Println("Discord bot stopped gracefully")
}
