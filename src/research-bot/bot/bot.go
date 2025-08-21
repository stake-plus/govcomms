package bot

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/stake-plus/govcomms/src/research-bot/components/network"
	"github.com/stake-plus/govcomms/src/research-bot/components/referendum"
	"github.com/stake-plus/govcomms/src/research-bot/components/research"
	"github.com/stake-plus/govcomms/src/research-bot/config"
	"gorm.io/gorm"
)

type Bot struct {
	config         *config.Config
	db             *gorm.DB
	session        *discordgo.Session
	networkManager *network.Manager
	refManager     *referendum.Manager
	researcher     *research.Researcher
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
	researcher := research.NewResearcher(cfg.OpenAIKey, cfg.TempDir)

	bot := &Bot{
		config:         cfg,
		db:             db,
		session:        session,
		networkManager: networkManager,
		refManager:     refManager,
		researcher:     researcher,
	}

	bot.initHandlers()
	return bot, nil
}

func (b *Bot) initHandlers() {
	b.session.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		log.Printf("Research Bot logged in as: %v#%v", s.State.User.Username, s.State.User.Discriminator)
	})

	b.session.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
		if m.Author.ID == s.State.User.ID {
			return
		}

		content := strings.TrimSpace(m.Content)
		if strings.HasPrefix(content, "!research-claims") {
			b.handleResearchClaims(s, m)
		} else if strings.HasPrefix(content, "!research-proponent") {
			b.handleResearchProponent(s, m)
		}
	})
}

func (b *Bot) handleResearchClaims(s *discordgo.Session, m *discordgo.MessageCreate) {
	if b.config.ResearchRoleID != "" && !b.hasRole(s, b.config.GuildID, m.Author.ID, b.config.ResearchRoleID) {
		return
	}

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

	s.ChannelTyping(m.ChannelID)
	s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Starting claim verification for %s referendum #%d...", network.Name, threadInfo.RefID))

	go func() {
		results, err := b.researcher.VerifyClaims(network.Name, uint32(threadInfo.RefID))
		if err != nil {
			if err.Error() == "proposal content not found" {
				s.ChannelMessageSend(m.ChannelID, "Proposal content not found. Please run !refresh first.")
			} else {
				s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Error verifying claims: %v", err))
			}
			return
		}

		b.sendVerificationResults(s, m.ChannelID, results, "Claim Verification Results")
	}()
}

func (b *Bot) handleResearchProponent(s *discordgo.Session, m *discordgo.MessageCreate) {
	if b.config.ResearchRoleID != "" && !b.hasRole(s, b.config.GuildID, m.Author.ID, b.config.ResearchRoleID) {
		return
	}

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

	s.ChannelTyping(m.ChannelID)
	s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Starting proponent research for %s referendum #%d...", network.Name, threadInfo.RefID))

	go func() {
		results, err := b.researcher.ResearchProponent(network.Name, uint32(threadInfo.RefID))
		if err != nil {
			if err.Error() == "proposal content not found" {
				s.ChannelMessageSend(m.ChannelID, "Proposal content not found. Please run !refresh first.")
			} else {
				s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Error researching proponent: %v", err))
			}
			return
		}

		b.sendVerificationResults(s, m.ChannelID, results, "Proponent Research Results")
	}()
}

func (b *Bot) sendVerificationResults(s *discordgo.Session, channelID string, results []research.VerificationResult, title string) {
	if len(results) == 0 {
		s.ChannelMessageSend(channelID, "No verifiable claims found in the proposal.")
		return
	}

	var embedFields []*discordgo.MessageEmbedField

	verifiedCount := 0
	unverifiedCount := 0
	failedCount := 0

	for _, result := range results {
		statusEmoji := "❓"

		switch result.Status {
		case research.StatusVerified:
			statusEmoji = "✅"
			verifiedCount++
		case research.StatusUnverified:
			statusEmoji = "❌"
			unverifiedCount++
		case research.StatusFailed:
			statusEmoji = "⚠️"
			failedCount++
		}

		fieldValue := fmt.Sprintf("%s **%s**\n%s", statusEmoji, result.Status, result.Evidence)
		if len(fieldValue) > 1024 {
			fieldValue = fieldValue[:1021] + "..."
		}

		embedFields = append(embedFields, &discordgo.MessageEmbedField{
			Name:   result.Claim,
			Value:  fieldValue,
			Inline: false,
		})

		if len(embedFields) >= 25 {
			break
		}
	}

	color := 0x00FF00
	if unverifiedCount > verifiedCount {
		color = 0xFF0000
	} else if failedCount > verifiedCount {
		color = 0xFFFF00
	}

	embed := &discordgo.MessageEmbed{
		Title:  title,
		Color:  color,
		Fields: embedFields,
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("✅ Verified: %d | ❌ Unverified: %d | ⚠️ Failed: %d",
				verifiedCount, unverifiedCount, failedCount),
		},
	}

	s.ChannelMessageSendEmbed(channelID, embed)
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
