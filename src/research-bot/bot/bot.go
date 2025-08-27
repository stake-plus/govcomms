package bot

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/stake-plus/govcomms/src/research-bot/components/claims"
	"github.com/stake-plus/govcomms/src/research-bot/components/network"
	"github.com/stake-plus/govcomms/src/research-bot/components/referendum"
	"github.com/stake-plus/govcomms/src/research-bot/components/teams"
	"github.com/stake-plus/govcomms/src/research-bot/config"
	"gorm.io/gorm"
)

type Bot struct {
	config         *config.Config
	db             *gorm.DB
	session        *discordgo.Session
	networkManager *network.Manager
	refManager     *referendum.Manager
	claimsAnalyzer *claims.Analyzer
	teamsAnalyzer  *teams.Analyzer
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
	claimsAnalyzer := claims.NewAnalyzer(cfg.OpenAIKey)
	teamsAnalyzer := teams.NewAnalyzer(cfg.OpenAIKey)

	bot := &Bot{
		config:         cfg,
		db:             db,
		session:        session,
		networkManager: networkManager,
		refManager:     refManager,
		claimsAnalyzer: claimsAnalyzer,
		teamsAnalyzer:  teamsAnalyzer,
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

// formatURLsNoEmbed wraps URLs in angle brackets to prevent Discord embeds
func formatURLsNoEmbed(urls []string) string {
	if len(urls) == 0 {
		return ""
	}

	var formatted []string
	for _, url := range urls {
		formatted = append(formatted, fmt.Sprintf("<%s>", url))
	}
	return strings.Join(formatted, " ")
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

	go func() {
		// Create context with 10 minute timeout for entire operation
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		// Get proposal content
		proposalContent, err := b.getProposalContent(network.Name, uint32(threadInfo.RefID))
		if err != nil {
			s.ChannelMessageSend(m.ChannelID, "Proposal content not found. Please run !refresh first.")
			return
		}

		// Extract claims
		topClaims, totalClaims, err := b.claimsAnalyzer.ExtractTopClaims(ctx, proposalContent)
		if err != nil {
			if err == context.DeadlineExceeded {
				s.ChannelMessageSend(m.ChannelID, "⏱️ Verification timeout - process took longer than 10 minutes")
			} else {
				s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Error extracting claims: %v", err))
			}
			return
		}

		if len(topClaims) == 0 {
			s.ChannelMessageSend(m.ChannelID, "No verifiable historical claims found in the proposal.")
			return
		}

		// Send header message
		headerMsg := fmt.Sprintf("🔍 **Verifying Historical Claims for %s Referendum #%d**\n", network.Name, threadInfo.RefID)
		headerMsg += fmt.Sprintf("Found %d total historical claims, verifying top %d most important:\n", totalClaims, len(topClaims))
		s.ChannelMessageSend(m.ChannelID, headerMsg)

		// Create a message for each claim
		claimMessages := make(map[int]*discordgo.Message)
		for i, claim := range topClaims {
			msgContent := fmt.Sprintf("**Claim %d:** %s\n⏳ *Verifying...*", i+1, claim.Claim)
			msg, err := s.ChannelMessageSend(m.ChannelID, msgContent)
			if err == nil {
				claimMessages[i] = msg
			}
			time.Sleep(100 * time.Millisecond)
		}

		// Verify claims with proper batching and timeout handling
		results, err := b.claimsAnalyzer.VerifyClaims(ctx, topClaims)
		if err != nil && err != context.DeadlineExceeded {
			log.Printf("Error during verification: %v", err)
		}

		// Update messages with results
		validCount := 0
		rejectedCount := 0
		unknownCount := 0

		for i, result := range results {
			statusEmoji := "❓"
			switch result.Status {
			case claims.StatusValid:
				statusEmoji = "✅"
				validCount++
			case claims.StatusRejected:
				statusEmoji = "❌"
				rejectedCount++
			case claims.StatusUnknown:
				statusEmoji = "❓"
				unknownCount++
			}

			// Update the message
			if msg, exists := claimMessages[i]; exists {
				updatedContent := fmt.Sprintf("**Claim %d:** %s\n%s **%s** - %s",
					i+1,
					topClaims[i].Claim,
					statusEmoji,
					result.Status,
					result.Evidence)

				// Add source URLs if available (wrapped to prevent embeds)
				if len(result.SourceURLs) > 0 {
					updatedContent += fmt.Sprintf("\n📌 Sources: %s", formatURLsNoEmbed(result.SourceURLs))
				}

				s.ChannelMessageEdit(m.ChannelID, msg.ID, updatedContent)
			}
		}

		// Send summary message
		summaryMsg := fmt.Sprintf("\n📊 **Verification Complete**\n✅ Valid: %d | ❌ Rejected: %d | ❓ Unknown: %d",
			validCount, rejectedCount, unknownCount)
		s.ChannelMessageSend(m.ChannelID, summaryMsg)
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

	go func() {
		// Create context with 10 minute timeout for entire operation
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		// Get proposal content
		proposalContent, err := b.getProposalContent(network.Name, uint32(threadInfo.RefID))
		if err != nil {
			s.ChannelMessageSend(m.ChannelID, "Proposal content not found. Please run !refresh first.")
			return
		}

		// Extract team members
		members, err := b.teamsAnalyzer.ExtractTeamMembers(ctx, proposalContent)
		if err != nil {
			if err == context.DeadlineExceeded {
				s.ChannelMessageSend(m.ChannelID, "⏱️ Analysis timeout - process took longer than 10 minutes")
			} else {
				s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Error extracting team members: %v", err))
			}
			return
		}

		if len(members) == 0 {
			s.ChannelMessageSend(m.ChannelID, "No team members found in the proposal.")
			return
		}

		// Send header message
		headerMsg := fmt.Sprintf("👥 **Team Analysis for %s Referendum #%d**\n", network.Name, threadInfo.RefID)
		headerMsg += fmt.Sprintf("Found %d team members to analyze:\n", len(members))
		s.ChannelMessageSend(m.ChannelID, headerMsg)

		// Create a message for each team member
		memberMessages := make(map[int]*discordgo.Message)
		for i, member := range members {
			msgContent := fmt.Sprintf("**Member %d:** %s", i+1, member.Name)
			if member.Role != "" {
				msgContent += fmt.Sprintf(" (%s)", member.Role)
			}
			msgContent += "\n⏳ *Analyzing...*"

			msg, err := s.ChannelMessageSend(m.ChannelID, msgContent)
			if err == nil {
				memberMessages[i] = msg
			}
			time.Sleep(100 * time.Millisecond)
		}

		// Analyze team members with proper batching and timeout handling
		results, err := b.teamsAnalyzer.AnalyzeTeamMembers(ctx, members)
		if err != nil && err != context.DeadlineExceeded {
			log.Printf("Error during team analysis: %v", err)
		}

		// Update messages with results
		realCount := 0
		skilledCount := 0

		for i, result := range results {
			statusIcons := ""
			if result.IsReal {
				statusIcons += "👤 Real Person"
				realCount++
			} else {
				statusIcons += "❌ Not Verified"
			}

			if result.HasStatedSkills {
				statusIcons += " | 🎯 Skills Verified"
				skilledCount++
			} else {
				statusIcons += " | ⚠️ Skills Unverified"
			}

			// Update the message
			if msg, exists := memberMessages[i]; exists {
				updatedContent := fmt.Sprintf("**Member %d:** %s", i+1, result.Name)
				if result.Role != "" {
					updatedContent += fmt.Sprintf(" (%s)", result.Role)
				}
				updatedContent += fmt.Sprintf("\n%s\n💼 %s", statusIcons, result.Capability)

				// Add verified URLs if available (wrapped to prevent embeds)
				if len(result.VerifiedURLs) > 0 {
					updatedContent += fmt.Sprintf("\n📌 Verified profiles: %s", formatURLsNoEmbed(result.VerifiedURLs))
				}

				s.ChannelMessageEdit(m.ChannelID, msg.ID, updatedContent)
			}
		}

		teamAssessment := "❌ Team unlikely to complete the proposed task"

		if len(results) > 0 && realCount == len(results) && skilledCount >= len(results)*3/4 {
			teamAssessment = "✅ Team appears capable of completing the proposed task"
		} else if realCount >= len(results)/2 && skilledCount >= len(results)/2 {
			teamAssessment = "⚠️ Team may be capable but has some concerns"
		}

		// Send summary message
		summaryMsg := fmt.Sprintf("\n📊 **Team Analysis Complete**\n")
		summaryMsg += fmt.Sprintf("👤 Real People: %d/%d | 🎯 Verified Skills: %d/%d\n",
			realCount, len(results), skilledCount, len(results))
		summaryMsg += fmt.Sprintf("**Assessment:** %s", teamAssessment)

		s.ChannelMessageSend(m.ChannelID, summaryMsg)
	}()
}

func (b *Bot) getProposalContent(network string, refID uint32) (string, error) {
	cacheFile := b.getCacheFilePath(network, refID)
	content, err := os.ReadFile(cacheFile)
	if err != nil {
		return "", fmt.Errorf("proposal content not found")
	}
	return string(content), nil
}

func (b *Bot) getCacheFilePath(network string, refID uint32) string {
	hash := md5.Sum([]byte(fmt.Sprintf("%s-%d", network, refID)))
	filename := fmt.Sprintf("%s-%d-%s.txt", network, refID, hex.EncodeToString(hash[:8]))
	return filepath.Join(b.config.TempDir, filename)
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
