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
	})

	b.session.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
		if m.Author.ID == s.State.User.ID {
			return
		}

		content := strings.TrimSpace(m.Content)

		if strings.HasPrefix(content, shareddiscord.CmdQuestion) {
			b.handleQuestion(s, m)
		} else if content == shareddiscord.CmdRefresh {
			b.handleRefresh(s, m)
		} else if content == shareddiscord.CmdContext {
			b.handleShowContext(s, m)
		}
	})
}

func (b *Bot) handleQuestion(s *discordgo.Session, m *discordgo.MessageCreate) {
	if b.config.QARoleID != "" && !shareddiscord.HasRole(s, b.config.GuildID, m.Author.ID, b.config.QARoleID) {
		return
	}

	question := strings.TrimPrefix(m.Content, shareddiscord.CmdQuestion)
	if len(question) < 5 {
		s.ChannelMessageSend(m.ChannelID, "Please provide a valid question.")
		return
	}

	s.ChannelTyping(m.ChannelID)

	threadInfo, err := b.refManager.FindThread(m.ChannelID)
	if err != nil || threadInfo == nil {
		s.ChannelMessageSend(m.ChannelID, "This command must be used in a referendum thread.")
		return
	}

	network := b.networkManager.GetByID(threadInfo.NetworkID)
	if network == nil {
		s.ChannelMessageSend(m.ChannelID, "Failed to identify network.")
		return
	}

	content, err := b.processor.GetProposalContent(network.Name, uint32(threadInfo.RefID))
	if err != nil {
		log.Printf("Error getting proposal content: %v", err)
		s.ChannelMessageSend(m.ChannelID, "Failed to retrieve proposal content. Please try !refresh first.")
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

	var answer string

	if b.config.AIConfig.AIEnableWeb {
		// Use web search tools via shared client when enabled
		client, ok := b.aiClient.(interface {
			Respond(context.Context, string, []sharedai.Tool, sharedai.Options) (string, error)
		})
		if ok {
			input := "Context:\n" + fullContent + "\n\nQuestion:\n" + question
			answer, err = client.Respond(context.Background(), input, []sharedai.Tool{{Type: "web_search"}}, sharedai.Options{
				Model:               b.config.AIModel,
				SystemPrompt:        b.config.AISystemPrompt,
				MaxCompletionTokens: 0,
			})
		} else {
			answer, err = b.aiClient.AnswerQuestion(context.Background(), fullContent, question, sharedai.Options{Model: b.config.AIModel, SystemPrompt: b.config.AISystemPrompt})
		}
	} else {
		answer, err = b.aiClient.AnswerQuestion(context.Background(), fullContent, question, sharedai.Options{
			Model:        b.config.AIModel,
			SystemPrompt: b.config.AISystemPrompt,
		})
	}
	if err != nil {
		log.Printf("Error getting AI response: %v", err)
		s.ChannelMessageSend(m.ChannelID, "Failed to generate answer. Please try again.")
		return
	}

	// Save Q&A to history
	err = b.contextManager.SaveQA(threadInfo.NetworkID, uint32(threadInfo.RefID), m.ChannelID, m.Author.ID, question, answer)
	if err != nil {
		log.Printf("Error saving Q&A history: %v", err)
	}

	b.sendLongMessage(s, m.ChannelID, m.Author.ID, answer)
}

func (b *Bot) handleShowContext(s *discordgo.Session, m *discordgo.MessageCreate) {
	if b.config.QARoleID != "" && !shareddiscord.HasRole(s, b.config.GuildID, m.Author.ID, b.config.QARoleID) {
		return
	}

	threadInfo, err := b.refManager.FindThread(m.ChannelID)
	if err != nil || threadInfo == nil {
		s.ChannelMessageSend(m.ChannelID, "This command must be used in a referendum thread.")
		return
	}

	qas, err := b.contextManager.GetRecentQAs(threadInfo.NetworkID, uint32(threadInfo.RefID), 10)
	if err != nil {
		s.ChannelMessageSend(m.ChannelID, "Failed to retrieve context.")
		return
	}

	if len(qas) == 0 {
		s.ChannelMessageSend(m.ChannelID, "No previous Q&A found for this referendum.")
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

	s.ChannelMessageSend(m.ChannelID, response.String())
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

func (b *Bot) handleRefresh(s *discordgo.Session, m *discordgo.MessageCreate) {
	if b.config.QARoleID != "" && !shareddiscord.HasRole(s, b.config.GuildID, m.Author.ID, b.config.QARoleID) {
		return
	}

	s.ChannelTyping(m.ChannelID)

	threadInfo, err := b.refManager.FindThread(m.ChannelID)
	if err != nil || threadInfo == nil {
		s.ChannelMessageSend(m.ChannelID, "This command must be used in a referendum thread.")
		return
	}

	network := b.networkManager.GetByID(threadInfo.NetworkID)
	if network == nil {
		s.ChannelMessageSend(m.ChannelID, "Failed to identify network.")
		return
	}

	err = b.processor.RefreshProposal(network.Name, uint32(threadInfo.RefID))
	if err != nil {
		log.Printf("Error refreshing proposal: %v", err)
		s.ChannelMessageSend(m.ChannelID, "Failed to refresh proposal content.")
		return
	}

	s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("✅ Refreshed content for %s referendum #%d", network.Name, threadInfo.RefID))
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
