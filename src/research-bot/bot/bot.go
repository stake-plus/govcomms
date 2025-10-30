package bot

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/stake-plus/govcomms/src/research-bot/components/claims"
	"github.com/stake-plus/govcomms/src/research-bot/components/teams"
	sharedconfig "github.com/stake-plus/govcomms/src/shared/config"
	shareddiscord "github.com/stake-plus/govcomms/src/shared/discord"
	sharedfsx "github.com/stake-plus/govcomms/src/shared/fsx"
	sharedgov "github.com/stake-plus/govcomms/src/shared/gov"
	"gorm.io/gorm"
)

type Bot struct {
	config         *sharedconfig.ResearchConfig
	db             *gorm.DB
	session        *discordgo.Session
	networkManager *sharedgov.NetworkManager
	refManager     *sharedgov.ReferendumManager
	claimsAnalyzer *claims.Analyzer
	teamsAnalyzer  *teams.Analyzer
	cancelFunc     context.CancelFunc
}

func New(cfg *sharedconfig.ResearchConfig, db *gorm.DB) (*Bot, error) {
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
		// Register only the research-specific slash commands
		if err := shareddiscord.RegisterSlashCommands(s, b.config.Base.GuildID,
			shareddiscord.CommandResearch,
			shareddiscord.CommandTeam,
		); err != nil {
			log.Printf("Failed to register slash commands: %v", err)
		}
	})

	// Handle slash command interactions
	b.session.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		if i.ApplicationCommandData().Name == "research" {
			b.handleResearchSlash(s, i)
		} else if i.ApplicationCommandData().Name == "team" {
			b.handleTeamSlash(s, i)
		}
	})
}

// URL helpers centralized in shared/discord

func (b *Bot) handleResearch(s *discordgo.Session, m *discordgo.MessageCreate) {
	if b.config.ResearchRoleID != "" && !shareddiscord.HasRole(s, b.config.GuildID, m.Author.ID, b.config.ResearchRoleID) {
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
		// Create context without timeout
		ctx, cancel := context.WithCancel(context.Background())
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
			s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Error extracting claims: %v", err))
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
					updatedContent += fmt.Sprintf("\n📌 Sources: %s", shareddiscord.FormatURLsNoEmbed(result.SourceURLs))
				}

				// Wrap any URLs in the evidence text
				updatedContent = shareddiscord.WrapURLsNoEmbed(updatedContent)

				s.ChannelMessageEdit(m.ChannelID, msg.ID, updatedContent)
			}
		}

		// Send summary message
		summaryMsg := fmt.Sprintf("\n📊 **Verification Complete**\n✅ Valid: %d | ❌ Rejected: %d | ❓ Unknown: %d",
			validCount, rejectedCount, unknownCount)
		s.ChannelMessageSend(m.ChannelID, summaryMsg)
	}()
}

func (b *Bot) handleResearchSlash(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Check role permissions first
	if b.config.ResearchRoleID != "" && !shareddiscord.HasRole(s, b.config.Base.GuildID, i.Member.User.ID, b.config.ResearchRoleID) {
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

	go func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		proposalContent, err := b.getProposalContent(network.Name, uint32(threadInfo.RefID))
		if err != nil {
			msg := "Proposal content not found. Please run /refresh first."
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Content: &msg,
			})
			return
		}

		topClaims, totalClaims, err := b.claimsAnalyzer.ExtractTopClaims(ctx, proposalContent)
		if err != nil {
			msg := fmt.Sprintf("Error extracting claims: %v", err)
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Content: &msg,
			})
			return
		}

		if len(topClaims) == 0 {
			msg := "No verifiable historical claims found in the proposal."
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Content: &msg,
			})
			return
		}

		// Send header message as the interaction response
		headerMsg := fmt.Sprintf("🔍 **Verifying Historical Claims for %s Referendum #%d**\n", network.Name, threadInfo.RefID)
		headerMsg += fmt.Sprintf("Found %d total historical claims, verifying top %d most important:\n", totalClaims, len(topClaims))
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: &headerMsg,
		})

		// Send claim messages as regular channel messages
		claimMessages := make(map[int]*discordgo.Message)
		for idx, claim := range topClaims {
			msgContent := fmt.Sprintf("**Claim %d:** %s\n⏳ *Verifying...*", idx+1, claim.Claim)
			msg, err := s.ChannelMessageSend(i.ChannelID, msgContent)
			if err == nil {
				claimMessages[idx] = msg
			}
			time.Sleep(100 * time.Millisecond)
		}

		results, err := b.claimsAnalyzer.VerifyClaims(ctx, topClaims)
		if err != nil && err != context.DeadlineExceeded {
			log.Printf("Error during verification: %v", err)
		}

		validCount := 0
		rejectedCount := 0
		unknownCount := 0

		for idx, result := range results {
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

			if msg, exists := claimMessages[idx]; exists {
				updatedContent := fmt.Sprintf("**Claim %d:** %s\n%s **%s** - %s",
					idx+1,
					topClaims[idx].Claim,
					statusEmoji,
					result.Status,
					result.Evidence)

				if len(result.SourceURLs) > 0 {
					updatedContent += fmt.Sprintf("\n📌 Sources: %s", shareddiscord.FormatURLsNoEmbed(result.SourceURLs))
				}

				updatedContent = shareddiscord.WrapURLsNoEmbed(updatedContent)
				s.ChannelMessageEdit(i.ChannelID, msg.ID, updatedContent)
			}
		}

		summaryMsg := fmt.Sprintf("\n📊 **Verification Complete**\n✅ Valid: %d | ❌ Rejected: %d | ❓ Unknown: %d",
			validCount, rejectedCount, unknownCount)
		s.ChannelMessageSend(i.ChannelID, summaryMsg)
	}()
}

