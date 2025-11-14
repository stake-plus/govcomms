package research

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/stake-plus/govcomms/src/actions/core"
	"github.com/stake-plus/govcomms/src/actions/research/components/claims"
	"github.com/stake-plus/govcomms/src/actions/research/components/teams"
	"github.com/stake-plus/govcomms/src/actions/team"
	cache "github.com/stake-plus/govcomms/src/cache"
	sharedconfig "github.com/stake-plus/govcomms/src/config"
	shareddiscord "github.com/stake-plus/govcomms/src/discord"
	sharedgov "github.com/stake-plus/govcomms/src/polkadot-go/governance"
	"gorm.io/gorm"
)

var _ core.Module = (*Module)(nil)

// Module wires the research and team actions into a Discord session.
type Module struct {
	cfg            *sharedconfig.ResearchConfig
	db             *gorm.DB
	session        *discordgo.Session
	cacheManager   *cache.Manager
	networkManager *sharedgov.NetworkManager
	refManager     *sharedgov.ReferendumManager
	claimsAnalyzer *claims.Analyzer
	teamsAnalyzer  *teams.Analyzer
	cancel         context.CancelFunc

	researchHandler *Handler
	teamHandler     *team.Handler
}

// NewModule constructs a research module with all dependencies.
func NewModule(cfg *sharedconfig.ResearchConfig, db *gorm.DB) (*Module, error) {
	if cfg == nil {
		return nil, fmt.Errorf("research: config is nil")
	}

	session, err := discordgo.New("Bot " + cfg.Base.Token)
	if err != nil {
		return nil, fmt.Errorf("research: discord session: %w", err)
	}

	session.Identify.Intents = discordgo.IntentsGuilds |
		discordgo.IntentsGuildMessages |
		discordgo.IntentsGuildMessageReactions |
		discordgo.IntentsMessageContent

	networkManager, err := sharedgov.NewNetworkManager(db)
	if err != nil {
		return nil, fmt.Errorf("research: network manager: %w", err)
	}
	refManager := sharedgov.NewReferendumManager(db)

	cacheManager, err := cache.NewManager(cfg.TempDir)
	if err != nil {
		return nil, fmt.Errorf("research: cache manager: %w", err)
	}

	claimsAnalyzer, err := claims.NewAnalyzer(cfg.OpenAIKey)
	if err != nil {
		return nil, fmt.Errorf("research: claims analyzer: %w", err)
	}
	teamsAnalyzer, err := teams.NewAnalyzer(cfg.OpenAIKey)
	if err != nil {
		return nil, fmt.Errorf("research: teams analyzer: %w", err)
	}

	researchHandler := &Handler{
		Config:         cfg,
		Cache:          cacheManager,
		NetworkManager: networkManager,
		RefManager:     refManager,
		ClaimsAnalyzer: claimsAnalyzer,
	}

	teamHandler := &team.Handler{
		Config:         cfg,
		Cache:          cacheManager,
		NetworkManager: networkManager,
		RefManager:     refManager,
		TeamsAnalyzer:  teamsAnalyzer,
	}

	return &Module{
		cfg:             cfg,
		db:              db,
		session:         session,
		cacheManager:    cacheManager,
		networkManager:  networkManager,
		refManager:      refManager,
		claimsAnalyzer:  claimsAnalyzer,
		teamsAnalyzer:   teamsAnalyzer,
		researchHandler: researchHandler,
		teamHandler:     teamHandler,
	}, nil
}

// Name implements actions.Module.
func (m *Module) Name() string { return "research" }

// Start opens the Discord session and registers handlers.
func (m *Module) Start(ctx context.Context) error {
	if err := m.session.Open(); err != nil {
		return fmt.Errorf("research: discord open: %w", err)
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

// Stop shuts down the Discord session.
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
		log.Printf("research: logged in as %s#%s", s.State.User.Username, s.State.User.Discriminator)
		if err := shareddiscord.RegisterSlashCommands(s, m.cfg.Base.GuildID,
			shareddiscord.CommandResearch,
			shareddiscord.CommandTeam,
		); err != nil {
			log.Printf("research: register commands failed: %v", err)
		}
	})

	m.session.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		switch i.ApplicationCommandData().Name {
		case "research":
			m.researchHandler.HandleSlash(s, i)
		case "team":
			m.teamHandler.HandleSlash(s, i)
		}
	})

	// legacy message commands
	m.session.AddHandler(func(s *discordgo.Session, mCreate *discordgo.MessageCreate) {
		if mCreate.Author == nil || mCreate.Author.Bot {
			return
		}
		content := mCreate.Content
		if strings.HasPrefix(content, "!research") {
			m.researchHandler.HandleMessage(s, mCreate)
		} else if strings.HasPrefix(content, "!team") {
			m.teamHandler.HandleMessage(s, mCreate)
		}
	})
}

