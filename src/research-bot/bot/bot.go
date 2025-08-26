package bot

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

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
		if strings.HasPrefix(content, "!research") {
			b.handleResearch(s, m)
		} else if strings.HasPrefix(content, "!team") {
			b.handleTeam(s, m)
		}
	})
}

func (b *Bot) handleResearch(s *discordgo.Session, m *discordgo.MessageCreate) {
	if b.config.ResearchRoleID != "" && !b.hasRole(s, b.config.GuildID, m.Author.ID, b.config.ResearchRoleID) {
		s.ChannelMessageSend(m.ChannelID, "You don't have permission to use this command.")
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

	// Send initial message
	initialMsg, _ := s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("🔍 Starting claim verification for %s referendum #%d...", network.Name, threadInfo.RefID))

	go func() {
		// Create context with 5 minute timeout
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		// Extract and verify claims
		claims, err := b.researcher.ExtractClaims(ctx, network.Name, uint32(threadInfo.RefID))
		if err != nil {
			if err == context.DeadlineExceeded {
				s.ChannelMessageEdit(m.ChannelID, initialMsg.ID, "⏱️ Verification timeout - process took longer than 5 minutes")
			} else if err.Error() == "proposal content not found" {
				s.ChannelMessageEdit(m.ChannelID, initialMsg.ID, "Proposal content not found. Please run !refresh first.")
			} else {
				s.ChannelMessageEdit(m.ChannelID, initialMsg.ID, fmt.Sprintf("Error extracting claims: %v", err))
			}
			return
		}

		if len(claims) == 0 {
			s.ChannelMessageEdit(m.ChannelID, initialMsg.ID, "No verifiable claims found in the proposal.")
			return
		}

		// Post list of claims being verified
		claimsList := "**Claims to verify:**\n"
		for i, claim := range claims {
			claimsList += fmt.Sprintf("%d. %s\n", i+1, claim.Claim)
		}
		s.ChannelMessageEdit(m.ChannelID, initialMsg.ID, claimsList+"\n⏳ Verifying claims...")

		// Verify claims with timeout
		results, err := b.researcher.VerifyClaims(ctx, claims)
		if err != nil {
			if err == context.DeadlineExceeded {
				s.ChannelMessageEdit(m.ChannelID, initialMsg.ID, claimsList+"\n⏱️ Verification timeout - exceeded 5 minute limit")
			} else {
				s.ChannelMessageEdit(m.ChannelID, initialMsg.ID, claimsList+fmt.Sprintf("\n❌ Verification failed: %v", err))
			}
			return
		}

		// Send verification results
		b.sendVerificationResults(s, m.ChannelID, results)
	}()
}

func (b *Bot) handleTeam(s *discordgo.Session, m *discordgo.MessageCreate) {
	if b.config.ResearchRoleID != "" && !b.hasRole(s, b.config.GuildID, m.Author.ID, b.config.ResearchRoleID) {
		s.ChannelMessageSend(m.ChannelID, "You don't have permission to use this command.")
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

	// Send initial message
	initialMsg, _ := s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("👥 Starting team analysis for %s referendum #%d...", network.Name, threadInfo.RefID))

	go func() {
		// Create context with 5 minute timeout
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		// Extract team members
		members, err := b.researcher.ExtractTeamMembers(ctx, network.Name, uint32(threadInfo.RefID))
		if err != nil {
			if err == context.DeadlineExceeded {
				s.ChannelMessageEdit(m.ChannelID, initialMsg.ID, "⏱️ Analysis timeout - process took longer than 5 minutes")
			} else if err.Error() == "proposal content not found" {
				s.ChannelMessageEdit(m.ChannelID, initialMsg.ID, "Proposal content not found. Please run !refresh first.")
			} else {
				s.ChannelMessageEdit(m.ChannelID, initialMsg.ID, fmt.Sprintf("Error extracting team members: %v", err))
			}
			return
		}

		if len(members) == 0 {
			s.ChannelMessageEdit(m.ChannelID, initialMsg.ID, "No team members found in the proposal.")
			return
		}

		// Post list of team members being analyzed
		teamList := "**Team members to analyze:**\n"
		for i, member := range members {
			teamList += fmt.Sprintf("%d. %s", i+1, member.Name)
			if member.Role != "" {
				teamList += fmt.Sprintf(" (%s)", member.Role)
			}
			teamList += "\n"
		}
		s.ChannelMessageEdit(m.ChannelID, initialMsg.ID, teamList+"\n⏳ Analyzing team...")

		// Analyze team members with timeout
		results, err := b.researcher.AnalyzeTeamMembers(ctx, members)
		if err != nil {
			if err == context.DeadlineExceeded {
				s.ChannelMessageEdit(m.ChannelID, initialMsg.ID, teamList+"\n⏱️ Analysis timeout - exceeded 5 minute limit")
			} else {
				s.ChannelMessageEdit(m.ChannelID, initialMsg.ID, teamList+fmt.Sprintf("\n❌ Analysis failed: %v", err))
			}
			return
		}

		// Send team analysis results
		b.sendTeamResults(s, m.ChannelID, results)
	}()
}

