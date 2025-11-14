package question

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/stake-plus/govcomms/src/actions/core"
	cache "github.com/stake-plus/govcomms/src/cache"
	sharedai "github.com/stake-plus/govcomms/src/shared/ai"
	sharedconfig "github.com/stake-plus/govcomms/src/shared/config"
	shareddiscord "github.com/stake-plus/govcomms/src/shared/discord"
	sharedgov "github.com/stake-plus/govcomms/src/shared/gov"
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
	aiClient       sharedai.Client
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
	aiClient := sharedai.NewClient(sharedai.FactoryConfig{
		Provider:     cfg.AIConfig.AIProvider,
		OpenAIKey:    cfg.AIConfig.OpenAIKey,
		ClaudeKey:    cfg.AIConfig.ClaudeKey,
		SystemPrompt: cfg.AIConfig.AISystemPrompt,
		Model:        cfg.AIConfig.AIModel,
		Temperature:  0,
	})

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
		msg := "Please provide a valid question (at least 5 characters)."
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &msg})
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

	content, err := m.cacheManager.GetProposalContent(network.Name, uint32(threadInfo.RefID))
	if err != nil {
		log.Printf("question: proposal content: %v", err)
		msg := "Failed to retrieve proposal content. Please try /refresh first."
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &msg})
		return
	}

	qaContext, err := m.contextStore.BuildContext(threadInfo.NetworkID, uint32(threadInfo.RefID))
	if err != nil {
		log.Printf("question: build context: %v", err)
		qaContext = ""
	}

	fullContent := content + qaContext
	ctx := context.Background()
	opts := sharedai.Options{
		Model:        m.cfg.AIModel,
		SystemPrompt: m.cfg.AISystemPrompt,
	}

	input := "Context:\n" + fullContent + "\n\nQuestion:\n" + question + "\n\nUse web search as needed to ensure the answer reflects the latest information."
	answer, err := m.aiClient.Respond(ctx, input, []sharedai.Tool{{Type: "web_search"}}, opts)
	if err != nil {
		log.Printf("question: web search failed, fallback: %v", err)
		answer, err = m.aiClient.AnswerQuestion(ctx, fullContent, question, opts)
	}
	if err != nil {
		log.Printf("question: AI failure: %v", err)
		msg := "Failed to generate answer. Please try again."
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &msg})
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

	answer := shareddiscord.BeautifyForDiscord(message)
	formatted := answer
	if strings.TrimSpace(question) != "" {
		formatted = fmt.Sprintf("> **Question:** %s\n\n%s", question, answer)
	}

	mentionPrefix := ""
	mentionContent := ""
	if userID != "" {
		mentionPrefix = fmt.Sprintf("<@%s> ", userID)
		mentionContent = fmt.Sprintf("<@%s>", userID)
	}

	msgs := shareddiscord.BuildLongMessages(formatted, userID)
	if len(msgs) == 0 {
		return
	}

	flags := discordgo.MessageFlagsSuppressEmbeds
	first := msgs[0]
	firstBody := strings.TrimSpace(strings.TrimPrefix(first, mentionPrefix))

	embedSent := false
	if firstBody != "" && len(firstBody) <= 4000 {
		embed := &discordgo.MessageEmbed{
			Description: firstBody,
			Color:       answerEmbedColor,
		}
		if questionTitle := strings.TrimSpace(question); questionTitle != "" {
			title := fmt.Sprintf("Answer • %s", questionTitle)
			if len(title) > 256 {
				title = title[:253] + "..."
			}
			embed.Title = title
		} else {
			embed.Title = "AI Answer"
		}
		var contentPtr *string
		if mentionContent != "" {
			contentPtr = &mentionContent
		}
		if _, err := s.InteractionResponseEdit(interaction, &discordgo.WebhookEdit{
			Content: contentPtr,
			Embeds:  &[]*discordgo.MessageEmbed{embed},
		}); err == nil {
			embedSent = true
		} else {
			log.Printf("question: embed response failed: %v", err)
		}
	}

	if !embedSent {
		resp, err := s.InteractionResponseEdit(interaction, &discordgo.WebhookEdit{
			Content: &first,
		})
		if err != nil {
			log.Printf("question: follow-up failed: %v", err)
			return
		}
		if resp != nil && resp.ChannelID != "" && resp.ID != "" {
			content := resp.Content
			if _, err := s.ChannelMessageEditComplex(&discordgo.MessageEdit{
				ID:      resp.ID,
				Channel: resp.ChannelID,
				Content: &content,
				Flags:   flags,
			}); err != nil {
				log.Printf("question: suppress embeds failed: %v", err)
			}
		}
	}

	for idx := 1; idx < len(msgs); idx++ {
		if _, err := s.ChannelMessageSendComplex(interaction.ChannelID, &discordgo.MessageSend{
			Content: msgs[idx],
			Flags:   flags,
		}); err != nil {
			log.Printf("question: follow-up send failed: %v", err)
			return
		}
	}
}
