package bot

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/stake-plus/govcomms/src/ai-qa/components/ai"
	"github.com/stake-plus/govcomms/src/ai-qa/components/network"
	"github.com/stake-plus/govcomms/src/ai-qa/components/processor"
	"github.com/stake-plus/govcomms/src/ai-qa/components/referendum"
	"github.com/stake-plus/govcomms/src/ai-qa/config"
	"gorm.io/gorm"
)

type Bot struct {
	config         *config.Config
	db             *gorm.DB
	session        *discordgo.Session
	processor      *processor.Processor
	aiClient       ai.Client
	networkManager *network.Manager
	refManager     *referendum.Manager
	cancelFunc     context.CancelFunc
}

func New(cfg *config.Config, db *gorm.DB) (*Bot, error) {
	session, err := discordgo.New("Bot " + cfg.Token)
	if err != nil {
		return nil, fmt.Errorf("failed to create Discord session: %w", err)
	}

	session.Identify.Intents = discordgo.IntentsGuilds |
		discordgo.IntentsGuildMessages |
		discordgo.IntentsMessageContent

	networkManager, err := network.NewManager(db)
	if err != nil {
		log.Printf("Failed to create network manager: %v", err)
		networkManager = nil
	}

	refManager := referendum.NewManager(db)

	var aiClient ai.Client
	if cfg.AIProvider == "claude" && cfg.ClaudeKey != "" {
		aiClient = ai.NewClaudeClient(cfg.ClaudeKey, cfg.AISystemPrompt)
	} else if cfg.OpenAIKey != "" {
		aiClient = ai.NewOpenAIClient(cfg.OpenAIKey, cfg.AISystemPrompt)
	} else {
		return nil, fmt.Errorf("no AI provider configured")
	}

	proc := processor.NewProcessor(cfg.TempDir, db)

	bot := &Bot{
		config:         cfg,
		db:             db,
		session:        session,
		processor:      proc,
		aiClient:       aiClient,
		networkManager: networkManager,
		refManager:     refManager,
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

		if strings.HasPrefix(content, "!question ") {
			b.handleQuestion(s, m)
		} else if content == "!refresh" {
			b.handleRefresh(s, m)
		}
	})
}

func (b *Bot) handleQuestion(s *discordgo.Session, m *discordgo.MessageCreate) {
	if b.config.QARoleID != "" && !b.hasRole(s, b.config.GuildID, m.Author.ID, b.config.QARoleID) {
		return
	}

	question := strings.TrimPrefix(m.Content, "!question ")
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

	answer, err := b.aiClient.Ask(content, question)
	if err != nil {
		log.Printf("Error getting AI response: %v", err)
		s.ChannelMessageSend(m.ChannelID, "Failed to generate answer. Please try again.")
		return
	}

	b.sendLongMessage(s, m.ChannelID, m.Author.ID, answer)
}

func (b *Bot) sendLongMessage(s *discordgo.Session, channelID string, userID string, message string) {
	// Add user mention to first message
	firstMessage := fmt.Sprintf("<@%s> %s", userID, message)

	// If the message fits in one Discord message
	if len(firstMessage) <= 2000 {
		s.ChannelMessageSend(channelID, firstMessage)
		return
	}

	// Split into multiple messages
	messages := b.splitMessage(message, userID)
	for i, msg := range messages {
		if i > 0 {
			// Add a small delay between messages to avoid rate limits
			s.ChannelTyping(channelID)
		}
		s.ChannelMessageSend(channelID, msg)
	}
}

