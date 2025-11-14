package question

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/stake-plus/govcomms/src/actions/core"
	aicore "github.com/stake-plus/govcomms/src/ai/core"
	cache "github.com/stake-plus/govcomms/src/cache"
	sharedconfig "github.com/stake-plus/govcomms/src/config"
	shareddiscord "github.com/stake-plus/govcomms/src/discord"
	sharedgov "github.com/stake-plus/govcomms/src/polkadot-go/governance"
	"gorm.io/gorm"
)

const answerEmbedColor = 0x3B82F6

var _ core.Module = (*Module)(nil)

// Module owns the Discord session and logic for the Q&A action set.
type Module struct {
	cfg            *sharedconfig.QAConfig
	db             *gorm.DB
	session        *discordgo.Session
	cacheManager   *cache.Manager
	contextStore   *cache.ContextStore
	aiClient       aicore.Client
	networkManager *sharedgov.NetworkManager
	refManager     *sharedgov.ReferendumManager
	cancel         context.CancelFunc
}

// NewModule wires dependencies for the question/refresh/context actions.
func NewModule(cfg *sharedconfig.QAConfig, db *gorm.DB) (*Module, error) {
	if cfg == nil {
		return nil, fmt.Errorf("qa config is nil")
	}

	session, err := discordgo.New("Bot " + cfg.Base.Token)
	if err != nil {
		return nil, fmt.Errorf("question: discord session: %w", err)
	}
	session.Identify.Intents = discordgo.IntentsGuilds |
		discordgo.IntentsGuildMessages |
		discordgo.IntentsMessageContent

	networkManager, err := sharedgov.NewNetworkManager(db)
	if err != nil {
		return nil, fmt.Errorf("question: network manager: %w", err)
	}
	refManager := sharedgov.NewReferendumManager(db)

	if cfg.AIConfig.OpenAIKey == "" && cfg.AIConfig.ClaudeKey == "" {
		return nil, fmt.Errorf("question: no AI provider configured")
	}
	aiClient, err := aicore.NewClient(aicore.FactoryConfig{
		Provider:     cfg.AIConfig.AIProvider,
		OpenAIKey:    cfg.AIConfig.OpenAIKey,
		ClaudeKey:    cfg.AIConfig.ClaudeKey,
		SystemPrompt: cfg.AIConfig.AISystemPrompt,
		Model:        cfg.AIConfig.AIModel,
		Temperature:  0,
	})
	if err != nil {
		return nil, fmt.Errorf("question: AI client init: %w", err)
	}

	cacheManager, err := cache.NewManager(cfg.TempDir)
	if err != nil {
		return nil, fmt.Errorf("question: cache manager: %w", err)
	}

	return &Module{
		cfg:            cfg,
		db:             db,
		session:        session,
		cacheManager:   cacheManager,
		contextStore:   cache.NewContextStore(db),
		aiClient:       aiClient,
		networkManager: networkManager,
		refManager:     refManager,
	}, nil
}

// Name implements actions.Module.
func (m *Module) Name() string { return "question" }

// Start boots the Discord session and registers handlers.
func (m *Module) Start(ctx context.Context) error {
	if m.session == nil {
		return fmt.Errorf("question: session not initialized")
	}

	m.initHandlers()
	if err := m.session.Open(); err != nil {
		return fmt.Errorf("question: discord open: %w", err)
	}

	sessionCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel

	go func() {
		<-sessionCtx.Done()
		m.session.Close()
	}()

	return nil
}

// Stop closes the Discord session.
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
		log.Printf("question: logged in as %s#%s", s.State.User.Username, s.State.User.Discriminator)
		if err := shareddiscord.RegisterSlashCommands(s, m.cfg.Base.GuildID,
			shareddiscord.CommandQuestion,
			shareddiscord.CommandRefresh,
			shareddiscord.CommandContext,
		); err != nil {
			log.Printf("question: register commands failed: %v", err)
		}
	})

	m.session.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		switch i.ApplicationCommandData().Name {
		case "question":
			m.handleQuestionSlash(s, i)
		case "refresh":
			m.handleRefreshSlash(s, i)
		case "context":
			m.handleContextSlash(s, i)
		}
	})
}

