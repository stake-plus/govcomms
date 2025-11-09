package bot

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/stake-plus/govcomms/src/ai-qa/components/processor"
	sharedai "github.com/stake-plus/govcomms/src/shared/ai"
	sharedconfig "github.com/stake-plus/govcomms/src/shared/config"
	shareddiscord "github.com/stake-plus/govcomms/src/shared/discord"
	sharedgov "github.com/stake-plus/govcomms/src/shared/gov"
	"gorm.io/gorm"
)

type Bot struct {
	config         *sharedconfig.QAConfig
	db             *gorm.DB
	session        *discordgo.Session
	processor      *processor.Processor
	aiClient       sharedai.Client
	networkManager *sharedgov.NetworkManager
	refManager     *sharedgov.ReferendumManager
	contextManager *processor.ContextManager
	cancelFunc     context.CancelFunc
}

func New(cfg *sharedconfig.QAConfig, db *gorm.DB) (*Bot, error) {
	session, err := discordgo.New("Bot " + cfg.Base.Token)
	if err != nil {
		return nil, fmt.Errorf("failed to create Discord session: %w", err)
	}

	session.Identify.Intents = discordgo.IntentsGuilds |
		discordgo.IntentsGuildMessages |
		discordgo.IntentsMessageContent

	networkManager, err := sharedgov.NewNetworkManager(db)
	if err != nil {
		log.Printf("Failed to create network manager: %v", err)
		networkManager = nil
	}

	refManager := sharedgov.NewReferendumManager(db)

	// Create shared AI client
	if cfg.AIConfig.OpenAIKey == "" && cfg.AIConfig.ClaudeKey == "" {
		return nil, fmt.Errorf("no AI provider configured: set OPENAI_API_KEY or CLAUDE_API_KEY")
	}
	aiClient := sharedai.NewClient(sharedai.FactoryConfig{
		Provider:     cfg.AIConfig.AIProvider,
		OpenAIKey:    cfg.AIConfig.OpenAIKey,
		ClaudeKey:    cfg.AIConfig.ClaudeKey,
		SystemPrompt: cfg.AIConfig.AISystemPrompt,
		Model:        cfg.AIConfig.AIModel,
		Temperature:  0, // use defaults
	})

	proc := processor.NewProcessor(cfg.TempDir, db)
	contextMgr := processor.NewContextManager(db)

	bot := &Bot{
		config:         cfg,
		db:             db,
		session:        session,
		processor:      proc,
		aiClient:       aiClient,
		networkManager: networkManager,
		refManager:     refManager,
		contextManager: contextMgr,
	}

	bot.initHandlers()
	return bot, nil
}

func (b *Bot) initHandlers() {
	b.session.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		log.Printf("AI Q&A Bot logged in as: %v#%v", s.State.User.Username, s.State.User.Discriminator)
		// Register slash commands scoped to the Q&A bot
		if err := shareddiscord.RegisterSlashCommands(s, b.config.Base.GuildID,
			shareddiscord.CommandQuestion,
			shareddiscord.CommandRefresh,
			shareddiscord.CommandContext,
		); err != nil {
			log.Printf("Failed to register slash commands: %v", err)
		} else {
			log.Println("Slash commands registered")
		}
	})

	// Handle slash command interactions
	b.session.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		if i.ApplicationCommandData().Name == "question" {
			b.handleQuestionSlash(s, i)
		} else if i.ApplicationCommandData().Name == "refresh" {
			b.handleRefreshSlash(s, i)
		} else if i.ApplicationCommandData().Name == "context" {
			b.handleContextSlash(s, i)
		}
	})
}

