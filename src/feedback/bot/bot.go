package bot

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/redis/go-redis/v9"
	"github.com/stake-plus/govcomms/src/feedback/data"
	sharedconfig "github.com/stake-plus/govcomms/src/shared/config"
	sharedgov "github.com/stake-plus/govcomms/src/shared/gov"
	"gorm.io/gorm"
)

type Bot struct {
	config         *sharedconfig.FeedbackConfig
	db             *gorm.DB
	redis          *redis.Client
	session        *discordgo.Session
	networkManager *sharedgov.NetworkManager
	refManager     *sharedgov.ReferendumManager
	cancelFunc     context.CancelFunc
}

func New(cfg *sharedconfig.FeedbackConfig, db *gorm.DB, rdb *redis.Client) (*Bot, error) {
	// Create Discord session
	session, err := discordgo.New("Bot " + cfg.Base.Token)
	if err != nil {
		return nil, fmt.Errorf("failed to create Discord session: %w", err)
	}

	// Set intents
	session.Identify.Intents = discordgo.IntentsGuilds |
		discordgo.IntentsGuildMessages |
		discordgo.IntentsGuildMessageReactions |
		discordgo.IntentsMessageContent |
		discordgo.IntentsDirectMessages

	// Create network manager
	networkManager, err := sharedgov.NewNetworkManager(db)
	if err != nil {
		log.Printf("Failed to create network manager: %v", err)
		networkManager = nil
	}

	// Create referendum manager
	refManager := sharedgov.NewReferendumManager(db)

	bot := &Bot{
		config:         cfg,
		db:             db,
		redis:          rdb,
		session:        session,
		networkManager: networkManager,
		refManager:     refManager,
	}

	// Initialize handlers
	bot.initHandlers()

	return bot, nil
}

func (b *Bot) initHandlers() {
	// Register Discord event handlers
	b.session.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		log.Printf("Logged in as: %v#%v", s.State.User.Username, s.State.User.Discriminator)
	})

	// Message create handler
	b.session.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
		if m.Author.ID == s.State.User.ID {
			return
		}
		// TODO: Implement feedback command handling inline
	})

	// Thread create/update handlers
	b.session.AddHandler(func(s *discordgo.Session, t *discordgo.ThreadCreate) {
		// TODO: Implement thread mapping inline
	})

	b.session.AddHandler(func(s *discordgo.Session, t *discordgo.ThreadUpdate) {
		// TODO: Implement thread mapping inline
	})
}

func (b *Bot) Start() error {
	// Open Discord connection
	if err := b.session.Open(); err != nil {
		return fmt.Errorf("failed to open Discord connection: %w", err)
	}

	// Create context for services
	ctx, cancel := context.WithCancel(context.Background())
	b.cancelFunc = cancel

	// Start indexer service
	go func() {
		log.Println("Starting indexer service")
		data.IndexerService(ctx, b.db, 5*time.Minute, 4)
	}()

	// TODO: Implement referendum sync and polkassembly monitoring inline if needed

	return nil
}

func (b *Bot) Stop() {
	// Cancel context to stop services
	if b.cancelFunc != nil {
		b.cancelFunc()
	}

	// Close Discord session
	if b.session != nil {
		b.session.Close()
	}

	// Close database connection
	if b.db != nil {
		sqlDB, err := b.db.DB()
		if err == nil {
			sqlDB.Close()
		}
	}

	// Close Redis connection
	if b.redis != nil {
		b.redis.Close()
	}
}