func (m *Module) handleQuestionSlash(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if m.cfg.QARoleID != "" && !shareddiscord.HasRole(s, m.cfg.Base.GuildID, i.Member.User.ID, m.cfg.QARoleID) {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "You don't have permission to use this command.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	}); err != nil {
		log.Printf("question: slash ack failed: %v", err)
		return
	}

	data := i.ApplicationCommandData()
	var question string
	for _, opt := range data.Options {
		if opt.Name == "question" {
			question = opt.StringValue()
			break
		}
	}
	if len(question) < 5 {
		sendStyledWebhookEdit(s, i.Interaction, "Question", "Please provide a valid question (at least 5 characters).")
		return
	}

	threadInfo, err := m.refManager.FindThread(i.ChannelID)
	if err != nil || threadInfo == nil {
		sendStyledWebhookEdit(s, i.Interaction, "Question", "This command must be used in a referendum thread.")
		return
	}

	network := m.networkManager.GetByID(threadInfo.NetworkID)
	if network == nil {
		sendStyledWebhookEdit(s, i.Interaction, "Question", "Failed to identify network.")
		return
	}

	content, err := m.cacheManager.GetProposalContent(network.Name, uint32(threadInfo.RefID))
	if err != nil {
		log.Printf("question: proposal content: %v", err)
		sendStyledWebhookEdit(s, i.Interaction, "Question", "Failed to retrieve proposal content. Please try /refresh first.")
		return
	}

	qaContext, err := m.contextStore.BuildContext(threadInfo.NetworkID, uint32(threadInfo.RefID))
	if err != nil {
		log.Printf("question: build context: %v", err)
		qaContext = ""
	}

	fullContent := content + qaContext
	ctx := context.Background()
	opts := aicore.Options{
		Model:        m.cfg.AIModel,
		SystemPrompt: m.cfg.AISystemPrompt,
	}

	input := "Context:\n" + fullContent + "\n\nQuestion:\n" + question + "\n\nUse web search as needed to ensure the answer reflects the latest information."
	answer, err := m.aiClient.Respond(ctx, input, []aicore.Tool{{Type: "web_search"}}, opts)
	if err != nil {
		log.Printf("question: web search failed, fallback: %v", err)
		answer, err = m.aiClient.AnswerQuestion(ctx, fullContent, question, opts)
	}
	if err != nil {
		log.Printf("question: AI failure: %v", err)
		sendStyledWebhookEdit(s, i.Interaction, "Question", "Failed to generate answer. Please try again.")
		return
	}

	if err := m.contextStore.SaveQA(threadInfo.NetworkID, uint32(threadInfo.RefID), i.ChannelID, i.Member.User.ID, question, answer); err != nil {
		log.Printf("question: save QA history: %v", err)
	}

	m.sendLongMessageSlash(s, i.Interaction, question, answer)
}

func (m *Module) handleContextSlash(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	}); err != nil {
		log.Printf("question: context ack failed: %v", err)
		return
	}

	if m.cfg.QARoleID != "" && !shareddiscord.HasRole(s, m.cfg.Base.GuildID, i.Member.User.ID, m.cfg.QARoleID) {
		msg := "You don't have permission to use this command."
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &msg})
		return
	}

	threadInfo, err := m.refManager.FindThread(i.ChannelID)
	if err != nil || threadInfo == nil {
		msg := "This command must be used in a referendum thread."
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &msg})
		return
	}

	qas, err := m.contextStore.GetRecentQAs(threadInfo.NetworkID, uint32(threadInfo.RefID), 10)
	if err != nil {
		msg := "Failed to retrieve context."
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &msg})
		return
	}
	if len(qas) == 0 {
		msg := "No previous Q&A found for this referendum."
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &msg})
		return
	}

	var response strings.Builder
	response.WriteString("**Recent Q&A History:**\n")
	for _, qa := range qas {
		response.WriteString(fmt.Sprintf("\n**Q:** %s\n**A:** ", qa.Question))
		if len(qa.Answer) > 200 {
			response.WriteString(qa.Answer[:200] + "...")
		} else {
			response.WriteString(qa.Answer)
		}
		response.WriteString("\n")
		if response.Len() > 1800 {
			response.WriteString("\n*[Truncated]*")
			break
		}
	}
	content := response.String()
	s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &content})
}

func (m *Module) handleRefreshSlash(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if m.cfg.QARoleID != "" && !shareddiscord.HasRole(s, m.cfg.Base.GuildID, i.Member.User.ID, m.cfg.QARoleID) {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "You don't have permission to use this command.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	}); err != nil {
		log.Printf("question: refresh ack failed: %v", err)
		return
	}

	threadInfo, err := m.refManager.FindThread(i.ChannelID)
	if err != nil || threadInfo == nil {
		msg := "This command must be used in a referendum thread."
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &msg})
		return
	}

	network := m.networkManager.GetByID(threadInfo.NetworkID)
	if network == nil {
		msg := "Failed to identify network."
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &msg})
		return
	}

	if _, err := m.cacheManager.Refresh(network.Name, uint32(threadInfo.RefID)); err != nil {
		log.Printf("question: refresh failed: %v", err)
		msg := "Failed to refresh proposal content."
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &msg})
		return
	}

	msg := fmt.Sprintf("✅ Refreshed content for %s referendum #%d", network.Name, threadInfo.RefID)
	s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &msg})
}

func (m *Module) sendLongMessageSlash(s *discordgo.Session, interaction *discordgo.Interaction, question string, message string) {
	userID := ""
	if interaction.Member != nil && interaction.Member.User != nil {
		userID = interaction.Member.User.ID
	} else if interaction.User != nil {
		userID = interaction.User.ID
	}

	title := "Answer"
	if questionTitle := strings.TrimSpace(question); questionTitle != "" {
		title = fmt.Sprintf("Answer • %s", questionTitle)
	}

	chunks := shareddiscord.BuildStyledMessages(title, message, userID)
	if len(chunks) == 0 {
		return
	}

	first := chunks[0]
	if _, err := s.InteractionResponseEdit(interaction, &discordgo.WebhookEdit{
		Content: &first,
	}); err != nil {
		log.Printf("question: response send failed: %v", err)
		return
	}

	for idx := 1; idx < len(chunks); idx++ {
		if _, err := s.ChannelMessageSend(interaction.ChannelID, chunks[idx]); err != nil {
			log.Printf("question: follow-up send failed: %v", err)
			return
		}
	}
}

func sendStyledWebhookEdit(s *discordgo.Session, interaction *discordgo.Interaction, title, body string) {
	formatted := shareddiscord.FormatStyledBlock(title, body)
	s.InteractionResponseEdit(interaction, &discordgo.WebhookEdit{Content: &formatted})
}
