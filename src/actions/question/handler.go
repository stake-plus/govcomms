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
	"github.com/stake-plus/govcomms/src/mcp"
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
	mcpEnabled     bool
	mcpBaseURL     string
	mcpAuthToken   string
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

	if cfg.AIConfig.OpenAIKey == "" &&
		cfg.AIConfig.ClaudeKey == "" &&
		cfg.AIConfig.GeminiKey == "" &&
		cfg.AIConfig.DeepSeekKey == "" &&
		cfg.AIConfig.GrokKey == "" {
		return nil, fmt.Errorf("question: no AI provider configured")
	}
	factoryCfg := cfg.AIConfig.FactoryConfig()
	factoryCfg.Temperature = 0
	aiClient, err := aicore.NewClient(factoryCfg)
	if err != nil {
		return nil, fmt.Errorf("question: AI client init: %w", err)
	}

	cacheManager, err := cache.NewManager(cfg.TempDir)
	if err != nil {
		return nil, fmt.Errorf("question: cache manager: %w", err)
	}

	mcpCfg := sharedconfig.LoadMCPConfig(db)

	return &Module{
		cfg:            cfg,
		db:             db,
		session:        session,
		cacheManager:   cacheManager,
		contextStore:   cache.NewContextStore(db),
		aiClient:       aiClient,
		networkManager: networkManager,
		refManager:     refManager,
		mcpEnabled:     mcpCfg.Enabled,
		mcpBaseURL:     mcpCfg.Listen,
		mcpAuthToken:   mcpCfg.AuthToken,
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
		formatted := shareddiscord.FormatStyledBlock("Question", "You don't have permission to use this command.")
		shareddiscord.InteractionRespondNoEmbed(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: formatted,
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	if err := shareddiscord.InteractionRespondNoEmbed(s, i.Interaction, &discordgo.InteractionResponse{
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

	var contextBuilder strings.Builder
	contextBuilder.WriteString(fmt.Sprintf(
		"You are assisting with %s referendum #%d.\n- Network: %s\n- Referendum ID: %d\n",
		network.Name, threadInfo.RefID, network.Name, threadInfo.RefID))
	if m.mcpEnabled {
		contextBuilder.WriteString("You do NOT have the proposal body in your context. You MUST call the tool `fetch_referendum_data` before answering. Example calls:\n")
		contextBuilder.WriteString("1. metadata: {\"network\":\"" + strings.ToLower(network.Name) + "\",\"refId\":" + fmt.Sprint(threadInfo.RefID) + ",\"resource\":\"metadata\"}\n")
		contextBuilder.WriteString("2. content: {\"network\":\"" + strings.ToLower(network.Name) + "\",\"refId\":" + fmt.Sprint(threadInfo.RefID) + ",\"resource\":\"content\"}\n")
		contextBuilder.WriteString("Only after reviewing metadata/content should you answer. Fetch attachments if needed.\n")
	} else {
		contextBuilder.WriteString("Full proposal text:\n")
		contextBuilder.WriteString(content)
	}
	if strings.TrimSpace(qaContext) != "" {
		contextBuilder.WriteString("\nRecent Q&A history:\n")
		contextBuilder.WriteString(qaContext)
	}

	input := contextBuilder.String() + "\n\nQuestion:\n" + question

	tools := []aicore.Tool{{Type: "web_search"}}
	if mcptool := m.buildMCPTool(strings.ToLower(network.Name), uint32(threadInfo.RefID)); mcptool != nil {
		tools = append(tools, *mcptool)
	}

	answer, err := m.aiClient.Respond(ctx, input, tools, opts)
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

func (m *Module) buildMCPTool(network string, refID uint32) *aicore.Tool {
	if !m.mcpEnabled {
		return nil
	}
	tool := mcp.NewReferendaTool(m.mcpBaseURL, m.mcpAuthToken)
	if tool == nil {
		return nil
	}
	if tool.Defaults == nil {
		tool.Defaults = map[string]any{}
	}
	tool.Defaults["network"] = network
	tool.Defaults["refId"] = refID
	if _, ok := tool.Defaults["resource"]; !ok {
		tool.Defaults["resource"] = "metadata"
	}
	return tool
}

func (m *Module) handleContextSlash(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if err := shareddiscord.InteractionRespondNoEmbed(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	}); err != nil {
		log.Printf("question: context ack failed: %v", err)
		return
	}

	if m.cfg.QARoleID != "" && !shareddiscord.HasRole(s, m.cfg.Base.GuildID, i.Member.User.ID, m.cfg.QARoleID) {
		sendStyledWebhookEdit(s, i.Interaction, "Context", "You don't have permission to use this command.")
		return
	}

	threadInfo, err := m.refManager.FindThread(i.ChannelID)
	if err != nil || threadInfo == nil {
		sendStyledWebhookEdit(s, i.Interaction, "Context", "This command must be used in a referendum thread.")
		return
	}

	qas, err := m.contextStore.GetRecentQAs(threadInfo.NetworkID, uint32(threadInfo.RefID), 10)
	if err != nil {
		sendStyledWebhookEdit(s, i.Interaction, "Context", "Failed to retrieve context.")
		return
	}
	if len(qas) == 0 {
		sendStyledWebhookEdit(s, i.Interaction, "Context", "No previous Q&A found for this referendum.")
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
	sendStyledWebhookEdit(s, i.Interaction, "Context", content)
}

func (m *Module) handleRefreshSlash(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if m.cfg.QARoleID != "" && !shareddiscord.HasRole(s, m.cfg.Base.GuildID, i.Member.User.ID, m.cfg.QARoleID) {
		formatted := shareddiscord.FormatStyledBlock("Refresh", "You don't have permission to use this command.")
		shareddiscord.InteractionRespondNoEmbed(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: formatted,
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	if err := shareddiscord.InteractionRespondNoEmbed(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	}); err != nil {
		log.Printf("question: refresh ack failed: %v", err)
		return
	}

	threadInfo, err := m.refManager.FindThread(i.ChannelID)
	if err != nil || threadInfo == nil {
		sendStyledWebhookEdit(s, i.Interaction, "Refresh", "This command must be used in a referendum thread.")
		return
	}

	network := m.networkManager.GetByID(threadInfo.NetworkID)
	if network == nil {
		sendStyledWebhookEdit(s, i.Interaction, "Refresh", "Failed to identify network.")
		return
	}

	if _, err := m.cacheManager.Refresh(network.Name, uint32(threadInfo.RefID)); err != nil {
		log.Printf("question: refresh failed: %v", err)
		sendStyledWebhookEdit(s, i.Interaction, "Refresh", "Failed to refresh proposal content.")
		return
	}

	msg := fmt.Sprintf("✅ Refreshed content for %s referendum #%d", network.Name, threadInfo.RefID)
	sendStyledWebhookEdit(s, i.Interaction, "Refresh", msg)
}

func (m *Module) sendLongMessageSlash(s *discordgo.Session, interaction *discordgo.Interaction, question string, message string) {
	userID := ""
	if interaction.Member != nil && interaction.Member.User != nil {
		userID = interaction.Member.User.ID
	} else if interaction.User != nil {
		userID = interaction.User.ID
	}

	title := "Question"
	if questionTitle := strings.TrimSpace(question); questionTitle != "" {
		title = fmt.Sprintf("Question: %s", questionTitle)
	}

	answerCleaned, refs := shareddiscord.ReplaceURLsAndCollect(message)
	if strings.TrimSpace(answerCleaned) == "" {
		answerCleaned = "_No content_"
	}
	answerBody := fmt.Sprintf("Answer:\n\n%s", strings.TrimSpace(answerCleaned))

	payloads := shareddiscord.BuildStyledMessages(title, answerBody, userID)
	if len(payloads) == 0 {
		return
	}

	first := payloads[0]
	edit := &discordgo.WebhookEdit{
		Content: &first.Content,
	}
	if len(refs) > 0 {
		components := shareddiscord.BuildLinkButtons(refs)
		edit.Components = &components
	}
	if _, err := shareddiscord.InteractionResponseEditNoEmbed(s, interaction, edit); err != nil {
		log.Printf("question: response send failed: %v", err)
		return
	}

	for idx := 1; idx < len(payloads); idx++ {
		payload := payloads[idx]
		msg := &discordgo.MessageSend{
			Content: payload.Content,
		}
		if len(refs) > 0 {
			msg.Components = shareddiscord.BuildLinkButtons(refs)
		}
		if _, err := shareddiscord.SendComplexMessageNoEmbed(s, interaction.ChannelID, msg); err != nil {
			log.Printf("question: follow-up send failed: %v", err)
			return
		}
	}
}

func sendStyledWebhookEdit(s *discordgo.Session, interaction *discordgo.Interaction, title, body string) {
	cleaned, refs := shareddiscord.ReplaceURLsAndCollect(body)
	payload := shareddiscord.BuildStyledMessage(title, cleaned)
	edit := &discordgo.WebhookEdit{
		Content: &payload.Content,
	}
	if len(refs) > 0 {
		components := shareddiscord.BuildLinkButtons(refs)
		edit.Components = &components
	}
	shareddiscord.InteractionResponseEditNoEmbed(s, interaction, edit)
}