func (b *Bot) handleTeamSlash(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Check role permissions first
	if b.config.ResearchRoleID != "" && !shareddiscord.HasRole(s, b.config.Base.GuildID, i.Member.User.ID, b.config.ResearchRoleID) {
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

	go func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		proposalContent, err := b.getProposalContent(network.Name, uint32(threadInfo.RefID))
		if err != nil {
			msg := "Proposal content not found. Please run /refresh first."
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Content: &msg,
			})
			return
		}

		members, err := b.teamsAnalyzer.ExtractTeamMembers(ctx, proposalContent)
		if err != nil {
			msg := fmt.Sprintf("Error extracting team members: %v", err)
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Content: &msg,
			})
			return
		}

		if len(members) == 0 {
			msg := "No team members found in the proposal."
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Content: &msg,
			})
			return
		}

		// Analyze all members (returns TeamAnalysisResult slice)
		results, err := b.teamsAnalyzer.AnalyzeTeamMembers(ctx, members)
		if err != nil {
			msg := fmt.Sprintf("Error analyzing team members: %v", err)
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Content: &msg,
			})
			return
		}

		// Send initial placeholder messages
		claimMessages := make(map[int]*discordgo.Message)
		for idx := range members {
			msgContent := fmt.Sprintf("**Member %d:** %s\n⏳ *Analyzing...*", idx+1, members[idx].Name)
			msg, err := s.ChannelMessageSend(i.ChannelID, msgContent)
			if err == nil {
				claimMessages[idx] = msg
			}
			time.Sleep(100 * time.Millisecond)
		}

		// Update messages with results
		for idx, result := range results {
			if msg, exists := claimMessages[idx]; exists {
				updatedContent := fmt.Sprintf("**Member %d:** %s\n", idx+1, result.Name)
				if result.Role != "" {
					updatedContent += fmt.Sprintf("**Role:** %s\n", result.Role)
				}
				if result.Capability != "" {
					updatedContent += fmt.Sprintf("**Assessment:** %s\n", result.Capability)
				}
				if len(result.VerifiedURLs) > 0 {
					updatedContent += fmt.Sprintf("**Verified URLs:** %s\n", strings.Join(result.VerifiedURLs, ", "))
				}
				if result.IsReal {
					updatedContent += "✅ **Verified Real Person**\n"
				} else {
					updatedContent += "❓ **Verification Failed**\n"
				}
				if result.HasStatedSkills {
					updatedContent += "✅ **Has Stated Skills**\n"
				}

				s.ChannelMessageEdit(i.ChannelID, msg.ID, updatedContent)
			}
		}

		summaryMsg := "\n📊 **Team Analysis Complete**\n"
		s.ChannelMessageSend(i.ChannelID, summaryMsg)
	}()
}

func (b *Bot) handleTeam(s *discordgo.Session, m *discordgo.MessageCreate) {
	if b.config.ResearchRoleID != "" && !shareddiscord.HasRole(s, b.config.Base.GuildID, m.Author.ID, b.config.ResearchRoleID) {
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
		// Create context without timeout
		ctx, cancel := context.WithCancel(context.Background())
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
			s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Error extracting team members: %v", err))
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
					updatedContent += fmt.Sprintf("\n📌 Verified profiles: %s", shareddiscord.FormatURLsNoEmbed(result.VerifiedURLs))
				}

				// Wrap any URLs in the entire message
				updatedContent = shareddiscord.WrapURLsNoEmbed(updatedContent)

				s.ChannelMessageEdit(m.ChannelID, msg.ID, updatedContent)
			}
		}

		// Determine team capability
		teamAssessment := "❌ Team unlikely to complete the proposed task"

		if len(results) > 0 && realCount == len(results) && skilledCount >= len(results)*3/4 {
			teamAssessment = "✅ Team appears capable of completing the proposed task"
		} else if realCount >= len(results)/2 && skilledCount >= len(results)/2 {
			teamAssessment = "⚠️ Team may be capable but has some concerns"
		}

		// Send summary message
		summaryMsg := "\n📊 **Team Analysis Complete**\n"
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
	return sharedfsx.ProposalCachePath(b.config.TempDir, network, refID)
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
