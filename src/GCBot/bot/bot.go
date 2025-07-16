package bot

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/bwmarrin/discordgo"
	"github.com/redis/go-redis/v9"
	"github.com/stake-plus/govcomms/src/GCApi/data"
	"github.com/stake-plus/govcomms/src/GCBot/api"
	"github.com/stake-plus/govcomms/src/GCBot/components/discord"
	"github.com/stake-plus/govcomms/src/GCBot/components/feedback"
	"github.com/stake-plus/govcomms/src/GCBot/components/network"
	"github.com/stake-plus/govcomms/src/GCBot/components/polkassembly"
	"github.com/stake-plus/govcomms/src/GCBot/components/referendum"
	"gorm.io/gorm"
)

type Config struct {
	Token          string
	FeedbackRoleID string
	GuildID        string
	DB             *gorm.DB
	Redis          *redis.Client
}

type Bot struct {
	session             *discordgo.Session
	db                  *gorm.DB
	rdb                 *redis.Client
	config              Config
	networks            *network.Manager
	feedbackHandler     *feedback.Handler
	refManager          *referendum.Manager
	apiClient           *api.Client
	messageMonitor      *discord.MessageMonitor
	polkassemblyService *polkassembly.Service
	ctx                 context.Context
	cancel              context.CancelFunc
	wg                  sync.WaitGroup
}

func New(config Config) (*Bot, error) {
	// Load settings
	if err := data.LoadSettings(config.DB); err != nil {
		return nil, fmt.Errorf("load settings: %w", err)
	}

	// Create Discord session
	dg, err := discordgo.New("Bot " + config.Token)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())

	bot := &Bot{
		session: dg,
		db:      config.DB,
		rdb:     config.Redis,
		config:  config,
		ctx:     ctx,
		cancel:  cancel,
	}

	// Initialize components
	if err := bot.initializeComponents(); err != nil {
		return nil, err
	}

	// Register handlers
	bot.registerHandlers()

	// Set intents
	dg.Identify.Intents = discordgo.IntentsGuildMessages |
		discordgo.IntentsMessageContent |
		discordgo.IntentsGuilds

	return bot, nil
}

func (b *Bot) initializeComponents() error {
	// Initialize network manager
	netMgr, err := network.NewManager(b.db)
	if err != nil {
		return fmt.Errorf("create network manager: %w", err)
	}
	b.networks = netMgr

	// Initialize API client
	apiURL := data.GetSetting("gcapi_url")
	if apiURL == "" {
		apiURL = "http://localhost:443"
	}
	b.apiClient = api.NewClient(apiURL)

	// Initialize referendum manager
	b.refManager = referendum.NewManager(b.db, b.networks)

	// Initialize Polkassembly service with database
	log.Println("Initializing Polkassembly service...")
	polkassemblyService, err := polkassembly.NewService(log.Default(), b.db)
	if err != nil {
		log.Printf("WARNING: Failed to initialize Polkassembly service: %v", err)
		log.Printf("Polkassembly posting will be disabled")
		// Don't fail initialization, just disable Polkassembly posting
		polkassemblyService = nil
	} else {
		log.Println("Polkassembly service initialized successfully")
	}
	b.polkassemblyService = polkassemblyService

	// Initialize feedback handler with Polkassembly service
	b.feedbackHandler = feedback.NewHandler(feedback.Config{
		DB:                  b.db,
		Redis:               b.rdb,
		NetworkManager:      b.networks,
		RefManager:          b.refManager,
		APIClient:           b.apiClient,
		FeedbackRoleID:      b.config.FeedbackRoleID,
		GuildID:             b.config.GuildID,
		PolkassemblyService: polkassemblyService,
	})

	// Initialize message monitor
	b.messageMonitor = discord.NewMessageMonitor(discord.MonitorConfig{
		DB:             b.db,
		NetworkManager: b.networks,
		RefManager:     b.refManager,
		Session:        b.session,
		GuildID:        b.config.GuildID,
	})

	return nil
}

func (b *Bot) registerHandlers() {
	b.session.AddHandler(b.handleReady)
	b.session.AddHandler(b.feedbackHandler.HandleMessage)
	b.session.AddHandler(b.handleThreadUpdate)
}

func (b *Bot) Start() error {
	return b.session.Open()
}

func (b *Bot) Stop() {
	b.cancel()
	b.wg.Wait()
	b.session.Close()
}

func (b *Bot) handleReady(s *discordgo.Session, event *discordgo.Ready) {
	log.Printf("Discord bot logged in as %s", event.User.Username)

	// Synchronize threads on startup
	if err := b.refManager.SyncThreads(s, b.config.GuildID); err != nil {
		log.Printf("Failed to sync threads: %v", err)
	}

	// Start monitoring for new messages from the API
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		b.messageMonitor.Start(b.ctx)
	}()
}

func (b *Bot) handleThreadUpdate(s *discordgo.Session, t *discordgo.ThreadUpdate) {
	b.refManager.HandleThreadUpdate(t)
}
