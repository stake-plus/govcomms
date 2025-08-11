package bot

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/redis/go-redis/v9"
	"github.com/stake-plus/govcomms/src/bot/components/feedback"
	"github.com/stake-plus/govcomms/src/bot/components/polkassembly"
	"github.com/stake-plus/govcomms/src/bot/components/referendum"
	"github.com/stake-plus/govcomms/src/bot/config"
	"github.com/stake-plus/govcomms/src/bot/data"
	"gorm.io/gorm"
)

type Bot struct {
	config       *config.Config
	db           *gorm.DB
	redis        *redis.Client
	session      *discordgo.Session
	handlers     []interface{}
	cancelFunc   context.CancelFunc
	polkassembly *polkassembly.Service
}

func New(cfg *config.Config, db *gorm.DB, rdb *redis.Client) (*Bot, error) {
	// Create Discord session
	session, err := discordgo.New("Bot " + cfg.Token)
	if err != nil {
		return nil, fmt.Errorf("failed to create Discord session: %w", err)
	}

	// Set intents
	session.Identify.Intents = discordgo.IntentsGuilds |
		discordgo.IntentsGuildMessages |
		discordgo.IntentsGuildMessageReactions |
		discordgo.IntentsMessageContent |
		discordgo.IntentsDirectMessages

	// Initialize Polkassembly service
	polkassemblyLogger := log.New(os.Stdout, "[Polkassembly] ", log.LstdFlags)
	polkassemblyService, err := polkassembly.NewService(polkassemblyLogger, db)
	if err != nil {
		log.Printf("Failed to create Polkassembly service: %v", err)
	}

	bot := &Bot{
		config:       cfg,
		db:           db,
		redis:        rdb,
		session:      session,
		polkassembly: polkassemblyService,
	}

	// Initialize handlers
	bot.initHandlers()

	return bot, nil
}

func (b *Bot) initHandlers() {
	// Create feedback handler config
	feedbackConfig := feedback.Config{
		DB:                  b.db,
		FeedbackRoleID:      b.config.FeedbackRoleID,
		GuildID:             b.config.GuildID,
		PolkassemblyService: b.polkassembly,
	}

	feedbackHandler := feedback.NewHandler(feedbackConfig)

	// Create referendum handler
	referendumHandler := referendum.NewHandler(b.db, b.config)

	// Store handlers
	b.handlers = []interface{}{
		feedbackHandler,
		referendumHandler,
	}

	// Register Discord event handlers
	b.session.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		log.Printf("Logged in as: %v#%v", s.State.User.Username, s.State.User.Discriminator)
	})

	// Message create handler
	b.session.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
		// Ignore bot messages
		if m.Author.ID == s.State.User.ID {
			return
		}

		// Handle feedback command
		if feedbackHandler != nil {
			feedbackHandler.HandleMessage(s, m)
		}
	})

	// Thread create handler
	b.session.AddHandler(func(s *discordgo.Session, t *discordgo.ThreadCreate) {
		if referendumHandler != nil {
			referendumHandler.HandleThreadCreate(s, t)
		}
	})

	// Thread update handler
	b.session.AddHandler(func(s *discordgo.Session, t *discordgo.ThreadUpdate) {
		if referendumHandler != nil {
			referendumHandler.HandleThreadUpdate(s, t)
		}
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

	// Start referendum sync service
	go func() {
		log.Println("Starting referendum sync service")
		referendum.StartPeriodicSync(ctx, b.session, b.db, b.config, 10*time.Minute)
	}()

	// Start Polkassembly reply monitor if service is available
	if b.polkassembly != nil {
		go func() {
			log.Println("Starting Polkassembly reply monitor")
			polkassemblyLogger := log.New(os.Stdout, "[ReplyMonitor] ", log.LstdFlags)
			replyMonitor := polkassembly.NewReplyMonitor(b.db, b.polkassembly, b.session, polkassemblyLogger)
			replyMonitor.Start(ctx, 2*time.Minute)
		}()
	}

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
