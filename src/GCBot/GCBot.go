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
	networks        map[uint8]*types.Network
	threadMapping   map[string]*ThreadInfo // channelID -> ThreadInfo
	apiClient       *APIClient
	rateLimiter     *UserRateLimiter
	mu              sync.RWMutex
}

type ThreadInfo struct {
	ThreadID  string
	NetworkID uint8
	RefID     uint64
	RefDBID   uint64 // Database ID of the referendum
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
		apiURL = "http://localhost:443"
	}

	bot := &DiscordBot{
		session:         dg,
		db:              db,
		feedbackRoleID:  feedbackRoleID,
		feedbackCommand: "!feedback",
		guildID:         guildID,
		networks:        make(map[uint8]*types.Network),
		threadMapping:   make(map[string]*ThreadInfo),
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

	// Synchronize threads on startup
	if err := b.syncAllThreads(); err != nil {
		log.Printf("Failed to sync threads: %v", err)
	}

	// Start monitoring for new messages from the API
	go b.monitorAPIMessages(context.Background())
}

func (b *DiscordBot) syncAllThreads() error {
	log.Println("Starting thread synchronization...")

	// Get all active threads
	threads, err := b.session.GuildThreadsActive(b.guildID)
	if err != nil {
		return fmt.Errorf("get active threads: %w", err)
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	synced := 0
	for _, thread := range threads.Threads {
		// Check if thread belongs to a referendum channel
		for _, network := range b.networks {
			if thread.ParentID == network.DiscordChannelID {
				refID := b.extractRefIDFromThreadName(thread.Name)
				if refID > 0 {
					// Create or get referendum in database
					var ref types.Ref
					err := b.db.FirstOrCreate(&ref, types.Ref{
						NetworkID: network.ID,
						RefID:     refID,
					}).Error

					if err == nil {
						// If newly created, set default values
						if ref.Title == "" {
							ref.Title = thread.Name
							ref.Status = "Unknown"
							ref.Submitter = "Unknown"
							b.db.Save(&ref)
						}

						// Store thread mapping
						b.threadMapping[thread.ID] = &ThreadInfo{
							ThreadID:  thread.ID,
							NetworkID: network.ID,
							RefID:     refID,
							RefDBID:   ref.ID,
						}
						synced++
						log.Printf("Synced thread: %s -> %s Ref #%d", thread.Name, network.Name, refID)
					}
				}
			}
		}
	}

	// Also check archived threads
	for _, network := range b.networks {
		if network.DiscordChannelID == "" {
			continue
		}

		publicThreads, err := b.session.ThreadsArchived(network.DiscordChannelID, nil, 100)
		if err == nil {
			for _, thread := range publicThreads.Threads {
				refID := b.extractRefIDFromThreadName(thread.Name)
				if refID > 0 {
					var ref types.Ref
					err := b.db.FirstOrCreate(&ref, types.Ref{
						NetworkID: network.ID,
						RefID:     refID,
					}).Error

					if err == nil {
						if ref.Title == "" {
							ref.Title = thread.Name
							ref.Status = "Unknown"
							ref.Submitter = "Unknown"
							b.db.Save(&ref)
						}

						b.threadMapping[thread.ID] = &ThreadInfo{
							ThreadID:  thread.ID,
							NetworkID: network.ID,
							RefID:     refID,
							RefDBID:   ref.ID,
						}
						synced++
						log.Printf("Synced archived thread: %s -> %s Ref #%d", thread.Name, network.Name, refID)
					}
				}
			}
		}
	}

	log.Printf("Thread synchronization complete. Synced %d threads", synced)
	return nil
}

func (b *DiscordBot) handleThreadUpdate(s *discordgo.Session, t *discordgo.ThreadUpdate) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Check if this is a new thread in a referendum channel
	for _, network := range b.networks {
		if t.ParentID == network.DiscordChannelID {
			refID := b.extractRefIDFromThreadName(t.Name)
			if refID > 0 {
				// Create or get referendum in database
				var ref types.Ref
				err := b.db.FirstOrCreate(&ref, types.Ref{
					NetworkID: network.ID,
					RefID:     refID,
				}).Error

				if err == nil {
					if ref.Title == "" {
						ref.Title = t.Name
						ref.Status = "Unknown"
						ref.Submitter = "Unknown"
						b.db.Save(&ref)
					}

					// Update thread mapping
					b.threadMapping[t.ID] = &ThreadInfo{
						ThreadID:  t.ID,
						NetworkID: network.ID,
						RefID:     refID,
						RefDBID:   ref.ID,
					}
					log.Printf("Detected new/updated thread: %s -> %s Ref #%d", t.Name, network.Name, refID)
				}
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

	log.Printf("Feedback command received from %s in channel %s", m.Author.Username, m.ChannelID)

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
		s.ChannelMessageSend(m.ChannelID, "Failed to verify permissions. Please try again.")
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
		log.Printf("User %s lacks feedback role", m.Author.Username)
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

	// Validate feedback length
	if len(feedbackMsg) < 10 || len(feedbackMsg) > 5000 {
		s.ChannelMessageSend(m.ChannelID, "Feedback message must be between 10 and 5000 characters")
		return
	}

	// Check if we're in a thread
	b.mu.RLock()
	threadInfo, exists := b.threadMapping[m.ChannelID]
	b.mu.RUnlock()

	if !exists {
		log.Printf("Channel %s is not a recognized referendum thread", m.ChannelID)
		s.ChannelMessageSend(m.ChannelID, "This command must be used in a referendum thread.")
		return
	}

	// Get DAO member by Discord username
	var daoMember types.DaoMember
	if err := b.db.Where("discord = ?", m.Author.Username).First(&daoMember).Error; err != nil {
		log.Printf("Discord user %s not found in dao_members", m.Author.Username)
		s.ChannelMessageSend(m.ChannelID, "Your Discord account is not linked to a Polkadot address. Please contact an admin.")
		return
	}

	// Get network info
	network := b.getNetworkByID(threadInfo.NetworkID)
	if network == nil {
		log.Printf("Network not found for ID %d", threadInfo.NetworkID)
		s.ChannelMessageSend(m.ChannelID, "Failed to process feedback. Please try again.")
		return
	}

	// Process the feedback using the thread info
	if err := b.processFeedbackFromThread(s, m, threadInfo, network, feedbackMsg, &daoMember); err != nil {
		log.Printf("Error processing feedback: %v", err)
		s.ChannelMessageSend(m.ChannelID, "Failed to process feedback. Please try again.")
		return
	}
}

func (b *DiscordBot) processFeedbackFromThread(s *discordgo.Session, m *discordgo.MessageCreate, threadInfo *ThreadInfo, network *types.Network, feedbackMsg string, daoMember *types.DaoMember) error {
	log.Printf("Processing feedback for %s ref #%d from %s", network.Name, threadInfo.RefID, daoMember.Address)

	var msgID uint64
	err := b.db.Transaction(func(tx *gorm.DB) error {
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
	b.db.Model(&types.RefMessage{}).Where("ref_id = ?", threadInfo.RefDBID).Count(&msgCount)

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
		go b.postToPolkassembly(strings.ToLower(network.Name), threadInfo.RefID, feedbackMsg, link, network.PolkassemblyPrefix)
	}

	// Publish to Redis
	if b.rdb != nil {
		_ = data.PublishMessage(context.Background(), b.rdb, map[string]interface{}{
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

// Keep the old processFeedback function for backward compatibility but it shouldn't be used
func (b *DiscordBot) processFeedback(s *discordgo.Session, m *discordgo.MessageCreate, network string, refNum uint64, feedbackMsg string, daoMember *types.DaoMember) error {
	log.Printf("Warning: Using deprecated processFeedback function")
	return fmt.Errorf("please use this command in a referendum thread")
}

func (b *DiscordBot) postToPolkassembly(network string, refNum uint64, message, link, polkassemblyPrefix string) {
	log.Printf("Would post to Polkassembly: %s/%d", network, refNum)
	// TODO: Implement actual Polkassembly API integration
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

			if len(messages) > 0 {
				lastCheck = time.Now()
			}
		}
	}
}

func (b *DiscordBot) fetchNewMessages(since time.Time) ([]StreamMessage, error) {
	var messages []types.RefMessage
	err := b.db.Where("created_at > ? AND internal = ?", since, false).
		Order("created_at ASC").
		Find(&messages).Error

	if err != nil {
		return nil, err
	}

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

	// Find thread by referendum ID
	thread, err := b.findReferendumThread(network.DiscordChannelID, int(msg.RefID))
	if err != nil {
		return fmt.Errorf("find thread: %w", err)
	}

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
	if err == nil {
		log.Printf("Posted message from %s to thread for %s ref #%d", msg.Author, msg.Network, msg.RefID)
	}
	return err
}

func (b *DiscordBot) findReferendumThread(channelID string, refNum int) (*discordgo.Channel, error) {
	// First check our mapping
	b.mu.RLock()
	for threadID, info := range b.threadMapping {
		if info.RefID == uint64(refNum) {
			network := b.networks[info.NetworkID]
			if network != nil && network.DiscordChannelID == channelID {
				b.mu.RUnlock()
				// Get thread details
				thread, err := b.session.Channel(threadID)
				if err == nil {
					return thread, nil
				}
			}
		}
	}
	b.mu.RUnlock()

	// Fallback to searching
	threads, err := b.session.GuildThreadsActive(b.guildID)
	if err != nil {
		return nil, err
	}

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