func (b *Bot) sendVerificationResults(s *discordgo.Session, channelID string, results []research.VerificationResult) {
	embed := &discordgo.MessageEmbed{
		Title:  "📊 Claim Verification Results",
		Color:  0x3498db,
		Fields: []*discordgo.MessageEmbedField{},
		Footer: &discordgo.MessageEmbedFooter{
			Text: time.Now().Format("Jan 02, 2006 15:04 MST"),
		},
	}

	validCount := 0
	rejectedCount := 0
	unknownCount := 0

	for _, result := range results {
		statusEmoji := "❓"
		switch result.Status {
		case research.StatusValid:
			statusEmoji = "✅"
			validCount++
		case research.StatusRejected:
			statusEmoji = "❌"
			rejectedCount++
		case research.StatusUnknown:
			statusEmoji = "❓"
			unknownCount++
		}

		fieldValue := fmt.Sprintf("%s **%s**\n%s", statusEmoji, result.Status, result.Evidence)
		if len(fieldValue) > 1024 {
			fieldValue = fieldValue[:1021] + "..."
		}

		claimName := result.Claim
		if len(claimName) > 256 {
			claimName = claimName[:253] + "..."
		}

		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:   claimName,
			Value:  fieldValue,
			Inline: false,
		})

		if len(embed.Fields) >= 25 {
			break
		}
	}

	// Set color based on results
	if validCount > rejectedCount {
		embed.Color = 0x00ff00 // Green
	} else if rejectedCount > validCount {
		embed.Color = 0xff0000 // Red
	} else {
		embed.Color = 0xffff00 // Yellow
	}

	// Add summary
	embed.Description = fmt.Sprintf("**Summary:** ✅ Valid: %d | ❌ Rejected: %d | ❓ Unknown: %d",
		validCount, rejectedCount, unknownCount)

	s.ChannelMessageSendEmbed(channelID, embed)
}

func (b *Bot) sendTeamResults(s *discordgo.Session, channelID string, results []research.TeamAnalysisResult) {
	embed := &discordgo.MessageEmbed{
		Title:  "👥 Team Analysis Results",
		Color:  0x9b59b6,
		Fields: []*discordgo.MessageEmbedField{},
		Footer: &discordgo.MessageEmbedFooter{
			Text: time.Now().Format("Jan 02, 2006 15:04 MST"),
		},
	}

	realCount := 0
	skilledCount := 0
	capableTeam := false

	for _, result := range results {
		statusIcons := ""
		if result.IsReal {
			statusIcons += "👤 "
			realCount++
		} else {
			statusIcons += "❌ "
		}

		if result.HasStatedSkills {
			statusIcons += "🎯 "
			skilledCount++
		} else {
			statusIcons += "⚠️ "
		}

		memberInfo := fmt.Sprintf("**%s**", result.Name)
		if result.Role != "" {
			memberInfo += fmt.Sprintf(" - %s", result.Role)
		}

		fieldValue := fmt.Sprintf("%s\n%s", statusIcons, result.Capability)
		if len(fieldValue) > 1024 {
			fieldValue = fieldValue[:1021] + "..."
		}

		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:   memberInfo,
			Value:  fieldValue,
			Inline: false,
		})

		if len(embed.Fields) >= 25 {
			break
		}
	}

	// Determine if team can collectively complete the task
	if len(results) > 0 && realCount == len(results) && skilledCount >= len(results)*3/4 {
		capableTeam = true
	}

	// Set color based on team capability
	if capableTeam {
		embed.Color = 0x00ff00 // Green
	} else if realCount >= len(results)/2 {
		embed.Color = 0xffff00 // Yellow
	} else {
		embed.Color = 0xff0000 // Red
	}

	teamCapability := "❌ Team unlikely to complete the proposed task"
	if capableTeam {
		teamCapability = "✅ Team appears capable of completing the proposed task"
	} else if realCount >= len(results)/2 && skilledCount >= len(results)/2 {
		teamCapability = "⚠️ Team may be capable but has some concerns"
	}

	// Add summary
	embed.Description = fmt.Sprintf("**Team Assessment:** %s\n**Real People:** %d/%d | **Has Skills:** %d/%d\n\n%s",
		teamCapability, realCount, len(results), skilledCount, len(results), teamCapability)

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
