package reports

import (
	"context"
	"fmt"
	"log"

	"github.com/bwmarrin/discordgo"
	"github.com/stake-plus/govcomms/src/actions/core"
	sharedconfig "github.com/stake-plus/govcomms/src/config"
	sharedgov "github.com/stake-plus/govcomms/src/polkadot-go/governance"
	"gorm.io/gorm"
)

var _ core.Module = (*Module)(nil)

// Module handles PDF report generation for referendums
type Module struct {
	cfg            *sharedconfig.ReportsConfig
	db             *gorm.DB
	session        *discordgo.Session
	networkManager *sharedgov.NetworkManager
	refManager     *sharedgov.ReferendumManager
	cancel         context.CancelFunc
	handler        *Handler
}

// GenerateReport triggers PDF report generation for a referendum
func (m *Module) GenerateReport(s *discordgo.Session, channelID string, network string, refID uint32, refDBID uint64) {
	if m.handler == nil {
		log.Printf("reports: handler not initialized")
		return
	}
	m.handler.GenerateReport(s, channelID, network, refID, refDBID)
}

// NewModule creates a new reports module
func NewModule(cfg *sharedconfig.ReportsConfig, db *gorm.DB) (*Module, error) {
	if cfg == nil {
		return nil, fmt.Errorf("reports: config is nil")
	}

	session, err := discordgo.New("Bot " + cfg.Base.Token)
	if err != nil {
		return nil, fmt.Errorf("reports: discord session: %w", err)
	}

	session.Identify.Intents = discordgo.IntentsGuilds |
		discordgo.IntentsGuildMessages |
		discordgo.IntentsGuildMessageReactions |
		discordgo.IntentsMessageContent

	networkManager, err := sharedgov.NewNetworkManager(db)
	if err != nil {
		return nil, fmt.Errorf("reports: network manager: %w", err)
	}
	refManager := sharedgov.NewReferendumManager(db)

	handler := NewHandler(cfg, db, networkManager, refManager)

	return &Module{
		cfg:            cfg,
		db:             db,
		session:        session,
		networkManager: networkManager,
		refManager:     refManager,
		handler:        handler,
	}, nil
}

// Name implements actions.Module
func (m *Module) Name() string { return "reports" }

// Start opens the Discord session and registers handlers
func (m *Module) Start(ctx context.Context) error {
	if err := m.session.Open(); err != nil {
		return fmt.Errorf("reports: discord open: %w", err)
	}

	m.initHandlers()

	sessionCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel

	go func() {
		<-sessionCtx.Done()
		m.session.Close()
	}()

	return nil
}

// Stop shuts down the Discord session
func (m *Module) Stop(ctx context.Context) {
	if m.cancel != nil {
		m.cancel()
	}
	if m.session != nil {
		m.session.Close()
	}
}

func (m *Module) initHandlers() {
	m.session.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		username := formatDiscordUsername(s.State.User.Username, s.State.User.Discriminator)
		log.Printf("reports: logged in as %s", username)
	})
}

// formatDiscordUsername formats a Discord username, handling the deprecated discriminator field
func formatDiscordUsername(username, discriminator string) string {
	if discriminator == "" || discriminator == "0" {
		return username
	}
	return fmt.Sprintf("%s#%s", username, discriminator)
}