func (b *Bot) handleQuestionSlash(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Check role permissions first
	if b.config.QARoleID != "" && !shareddiscord.HasRole(s, b.config.Base.GuildID, i.Member.User.ID, b.config.QARoleID) {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "You don't have permission to use this command.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Respond immediately to acknowledge the interaction
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})
	if err != nil {
		log.Printf("Failed to acknowledge interaction: %v", err)
		return
	}

	// Extract question from options
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
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: &msg,
		})
		return
	}

	channelID := i.ChannelID

	threadInfo, err := b.refManager.FindThread(channelID)
	if err != nil || threadInfo == nil {
		msg := "This command must be used in a referendum thread."
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: &msg,
		})
		return
	}

	network := b.networkManager.GetByID(threadInfo.NetworkID)
	if network == nil {
		msg := "Failed to identify network."
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: &msg,
		})
		return
	}

	content, err := b.processor.GetProposalContent(network.Name, uint32(threadInfo.RefID))
	if err != nil {
		log.Printf("Error getting proposal content: %v", err)
		msg := "Failed to retrieve proposal content. Please try /refresh first."
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: &msg,
		})
		return
	}

	// Get previous Q&A context
	qaContext, err := b.contextManager.BuildContext(threadInfo.NetworkID, uint32(threadInfo.RefID))
	if err != nil {
		log.Printf("Error building context: %v", err)
		qaContext = ""
	}

	// Combine proposal content with Q&A history
	fullContent := content + qaContext

	ctx := context.Background()
	opts := sharedai.Options{
		Model:        b.config.AIModel,
		SystemPrompt: b.config.AISystemPrompt,
	}

	input := "Context:\n" + fullContent + "\n\nQuestion:\n" + question + "\n\nUse web search as needed to ensure the answer reflects the latest information."

	answer, err := b.aiClient.Respond(ctx, input, []sharedai.Tool{{Type: "web_search"}}, opts)
	if err != nil {
		log.Printf("Web-search answer failed, falling back to cached content: %v", err)
		answer, err = b.aiClient.AnswerQuestion(ctx, fullContent, question, opts)
	}
	if err != nil {
		log.Printf("Error getting AI response: %v", err)
		msg := "Failed to generate answer. Please try again."
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: &msg,
		})
		return
	}

	// Save Q&A to history
	err = b.contextManager.SaveQA(threadInfo.NetworkID, uint32(threadInfo.RefID), channelID, i.Member.User.ID, question, answer)
	if err != nil {
		log.Printf("Error saving Q&A history: %v", err)
	}

	b.sendLongMessageSlash(s, i.Interaction, answer)
}

func (b *Bot) sendLongMessageSlash(s *discordgo.Session, interaction *discordgo.Interaction, message string) {
	msgs := shareddiscord.BuildLongMessages(message, "")
	if len(msgs) > 0 {
		// Edit the deferred response with the first chunk
		s.InteractionResponseEdit(interaction, &discordgo.WebhookEdit{
			Content: &msgs[0],
		})
		// Send additional chunks as regular messages
		for idx := 1; idx < len(msgs); idx++ {
			s.ChannelMessageSend(interaction.ChannelID, msgs[idx])
		}
	}
}

func (b *Bot) handleContextSlash(s *discordgo.Session, i *discordgo.InteractionCreate) {
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})
	if err != nil {
		log.Printf("Failed to acknowledge interaction: %v", err)
		return
	}

	if b.config.QARoleID != "" && !shareddiscord.HasRole(s, b.config.Base.GuildID, i.Member.User.ID, b.config.QARoleID) {
		msg := "You don't have permission to use this command."
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: &msg,
		})
		return
	}

	threadInfo, err := b.refManager.FindThread(i.ChannelID)
	if err != nil || threadInfo == nil {
		msg := "This command must be used in a referendum thread."
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: &msg,
		})
		return
	}

	qas, err := b.contextManager.GetRecentQAs(threadInfo.NetworkID, uint32(threadInfo.RefID), 10)
	if err != nil {
		msg := "Failed to retrieve context."
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: &msg,
		})
		return
	}

	if len(qas) == 0 {
		msg := "No previous Q&A found for this referendum."
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: &msg,
		})
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
	s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: &content,
	})
}

func (b *Bot) handleRefreshSlash(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Check role permissions first
	if b.config.QARoleID != "" && !shareddiscord.HasRole(s, b.config.Base.GuildID, i.Member.User.ID, b.config.QARoleID) {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "You don't have permission to use this command.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})
	if err != nil {
		log.Printf("Failed to acknowledge interaction: %v", err)
		return
	}

	threadInfo, err := b.refManager.FindThread(i.ChannelID)
	if err != nil || threadInfo == nil {
		msg := "This command must be used in a referendum thread."
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: &msg,
		})
		return
	}

	network := b.networkManager.GetByID(threadInfo.NetworkID)
	if network == nil {
		msg := "Failed to identify network."
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: &msg,
		})
		return
	}

	err = b.processor.RefreshProposal(network.Name, uint32(threadInfo.RefID))
	if err != nil {
		log.Printf("Error refreshing proposal: %v", err)
		msg := "Failed to refresh proposal content."
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: &msg,
		})
		return
	}

	msg := fmt.Sprintf("✅ Refreshed content for %s referendum #%d", network.Name, threadInfo.RefID)
	s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: &msg,
	})
}

func (b *Bot) sendLongMessage(s *discordgo.Session, channelID string, userID string, message string) {
	msgs := shareddiscord.BuildLongMessages(message, userID)
	for i, msg := range msgs {
		if i > 0 {
			s.ChannelTyping(channelID)
		}
		s.ChannelMessageSend(channelID, msg)
	}
}

// role check centralized in shared/discord

func (b *Bot) Start() error {
	if err := b.session.Open(); err != nil {
		return fmt.Errorf("failed to open Discord connection: %w", err)
	}

	_, cancel := context.WithCancel(context.Background())
	b.cancelFunc = cancel

	return nil
}

func (b *Bot) Stop() {
	if b.cancelFunc != nil {
		b.cancelFunc()
	}

	if b.session != nil {
		b.session.Close()
	}
}