func (b *Bot) splitMessage(message string, userID string) []string {
	const maxLength = 1900 // Leave some buffer for formatting
	var messages []string

	// First message includes the user mention
	firstMaxLength := maxLength - len(fmt.Sprintf("<@%s> ", userID))

	// Split by paragraphs first to preserve formatting
	paragraphs := strings.Split(message, "\n\n")

	var currentMessage strings.Builder
	isFirst := true

	for _, paragraph := range paragraphs {
		// Check if paragraph itself is too long
		if len(paragraph) > maxLength {
			// If we have content, save it first
			if currentMessage.Len() > 0 {
				if isFirst {
					messages = append(messages, fmt.Sprintf("<@%s> %s", userID, currentMessage.String()))
					isFirst = false
				} else {
					messages = append(messages, currentMessage.String())
				}
				currentMessage.Reset()
			}

			// Split long paragraph by sentences
			sentences := b.splitBySentences(paragraph)
			for _, sentence := range sentences {
				effectiveMaxLength := maxLength
				if isFirst {
					effectiveMaxLength = firstMaxLength
				}

				if currentMessage.Len()+len(sentence)+2 > effectiveMaxLength {
					if currentMessage.Len() > 0 {
						if isFirst {
							messages = append(messages, fmt.Sprintf("<@%s> %s", userID, currentMessage.String()))
							isFirst = false
						} else {
							messages = append(messages, currentMessage.String())
						}
						currentMessage.Reset()
					}
				}

				if currentMessage.Len() > 0 {
					currentMessage.WriteString(" ")
				}
				currentMessage.WriteString(sentence)
			}
		} else {
			// Check if adding this paragraph would exceed limit
			effectiveMaxLength := maxLength
			if isFirst {
				effectiveMaxLength = firstMaxLength
			}

			if currentMessage.Len()+len(paragraph)+4 > effectiveMaxLength {
				// Save current message and start new one
				if currentMessage.Len() > 0 {
					if isFirst {
						messages = append(messages, fmt.Sprintf("<@%s> %s", userID, currentMessage.String()))
						isFirst = false
					} else {
						messages = append(messages, currentMessage.String())
					}
					currentMessage.Reset()
				}
			}

			if currentMessage.Len() > 0 {
				currentMessage.WriteString("\n\n")
			}
			currentMessage.WriteString(paragraph)
		}
	}

	// Add any remaining content
	if currentMessage.Len() > 0 {
		if isFirst {
			messages = append(messages, fmt.Sprintf("<@%s> %s", userID, currentMessage.String()))
		} else {
			messages = append(messages, currentMessage.String())
		}
	}

	// Add continuation indicators
	for i := 1; i < len(messages)-1; i++ {
		messages[i] = messages[i] + "\n*(continued...)*"
	}
	if len(messages) > 1 {
		messages[len(messages)-1] = messages[len(messages)-1] + "\n*(end of response)*"
	}

	return messages
}

func (b *Bot) splitBySentences(text string) []string {
	var sentences []string
	var current strings.Builder

	// Simple sentence splitting by common terminators
	for _, char := range text {
		current.WriteRune(char)
		if char == '.' || char == '!' || char == '?' {
			sentences = append(sentences, strings.TrimSpace(current.String()))
			current.Reset()
		}
	}

	// Add any remaining text
	if current.Len() > 0 {
		sentences = append(sentences, strings.TrimSpace(current.String()))
	}

	// If no sentences found, split by spaces
	if len(sentences) == 0 || (len(sentences) == 1 && len(sentences[0]) > 1900) {
		words := strings.Fields(text)
		var chunks []string
		var chunk strings.Builder

		for _, word := range words {
			if chunk.Len()+len(word)+1 > 1800 {
				chunks = append(chunks, chunk.String())
				chunk.Reset()
			}
			if chunk.Len() > 0 {
				chunk.WriteString(" ")
			}
			chunk.WriteString(word)
		}
		if chunk.Len() > 0 {
			chunks = append(chunks, chunk.String())
		}
		return chunks
	}

	return sentences
}

func (b *Bot) handleRefresh(s *discordgo.Session, m *discordgo.MessageCreate) {
	if b.config.QARoleID != "" && !b.hasRole(s, b.config.GuildID, m.Author.ID, b.config.QARoleID) {
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

func (b *Bot) hasRole(s *discordgo.Session, guildID, userID, roleID string) bool {
	if roleID == "" {
		return true
	}

	member, err := s.GuildMember(guildID, userID)
	if err != nil {
		return false
	}

	for _, role := range member.Roles {
		if role == roleID {
			return true
		}
	}
	return false
}

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
