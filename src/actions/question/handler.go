package question

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/stake-plus/govcomms/src/actions/core"
	"github.com/stake-plus/govcomms/src/actions/research/components/claims"
	"github.com/stake-plus/govcomms/src/actions/research/components/teams"
	aicore "github.com/stake-plus/govcomms/src/ai/core"
	cache "github.com/stake-plus/govcomms/src/cache"
	sharedconfig "github.com/stake-plus/govcomms/src/config"
	shareddiscord "github.com/stake-plus/govcomms/src/discord"
	"github.com/stake-plus/govcomms/src/mcp"
	sharedgov "github.com/stake-plus/govcomms/src/polkadot-go/governance"
	"gorm.io/gorm"
)

const answerEmbedColor = 0x3B82F6
const defaultInteractionTimeout = 2 * time.Minute

var _ core.Module = (*Module)(nil)

// Module owns the Discord session and logic for the Q&A action set.
type Module struct {
	cfg             *sharedconfig.QAConfig
	db              *gorm.DB
	session         *discordgo.Session
	cacheManager    *cache.Manager
	contextStore    *cache.ContextStore
	networkManager  *sharedgov.NetworkManager
	refManager      *sharedgov.ReferendumManager
	cancel          context.CancelFunc
	mcpEnabled      bool
	mcpBaseURL      string
	mcpAuthToken    string
	responseTimeout time.Duration
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
	cacheManager, err := cache.NewManager(cfg.TempDir)
	if err != nil {
		return nil, fmt.Errorf("question: cache manager: %w", err)
	}

	mcpCfg := sharedconfig.LoadMCPConfig(db)

	return &Module{
		cfg:             cfg,
		db:              db,
		session:         session,
		cacheManager:    cacheManager,
		contextStore:    cache.NewContextStore(db),
		networkManager:  networkManager,
		refManager:      refManager,
		mcpEnabled:      mcpCfg.Enabled,
		mcpBaseURL:      mcpCfg.Listen,
		mcpAuthToken:    mcpCfg.AuthToken,
		responseTimeout: defaultInteractionTimeout,
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
			shareddiscord.CommandSummary,
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
		case "summary":
			m.handleSummarySlash(s, i)
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

	var qaContext string
	if !m.mcpEnabled {
		qaContext, err = m.contextStore.BuildContext(threadInfo.NetworkID, uint32(threadInfo.RefID))
		if err != nil {
			log.Printf("question: build context: %v", err)
			qaContext = ""
		}
	}

	aiClient, aiCfg, err := m.createAIClient()
	if err != nil {
		log.Printf("question: ai client: %v", err)
		sendStyledWebhookEdit(s, i.Interaction, "Question", "AI provider is not configured correctly. Please try again later.")
		return
	}

	fullContent := content
	if strings.TrimSpace(qaContext) != "" {
		fullContent += qaContext
	}

	timeout := m.responseTimeout
	if timeout <= 0 {
		timeout = defaultInteractionTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	basePrompt := strings.TrimSpace(aiCfg.AISystemPrompt)
	respondOpts := aicore.Options{
		Model:        aiCfg.AIModel,
		SystemPrompt: m.buildRespondSystemPrompt(basePrompt, network.Name, threadInfo.RefID, content, qaContext),
	}
	providerInfo, _ := aicore.GetProviderInfo(aiCfg.AIProvider)
	modelDisplay := formatModelName(aiCfg.AIProvider, respondOpts.Model)

	input := strings.TrimSpace(question)
	if input == "" {
		input = question
	}

	tools := []aicore.Tool{{Type: "web_search"}}
	if mcptool := m.buildMCPTool(strings.ToLower(network.Name), uint32(threadInfo.RefID)); mcptool != nil {
		tools = append(tools, *mcptool)
	}

	answer, err := aiClient.Respond(ctx, input, tools, respondOpts)
	if err != nil {
		log.Printf("question: web search failed, fallback: %v", err)
		fallbackOpts := respondOpts
		fallbackOpts.SystemPrompt = basePrompt
		if m.mcpEnabled {
			fullContent = content
		}
		answer, err = aiClient.AnswerQuestion(ctx, fullContent, question, fallbackOpts)
	}
	if err != nil {
		log.Printf("question: AI failure: %v", err)
		sendStyledWebhookEdit(s, i.Interaction, "Question", "Failed to generate answer. Please try again.")
		return
	}

	if err := m.contextStore.SaveQA(threadInfo.NetworkID, uint32(threadInfo.RefID), i.ChannelID, i.Member.User.ID, question, answer); err != nil {
		log.Printf("question: save QA history: %v", err)
	}

	m.sendLongMessageSlash(s, i.Interaction, question, answer, providerInfo, modelDisplay)
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

func (m *Module) buildRespondSystemPrompt(basePrompt, networkName string, refID uint64, content, qaContext string) string {
	var builder strings.Builder
	if trimmed := strings.TrimSpace(basePrompt); trimmed != "" {
		builder.WriteString(trimmed)
		builder.WriteString("\n\n")
	}

	builder.WriteString(fmt.Sprintf(
		"You are assisting with %s referendum #%d.\n- Network: %s\n- Referendum ID: %d\n",
		networkName, refID, networkName, refID))

	if m.mcpEnabled {
		slug := strings.ToLower(strings.TrimSpace(networkName))
		builder.WriteString("Use the `fetch_referendum_data` tool to retrieve metadata and full proposal content before answering.\n")
		builder.WriteString(fmt.Sprintf("Metadata example: {\"network\":\"%s\",\"refId\":%d,\"resource\":\"metadata\"}\n", slug, refID))
		builder.WriteString(fmt.Sprintf("Content example: {\"network\":\"%s\",\"refId\":%d,\"resource\":\"content\"}\n", slug, refID))
		builder.WriteString("Request attachments when metadata lists files, and call the tool with {\"resource\":\"history\"} to review previous moderator Q&A exchanges when helpful. Avoid repeating tool calls after you have the information you need and then deliver the final answer.\n")
	} else {
		builder.WriteString("Full proposal text:\n")
		builder.WriteString(content)
		if strings.TrimSpace(qaContext) != "" {
			builder.WriteString("\nRecent Q&A history:\n")
			builder.WriteString(qaContext)
		}
	}

	return builder.String()
}

func (m *Module) createAIClient() (aicore.Client, sharedconfig.AIConfig, error) {
	latest := sharedconfig.LoadQAConfig(m.db)
	factoryCfg := latest.AIConfig.FactoryConfig()
	factoryCfg.Temperature = 0
	client, err := aicore.NewClient(factoryCfg)
	if err != nil {
		return nil, sharedconfig.AIConfig{}, err
	}
	return client, latest.AIConfig, nil
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

	// Update message to show we're processing research
	sendStyledWebhookEdit(s, i.Interaction, "Refresh", fmt.Sprintf("✅ Refreshed content for %s referendum #%d\n\n⏳ Processing claims and team analysis...", network.Name, threadInfo.RefID))

	// Run claims and teams analysis (wait for completion)
	if err := m.runSilentResearch(network.Name, uint32(threadInfo.RefID), threadInfo.RefDBID, threadInfo.NetworkID); err != nil {
		log.Printf("question: research failed: %v", err)
		sendStyledWebhookEdit(s, i.Interaction, "Refresh", fmt.Sprintf("✅ Refreshed content for %s referendum #%d\n\n⚠️ Research processing failed.", network.Name, threadInfo.RefID))
		return
	}

	// Update message to show we're generating summary
	sendStyledWebhookEdit(s, i.Interaction, "Refresh", fmt.Sprintf("✅ Refreshed content for %s referendum #%d\n\n✅ Research completed\n\n⏳ Generating summary...", network.Name, threadInfo.RefID))

	// Generate and save summary
	summary, err := m.generateSummary(network.Name, uint32(threadInfo.RefID), threadInfo.RefDBID, threadInfo.NetworkID)
	if err != nil {
		log.Printf("question: summary generation failed: %v", err)
		sendStyledWebhookEdit(s, i.Interaction, "Refresh", fmt.Sprintf("✅ Refreshed content for %s referendum #%d\n\n✅ Research completed\n\n⚠️ Summary generation failed.", network.Name, threadInfo.RefID))
		return
	}

	// Save summary to cache
	if err := m.cacheManager.UpdateSummary(network.Name, uint32(threadInfo.RefID), summary); err != nil {
		log.Printf("question: failed to save summary: %v", err)
	}

	// Get channel name for title
	channel, err := s.Channel(i.ChannelID)
	channelName := ""
	if err == nil && channel != nil {
		channelName = channel.Name
	}
	if channelName == "" {
		channelName = summary.Title // Fallback
	}

	// Format and send summary
	summaryMessages := m.formatSummary(summary, channelName)
	log.Printf("question: formatted summary for %s #%d (%d messages)", network.Name, threadInfo.RefID, len(summaryMessages))

	if len(summaryMessages) == 0 {
		log.Printf("question: no messages generated for summary")
		sendStyledWebhookEdit(s, i.Interaction, "Refresh", "Summary generated but formatting failed.")
		return
	}

	// Send first message as webhook edit (no title prefix)
	firstPayload := shareddiscord.BuildStyledMessage("", summaryMessages[0])
	edit := &discordgo.WebhookEdit{
		Content: &firstPayload.Content,
	}
	if len(firstPayload.Components) > 0 {
		edit.Components = &firstPayload.Components
	}
	if _, err := shareddiscord.InteractionResponseEditNoEmbed(s, i.Interaction, edit); err != nil {
		log.Printf("question: summary send failed: %v", err)
		return
	}

	// Send follow-up messages
	for idx := 1; idx < len(summaryMessages); idx++ {
		payload := shareddiscord.BuildStyledMessage("", summaryMessages[idx])
		msg := &discordgo.MessageSend{
			Content: payload.Content,
		}
		if len(payload.Components) > 0 {
			msg.Components = payload.Components
		}
		if _, err := shareddiscord.SendComplexMessageNoEmbed(s, i.ChannelID, msg); err != nil {
			log.Printf("question: summary follow-up send failed: %v", err)
			return
		}
	}

	log.Printf("question: summary successfully sent for %s #%d", network.Name, threadInfo.RefID)
}

func (m *Module) sendLongMessageSlash(s *discordgo.Session, interaction *discordgo.Interaction, question string, message string, providerInfo aicore.ProviderInfo, model string) {
	userID := ""
	if interaction.Member != nil && interaction.Member.User != nil {
		userID = interaction.Member.User.ID
	} else if interaction.User != nil {
		userID = interaction.User.ID
	}

	answerCleaned, refs := shareddiscord.ReplaceURLsAndCollect(message)
	if strings.TrimSpace(answerCleaned) == "" {
		answerCleaned = "_No content_"
	}
	answerBody := buildQuestionResponseBody(providerInfo, model, question, strings.TrimSpace(answerCleaned))

	payloads := shareddiscord.BuildStyledMessages("", answerBody, userID)
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

func formatModelName(provider, configuredModel string) string {
	resolved := strings.TrimSpace(aicore.ResolveModelName(provider, configuredModel))
	if resolved == "" {
		return "unknown"
	}
	return resolved
}

func buildQuestionResponseBody(providerInfo aicore.ProviderInfo, model, question, answer string) string {
	questionText := strings.TrimSpace(question)
	if questionText == "" {
		questionText = "N/A"
	}
	if strings.TrimSpace(answer) == "" {
		answer = "_No content_"
	}
	providerCompany := providerInfo.Company
	if providerCompany == "" {
		providerCompany = "unknown"
	}
	providerWebsite := providerInfo.Website
	if providerWebsite == "" {
		providerWebsite = "unknown"
	}
	return fmt.Sprintf("Provider: %s    Model: %s    Website: %s\n\nQuestion: %s\n\nAnswer:\n\n%s",
		providerCompany, model, providerWebsite, questionText, answer)
}

// runSilentResearch runs claims and teams analysis silently and saves results to cache metadata.
// Returns an error if research fails completely, nil if successful or partially successful.
func (m *Module) runSilentResearch(network string, refID uint32, refDBID uint64, networkID uint8) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	// Load AI config to get provider/model info
	aiCfg := sharedconfig.LoadQAConfig(m.db).AIConfig
	providerInfo, _ := aicore.GetProviderInfo(aiCfg.AIProvider)
	modelName := aicore.ResolveModelName(aiCfg.AIProvider, aiCfg.AIModel)
	if modelName == "" {
		modelName = providerInfo.Model
	}
	if modelName == "" {
		modelName = "unknown"
	}
	providerCompany := providerInfo.Company
	if providerCompany == "" {
		providerCompany = "unknown"
	}

	// Get proposal content
	proposalContent, err := m.cacheManager.GetProposalContent(network, refID)
	if err != nil {
		return fmt.Errorf("get proposal content: %w", err)
	}

	// Create AI clients for claims and teams
	factoryCfg := aiCfg.FactoryConfig()
	claimsClient, err := aicore.NewClient(factoryCfg)
	if err != nil {
		return fmt.Errorf("create claims client: %w", err)
	}
	claimsAnalyzer, err := claims.NewAnalyzer(claimsClient)
	if err != nil {
		return fmt.Errorf("create claims analyzer: %w", err)
	}

	teamsClient, err := aicore.NewClient(factoryCfg)
	if err != nil {
		return fmt.Errorf("create teams client: %w", err)
	}
	teamsAnalyzer, err := teams.NewAnalyzer(teamsClient)
	if err != nil {
		return fmt.Errorf("create teams analyzer: %w", err)
	}

	// Run claims and teams analysis in parallel
	var claimsData *cache.ClaimsData
	var teamsData *cache.TeamsData
	var claimsErr, teamsErr error
	done := make(chan bool, 2)

	// Claims analysis
	go func() {
		defer func() { done <- true }()
		topClaims, totalClaims, err := claimsAnalyzer.ExtractTopClaims(ctx, proposalContent)
		if err != nil {
			claimsErr = fmt.Errorf("extract claims: %w", err)
			return
		}
		if len(topClaims) == 0 {
			log.Printf("question: silent research: no claims found for %s #%d", network, refID)
			return
		}

		results, err := claimsAnalyzer.VerifyClaims(ctx, topClaims)
		if err != nil {
			claimsErr = fmt.Errorf("verify claims: %w", err)
			return
		}

		claimResults := make([]cache.ClaimResult, len(results))
		for i, result := range results {
			claim := topClaims[i]
			claimResults[i] = cache.ClaimResult{
				Claim:      claim.Claim,
				Category:   claim.Category,
				URLs:       claim.URLs,
				Context:    claim.Context,
				Status:     string(result.Status),
				Evidence:   result.Evidence,
				SourceURLs: result.SourceURLs,
			}
		}

		claimsData = &cache.ClaimsData{
			ProviderCompany: providerCompany,
			AIModel:         modelName,
			ProcessedAt:     time.Now().UTC(),
			TotalClaims:     totalClaims,
			Results:         claimResults,
		}
		log.Printf("question: silent research: processed %d claims for %s #%d", len(results), network, refID)
	}()

	// Teams analysis
	go func() {
		defer func() { done <- true }()
		members, err := teamsAnalyzer.ExtractTeamMembers(ctx, proposalContent)
		if err != nil {
			teamsErr = fmt.Errorf("extract team members: %w", err)
			return
		}
		if len(members) == 0 {
			log.Printf("question: silent research: no team members found for %s #%d", network, refID)
			return
		}

		results, err := teamsAnalyzer.AnalyzeTeamMembers(ctx, members)
		if err != nil {
			teamsErr = fmt.Errorf("analyze team members: %w", err)
			return
		}

		memberData := make([]cache.TeamMemberData, len(results))
		for i, result := range results {
			member := members[i]
			memberData[i] = cache.TeamMemberData{
				Name:            result.Name,
				Role:            result.Role,
				IsReal:          &result.IsReal,
				HasStatedSkills: &result.HasStatedSkills,
				Capability:      result.Capability,
				GitHub:          member.GitHub,
				Twitter:         member.Twitter,
				LinkedIn:        member.LinkedIn,
				Other:           member.Other,
				VerifiedURLs:    result.VerifiedURLs,
			}
		}

		teamsData = &cache.TeamsData{
			ProviderCompany: providerCompany,
			AIModel:         modelName,
			ProcessedAt:     time.Now().UTC(),
			Members:         memberData,
		}
		log.Printf("question: silent research: processed %d team members for %s #%d", len(results), network, refID)
	}()

	// Wait for both to complete
	<-done
	<-done

	if claimsErr != nil {
		log.Printf("question: silent research: claims error: %v", claimsErr)
	}
	if teamsErr != nil {
		log.Printf("question: silent research: teams error: %v", teamsErr)
	}

	// Save to cache metadata
	if claimsData != nil || teamsData != nil {
		if err := m.cacheManager.UpdateResearchData(network, refID, claimsData, teamsData); err != nil {
			return fmt.Errorf("update cache metadata: %w", err)
		}
		log.Printf("question: silent research: saved research data to cache for %s #%d", network, refID)
	}

	// If both failed, return error
	if claimsErr != nil && teamsErr != nil {
		return fmt.Errorf("both research tasks failed: claims=%v, teams=%v", claimsErr, teamsErr)
	}

	return nil
}

// generateSummary creates a summary of the referendum using AI and research data.
func (m *Module) generateSummary(network string, refID uint32, refDBID uint64, networkID uint8) (*cache.SummaryData, error) {
	// Get cache entry to access research data
	entry, err := m.cacheManager.EnsureEntry(network, refID)
	if err != nil {
		return nil, fmt.Errorf("get cache entry: %w", err)
	}

	// Get referendum title from database
	var ref sharedgov.Ref
	if err := m.db.Where("id = ?", refDBID).First(&ref).Error; err != nil {
		log.Printf("question: failed to get referendum title: %v", err)
	}
	title := "Unknown"
	if ref.Title != nil && *ref.Title != "" {
		title = *ref.Title
	}

	// Get proposal content
	proposalContent, err := m.cacheManager.GetProposalContent(network, refID)
	if err != nil {
		return nil, fmt.Errorf("get proposal content: %w", err)
	}

	// Build context for summary generation
	var summaryContext strings.Builder
	summaryContext.WriteString(fmt.Sprintf("Network: %s\nReferendum #%d\nTitle: %s\n\n", network, refID, title))
	summaryContext.WriteString("Proposal Content:\n")
	summaryContext.WriteString(proposalContent)
	summaryContext.WriteString("\n\n")

	// Add claims data with full details
	if entry.Claims != nil && len(entry.Claims.Results) > 0 {
		summaryContext.WriteString("Verified Claims (with URLs and evidence):\n")
		for _, claim := range entry.Claims.Results {
			summaryContext.WriteString(fmt.Sprintf("- [%s] %s\n", claim.Status, claim.Claim))
			if claim.Category != "" {
				summaryContext.WriteString(fmt.Sprintf("  Category: %s\n", claim.Category))
			}
			if claim.Context != "" {
				summaryContext.WriteString(fmt.Sprintf("  Context: %s\n", claim.Context))
			}
			if len(claim.URLs) > 0 {
				summaryContext.WriteString(fmt.Sprintf("  URLs from proposal: %s\n", strings.Join(claim.URLs, ", ")))
			}
			if claim.Evidence != "" {
				summaryContext.WriteString(fmt.Sprintf("  Verification Evidence: %s\n", claim.Evidence))
			}
			if len(claim.SourceURLs) > 0 {
				summaryContext.WriteString(fmt.Sprintf("  Verification Sources: %s\n", strings.Join(claim.SourceURLs, ", ")))
			}
			summaryContext.WriteString("\n")
		}
	}

	// Add team data with full details including URLs
	if entry.TeamMembers != nil && len(entry.TeamMembers.Members) > 0 {
		summaryContext.WriteString("Team Members (with all profile URLs and analysis):\n")
		for _, member := range entry.TeamMembers.Members {
			isReal := "Unknown"
			hasSkills := "Unknown"
			if member.IsReal != nil {
				if *member.IsReal {
					isReal = "Yes"
				} else {
					isReal = "No"
				}
			}
			if member.HasStatedSkills != nil {
				if *member.HasStatedSkills {
					hasSkills = "Yes"
				} else {
					hasSkills = "No"
				}
			}
			summaryContext.WriteString(fmt.Sprintf("- %s (%s) - Is Real: %s, Has Skills: %s\n", member.Name, member.Role, isReal, hasSkills))
			if member.Capability != "" {
				summaryContext.WriteString(fmt.Sprintf("  Capability Assessment: %s\n", member.Capability))
			}
			if len(member.GitHub) > 0 {
				summaryContext.WriteString(fmt.Sprintf("  GitHub: %s\n", strings.Join(member.GitHub, ", ")))
			}
			if len(member.Twitter) > 0 {
				summaryContext.WriteString(fmt.Sprintf("  Twitter: %s\n", strings.Join(member.Twitter, ", ")))
			}
			if len(member.LinkedIn) > 0 {
				summaryContext.WriteString(fmt.Sprintf("  LinkedIn: %s\n", strings.Join(member.LinkedIn, ", ")))
			}
			if len(member.Other) > 0 {
				summaryContext.WriteString(fmt.Sprintf("  Other URLs: %s\n", strings.Join(member.Other, ", ")))
			}
			if len(member.VerifiedURLs) > 0 {
				summaryContext.WriteString(fmt.Sprintf("  Verified URLs: %s\n", strings.Join(member.VerifiedURLs, ", ")))
			}
			summaryContext.WriteString("\n")
		}
	}

	// Load AI config
	aiCfg := sharedconfig.LoadQAConfig(m.db).AIConfig
	factoryCfg := aiCfg.FactoryConfig()
	aiClient, err := aicore.NewClient(factoryCfg)
	if err != nil {
		return nil, fmt.Errorf("create AI client: %w", err)
	}

	log.Printf("question: generating summary for %s #%d", network, refID)

	// Generate summary using AI
	prompt := fmt.Sprintf(`Generate a comprehensive summary for this blockchain governance proposal.

Requirements:
1. Background Context: Write 1 paragraph (maximum 4 sentences) explaining the background and context of this proposal.
2. Summary: Write 1 paragraph (maximum 4 sentences) summarizing what this proposal aims to achieve.

For each team member listed, provide a 2-sentence description of their history and background. Use the URLs, capability assessments, and verification information provided to write accurate descriptions.

Format your response as JSON:
{
  "backgroundContext": "1 paragraph, max 4 sentences",
  "summary": "1 paragraph, max 4 sentences",
  "teamHistories": {
    "Member Name": "2 sentences about their history"
  }
}

Proposal Data:
%s`, summaryContext.String())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	log.Printf("question: calling AI to generate summary for %s #%d", network, refID)
	response, err := aiClient.Respond(ctx, prompt, nil, aicore.Options{})
	if err != nil {
		log.Printf("question: AI summary generation failed for %s #%d: %v", network, refID, err)
		return nil, fmt.Errorf("AI response: %w", err)
	}
	log.Printf("question: received AI summary response for %s #%d (length: %d)", network, refID, len(response))

	// Parse AI response (extract JSON)
	var aiResponse struct {
		BackgroundContext string            `json:"backgroundContext"`
		Summary           string            `json:"summary"`
		TeamHistories     map[string]string `json:"teamHistories"`
	}

	// Try to extract JSON from response
	startIdx := strings.Index(response, "{")
	endIdx := strings.LastIndex(response, "}")
	if startIdx >= 0 && endIdx > startIdx {
		jsonStr := response[startIdx : endIdx+1]
		if err := json.Unmarshal([]byte(jsonStr), &aiResponse); err != nil {
			log.Printf("question: failed to parse AI summary response for %s #%d: %v\nResponse: %s", network, refID, err, response[:min(500, len(response))])
			// Fallback: use simple text extraction
			aiResponse.BackgroundContext = "Background context generation failed."
			aiResponse.Summary = "Summary generation failed."
			aiResponse.TeamHistories = make(map[string]string)
		} else {
			log.Printf("question: successfully parsed AI summary response for %s #%d", network, refID)
		}
	} else {
		log.Printf("question: no JSON found in AI summary response for %s #%d\nResponse: %s", network, refID, response[:min(500, len(response))])
		aiResponse.BackgroundContext = "Background context generation failed."
		aiResponse.Summary = "Summary generation failed."
		aiResponse.TeamHistories = make(map[string]string)
	}

	// Organize claims by status
	var validClaims, unverifiedClaims, invalidClaims []string
	if entry.Claims != nil {
		for _, claim := range entry.Claims.Results {
			switch strings.ToLower(claim.Status) {
			case "valid":
				validClaims = append(validClaims, claim.Claim)
			case "rejected":
				invalidClaims = append(invalidClaims, claim.Claim)
			default: // Unknown, etc.
				unverifiedClaims = append(unverifiedClaims, claim.Claim)
			}
		}
	}

	// Build team summaries
	var teamSummaries []cache.TeamSummary
	if entry.TeamMembers != nil {
		for _, member := range entry.TeamMembers.Members {
			isReal := false
			hasSkills := false
			if member.IsReal != nil {
				isReal = *member.IsReal
			}
			if member.HasStatedSkills != nil {
				hasSkills = *member.HasStatedSkills
			}

			history := aiResponse.TeamHistories[member.Name]
			if history == "" {
				history = "History information not available."
			}

			teamSummaries = append(teamSummaries, cache.TeamSummary{
				Name:            member.Name,
				Role:            member.Role,
				IsReal:          isReal,
				HasStatedSkills: hasSkills,
				History:         history,
			})
		}
	}

	summaryData := &cache.SummaryData{
		Network:           network,
		RefID:             refID,
		Title:             title,
		BackgroundContext: aiResponse.BackgroundContext,
		Summary:           aiResponse.Summary,
		ValidClaims:       validClaims,
		UnverifiedClaims:  unverifiedClaims,
		InvalidClaims:     invalidClaims,
		TeamMembers:       teamSummaries,
		GeneratedAt:       time.Now().UTC(),
	}

	log.Printf("question: summary generated for %s #%d: %d valid claims, %d unverified, %d invalid, %d team members",
		network, refID, len(validClaims), len(unverifiedClaims), len(invalidClaims), len(teamSummaries))

	return summaryData, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// truncateToSentences truncates text to at most n sentences.
func truncateToSentences(text string, n int) string {
	if n <= 0 || text == "" {
		return text
	}

	// Find sentence boundaries
	var sentences []string
	current := strings.Builder{}

	for _, r := range text {
		current.WriteRune(r)
		if r == '.' || r == '!' || r == '?' {
			sent := strings.TrimSpace(current.String())
			if sent != "" {
				sentences = append(sentences, sent)
				current.Reset()
			}
		}
	}

	// Add remaining text as last sentence if it exists
	remaining := strings.TrimSpace(current.String())
	if remaining != "" && len(sentences) < n {
		sentences = append(sentences, remaining)
	}

	if len(sentences) == 0 {
		// No sentence endings found, return original text (truncated if too long)
		if len(text) > 200 {
			return text[:200] + "..."
		}
		return text
	}

	if len(sentences) <= n {
		return strings.Join(sentences, " ")
	}

	// Return first n sentences
	return strings.Join(sentences[:n], " ")
}

// formatSummary formats the summary data for Discord display, returning multiple messages if needed.
// Sections are grouped: Summary & Context together, Claims together, Team Members together.
// If any section exceeds 1999 characters, it will be split.
func (m *Module) formatSummary(summary *cache.SummaryData, channelTitle string) []string {
	const maxChars = 1999
	var messages []string

	// Build header with Overview at the top
	// Overview at top, 3 newlines, then referendum info, 2 newlines, then content
	header := fmt.Sprintf("📋 Overview\n\n\n%s Referendum #%d\n%s\n\n", summary.Network, summary.RefID, channelTitle)

	// Section 1: Background Context & Summary (grouped together)
	var contextSummaryBuilder strings.Builder
	contextSummaryBuilder.WriteString(header)
	contextSummaryBuilder.WriteString("📖 Background Context\n\n")
	contextSummaryBuilder.WriteString(summary.BackgroundContext)
	contextSummaryBuilder.WriteString("\n\n")
	contextSummaryBuilder.WriteString("📝 Summary\n\n")
	contextSummaryBuilder.WriteString(summary.Summary)
	contextSummaryBuilder.WriteString("\n\n")

	contextSummaryText := contextSummaryBuilder.String()
	if len(contextSummaryText) > maxChars {
		// Split context and summary if needed
		contextPart := fmt.Sprintf("%s📖 Background Context\n\n%s", header, summary.BackgroundContext)
		summaryPart := fmt.Sprintf("📝 Summary\n\n%s", summary.Summary)

		if len(contextPart) <= maxChars {
			messages = append(messages, contextPart)
		} else {
			// Split context itself if too long
			messages = append(messages, splitLongText(header+"📖 Background Context\n\n", summary.BackgroundContext, maxChars)...)
		}

		if len(summaryPart) <= maxChars {
			messages = append(messages, summaryPart)
		} else {
			messages = append(messages, splitLongText("📝 Summary\n\n", summary.Summary, maxChars)...)
		}
	} else {
		messages = append(messages, contextSummaryText)
	}

	// Section 2: All Claims (grouped together)
	var claimsBuilder strings.Builder
	claimsBuilder.WriteString("🔍 Claims Analysis\n\n")

	// Valid Claims
	if len(summary.ValidClaims) > 0 {
		claimsBuilder.WriteString(fmt.Sprintf("✅ Valid Claims — %d verified\n", len(summary.ValidClaims)))
		for _, claim := range summary.ValidClaims {
			claimsBuilder.WriteString(fmt.Sprintf("  • %s\n", claim))
		}
		claimsBuilder.WriteString("\n")
	}

	// Unverified/Unknown Claims
	if len(summary.UnverifiedClaims) > 0 {
		claimsBuilder.WriteString(fmt.Sprintf("❓ Unverified Claims — %d unconfirmed\n", len(summary.UnverifiedClaims)))
		for _, claim := range summary.UnverifiedClaims {
			claimsBuilder.WriteString(fmt.Sprintf("  • %s\n", claim))
		}
		claimsBuilder.WriteString("\n")
	}

	// Invalid Claims
	if len(summary.InvalidClaims) > 0 {
		claimsBuilder.WriteString(fmt.Sprintf("❌ Invalid Claims — %d rejected\n", len(summary.InvalidClaims)))
		for _, claim := range summary.InvalidClaims {
			claimsBuilder.WriteString(fmt.Sprintf("  • %s\n", claim))
		}
		claimsBuilder.WriteString("\n")
	}

	// If no claims at all
	if len(summary.ValidClaims) == 0 && len(summary.UnverifiedClaims) == 0 && len(summary.InvalidClaims) == 0 {
		claimsBuilder.WriteString("No claims found\n")
	}

	claimsText := claimsBuilder.String()
	if len(claimsText) > maxChars {
		// Extract the content without the prefix for splitting
		claimsPrefix := "🔍 Claims Analysis\n\n"
		claimsContent := claimsText[len(claimsPrefix):]
		messages = append(messages, splitLongText(claimsPrefix, claimsContent, maxChars)...)
	} else {
		messages = append(messages, claimsText)
	}

	// Section 3: Team Members (all in one block)
	// First, calculate team breakdown statistics
	var realWithSkills, realNoSkills, notRealWithSkills, notRealNoSkills int

	if len(summary.TeamMembers) > 0 {
		for _, member := range summary.TeamMembers {
			isReal := member.IsReal
			hasSkills := member.HasStatedSkills

			if isReal && hasSkills {
				realWithSkills++
			} else if isReal && !hasSkills {
				realNoSkills++
			} else if !isReal && hasSkills {
				notRealWithSkills++
			} else if !isReal && !hasSkills {
				notRealNoSkills++
			}
		}
	}

	var teamContentBuilder strings.Builder

	// Add breakdown statistics
	if len(summary.TeamMembers) > 0 {
		teamContentBuilder.WriteString("📊 Team Breakdown:\n")
		if realWithSkills > 0 {
			teamContentBuilder.WriteString(fmt.Sprintf("  ✅ Real people with skills: %d\n", realWithSkills))
		}
		if realNoSkills > 0 {
			teamContentBuilder.WriteString(fmt.Sprintf("  ⚠️ Real people without skills: %d\n", realNoSkills))
		}
		if notRealWithSkills > 0 {
			teamContentBuilder.WriteString(fmt.Sprintf("  ⚠️ Not real people with skills: %d\n", notRealWithSkills))
		}
		if notRealNoSkills > 0 {
			teamContentBuilder.WriteString(fmt.Sprintf("  ❌ Not real people without skills: %d\n", notRealNoSkills))
		}
		teamContentBuilder.WriteString("\n")
	}

	// Add team member details
	if len(summary.TeamMembers) > 0 {
		for idx, member := range summary.TeamMembers {
			isRealStr := "No"
			if member.IsReal {
				isRealStr = "Yes"
			}
			hasSkillsStr := "No"
			if member.HasStatedSkills {
				hasSkillsStr = "Yes"
			}

			// Add spacing between team members
			if idx > 0 {
				teamContentBuilder.WriteString("\n")
			}

			// Truncate history to 2 sentences
			history := truncateToSentences(member.History, 2)

			teamContentBuilder.WriteString(fmt.Sprintf("%s (%s)\n", member.Name, member.Role))
			teamContentBuilder.WriteString(fmt.Sprintf("  • Real Person: %s  |  Has Skills: %s\n", isRealStr, hasSkillsStr))
			teamContentBuilder.WriteString(fmt.Sprintf("  • History: %s\n", history))
		}
	} else {
		teamContentBuilder.WriteString("No team members found\n")
	}

	teamPrefix := "⚡ Team Members\n\n"
	teamContent := teamContentBuilder.String()
	teamText := teamPrefix + teamContent

	if len(teamText) > maxChars {
		messages = append(messages, splitLongText(teamPrefix, teamContent, maxChars)...)
	} else {
		messages = append(messages, teamText)
	}

	return messages
}

// splitLongText splits a long text into chunks that fit within maxChars, preserving line breaks.
func splitLongText(prefix string, text string, maxChars int) []string {
	if len(prefix)+len(text) <= maxChars {
		return []string{prefix + text}
	}

	var chunks []string
	lines := strings.Split(text, "\n")
	var currentChunk strings.Builder
	currentChunk.WriteString(prefix)
	currentLen := len(prefix)

	for _, line := range lines {
		lineWithNewline := line + "\n"
		lineLen := len(lineWithNewline)

		// If adding this line would exceed the limit and we have content, start a new chunk
		if currentLen+lineLen > maxChars && currentLen > len(prefix) {
			chunks = append(chunks, strings.TrimRight(currentChunk.String(), "\n"))
			currentChunk.Reset()
			currentChunk.WriteString(prefix)
			currentLen = len(prefix)
		}

		// If a single line (with prefix) is too long, split it
		if len(prefix)+lineLen > maxChars {
			// Split the line itself
			remaining := line
			for len(remaining) > 0 {
				available := maxChars - currentLen - 1 // -1 for newline
				if available < 20 {
					// If we can't fit much, start a new chunk
					if currentLen > len(prefix) {
						chunks = append(chunks, strings.TrimRight(currentChunk.String(), "\n"))
						currentChunk.Reset()
						currentChunk.WriteString(prefix)
						currentLen = len(prefix)
					}
					available = maxChars - len(prefix) - 1
					if available < 20 {
						available = 100 // Fallback minimum
					}
				}
				if available > len(remaining) {
					available = len(remaining)
				}
				if available <= 0 {
					break
				}
				currentChunk.WriteString(remaining[:available])
				currentChunk.WriteString("\n")
				currentLen += available + 1
				remaining = remaining[available:]
			}
		} else {
			currentChunk.WriteString(lineWithNewline)
			currentLen += lineLen
		}
	}

	if currentChunk.Len() > len(prefix) {
		chunks = append(chunks, strings.TrimRight(currentChunk.String(), "\n"))
	}

	if len(chunks) == 0 {
		return []string{prefix + text}
	}

	return chunks
}

// handleSummarySlash handles the /summary command.
func (m *Module) handleSummarySlash(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if m.cfg.QARoleID != "" && !shareddiscord.HasRole(s, m.cfg.Base.GuildID, i.Member.User.ID, m.cfg.QARoleID) {
		formatted := shareddiscord.FormatStyledBlock("Summary", "You don't have permission to use this command.")
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
		log.Printf("question: summary ack failed: %v", err)
		return
	}

	threadInfo, err := m.refManager.FindThread(i.ChannelID)
	if err != nil || threadInfo == nil {
		sendStyledWebhookEdit(s, i.Interaction, "Summary", "This command must be used in a referendum thread.")
		return
	}

	network := m.networkManager.GetByID(threadInfo.NetworkID)
	if network == nil {
		sendStyledWebhookEdit(s, i.Interaction, "Summary", "Failed to identify network.")
		return
	}

	// Get cache entry
	entry, err := m.cacheManager.EnsureEntry(network.Name, uint32(threadInfo.RefID))
	if err != nil {
		sendStyledWebhookEdit(s, i.Interaction, "Summary", "Failed to load cache entry.")
		return
	}

	if entry.Summary == nil {
		sendStyledWebhookEdit(s, i.Interaction, "Summary", "No summary available. Please run /refresh first.")
		return
	}

	// Get channel name for title
	channel, err := s.Channel(i.ChannelID)
	channelName := ""
	if err == nil && channel != nil {
		channelName = channel.Name
	}
	if channelName == "" && entry.Summary != nil {
		channelName = entry.Summary.Title // Fallback
	}

	// Format and send summary
	summaryMessages := m.formatSummary(entry.Summary, channelName)

	if len(summaryMessages) == 0 {
		sendStyledWebhookEdit(s, i.Interaction, "Summary", "Summary formatting failed.")
		return
	}

	// Send first message as webhook edit (no title prefix)
	firstPayload := shareddiscord.BuildStyledMessage("", summaryMessages[0])
	edit := &discordgo.WebhookEdit{
		Content: &firstPayload.Content,
	}
	if len(firstPayload.Components) > 0 {
		edit.Components = &firstPayload.Components
	}
	if _, err := shareddiscord.InteractionResponseEditNoEmbed(s, i.Interaction, edit); err != nil {
		log.Printf("question: summary send failed: %v", err)
		return
	}

	// Send follow-up messages
	for idx := 1; idx < len(summaryMessages); idx++ {
		payload := shareddiscord.BuildStyledMessage("", summaryMessages[idx])
		msg := &discordgo.MessageSend{
			Content: payload.Content,
		}
		if len(payload.Components) > 0 {
			msg.Components = payload.Components
		}
		if _, err := shareddiscord.SendComplexMessageNoEmbed(s, i.ChannelID, msg); err != nil {
			log.Printf("question: summary follow-up send failed: %v", err)
			return
		}
	}
}
