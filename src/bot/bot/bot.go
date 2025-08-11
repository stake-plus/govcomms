package bot

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/redis/go-redis/v9"
	"github.com/stake-plus/govcomms/src/bot/components/discord"
	"github.com/stake-plus/govcomms/src/bot/components/feedback"
	"github.com/stake-plus/govcomms/src/bot/components/network"
	"github.com/stake-plus/govcomms/src/bot/components/polkassembly"
	"github.com/stake-plus/govcomms/src/bot/components/referendum"
	"github.com/stake-plus/govcomms/src/bot/config"
	"github.com/stake-plus/govcomms/src/bot/data"
	"gorm.io/gorm"
)

type Bot struct {
	session             *discordgo.Session
	db                  *gorm.DB
	rdb                 *redis.Client
	config              config.Config
	networks            *network.Manager
	feedbackHandler     *feedback.Handler
	refManager          *referendum.Manager
	messageMonitor      *discord.MessageMonitor
	polkassemblyService *polkassembly.Service
	ctx                 context.Context
	cancel              context.CancelFunc
	wg                  sync.WaitGroup
}

func New(cfg config.Config, db *gorm.DB, rdb *redis.Client) (*Bot, error) {
	dg, err := discordgo.New("Bot " + cfg.Token)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())

	bot := &Bot{
		session: dg,
		db:      db,
		rdb:     rdb,
		config:  cfg,
		ctx:     ctx,
		cancel:  cancel,
	}

	if err := bot.initializeComponents(); err != nil {
		return nil, err
	}

	bot.registerHandlers()

	dg.Identify.Intents = discordgo.IntentsGuildMessages |
		discordgo.IntentsMessageContent |
		discordgo.IntentsGuilds

	return bot, nil
}

func (b *Bot) initializeComponents() error {
	netMgr, err := network.NewManager(b.db)
	if err != nil {
		return fmt.Errorf("create network manager: %w", err)
	}
	b.networks = netMgr

	b.refManager = referendum.NewManager(b.db, b.networks)

	log.Println("Initializing Polkassembly service...")
	polkassemblyService, err := polkassembly.NewService(log.Default(), b.db)
	if err != nil {
		log.Printf("WARNING: Failed to initialize Polkassembly service: %v", err)
		polkassemblyService = nil
	} else {
		log.Println("Polkassembly service initialized successfully")
	}
	b.polkassemblyService = polkassemblyService

	b.feedbackHandler = feedback.NewHandler(feedback.Config{
		DB:                  b.db,
		NetworkManager:      b.networks,
		RefManager:          b.refManager,
		FeedbackRoleID:      b.config.FeedbackRoleID,
		GuildID:             b.config.GuildID,
		PolkassemblyService: polkassemblyService,
	})

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

func (b *Bot) handleThreadUpdate(s *discordgo.Session, t *discordgo.ThreadUpdate) {
	b.refManager.HandleThreadUpdate(t)
}

func (b *Bot) handleReady(s *discordgo.Session, event *discordgo.Ready) {
	log.Printf("Discord bot logged in as %s", event.User.Username)

	// Initial thread sync
	if err := b.refManager.SyncThreads(s, b.config.GuildID); err != nil {
		log.Printf("Failed to sync threads: %v", err)
	}

	// Start monitoring for new messages from Polkassembly
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		b.messageMonitor.Start(b.ctx)
	}()

	// Start Polkassembly reply monitor
	if b.polkassemblyService != nil {
		b.wg.Add(1)
		go func() {
			defer b.wg.Done()
			b.polkassemblyService.StartReplyMonitor(b.ctx, 5*time.Minute)
		}()
	}

	// Start indexer service
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		interval := time.Duration(b.config.IndexerIntervalMinutes) * time.Minute
		data.IndexerService(b.ctx, b.db, interval, b.config.IndexerWorkers)
	}()

	// Start periodic thread sync (every 5 minutes)
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-b.ctx.Done():
				log.Println("Stopping thread sync")
				return
			case <-ticker.C:
				log.Println("Running periodic thread sync")
				if err := b.refManager.SyncThreads(s, b.config.GuildID); err != nil {
					log.Printf("Failed to sync threads: %v", err)
				}
			}
		}
	}()
}
