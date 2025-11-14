package team

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/stake-plus/govcomms/src/actions/research/components/teams"
	cache "github.com/stake-plus/govcomms/src/cache"
	sharedconfig "github.com/stake-plus/govcomms/src/shared/config"
	shareddiscord "github.com/stake-plus/govcomms/src/shared/discord"
	sharedgov "github.com/stake-plus/govcomms/src/shared/gov"
)

// Handler encapsulates /team logic for both legacy and slash commands.
type Handler struct {
	Config         *sharedconfig.ResearchConfig
	Cache          *cache.Manager
	NetworkManager *sharedgov.NetworkManager
	RefManager     *sharedgov.ReferendumManager
	TeamsAnalyzer  *teams.Analyzer
}

// HandleMessage processes the message-based team command.
func (h *Handler) HandleMessage(s *discordgo.Session, m *discordgo.MessageCreate) {
	if h == nil {
		return
	}

	if h.Config.ResearchRoleID != "" && !shareddiscord.HasRole(s, h.Config.Base.GuildID, m.Author.ID, h.Config.ResearchRoleID) {
		s.ChannelMessageSend(m.ChannelID, "You don't have permission to use this command.")
		return
	}

	threadInfo, err := h.RefManager.FindThread(m.ChannelID)
	if err != nil || threadInfo == nil {
		s.ChannelMessageSend(m.ChannelID, "This command must be used in a referendum thread.")
		return
	}

	network := h.NetworkManager.GetByID(threadInfo.NetworkID)
	if network == nil {
		s.ChannelMessageSend(m.ChannelID, "Failed to identify network.")
		return
	}

	s.ChannelTyping(m.ChannelID)

	go h.runTeamWorkflow(s, m.ChannelID, network.Name, uint32(threadInfo.RefID))
}

// HandleSlash processes the /team slash command.
func (h *Handler) HandleSlash(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if h == nil {
		return
	}

	if h.Config.ResearchRoleID != "" && !shareddiscord.HasRole(s, h.Config.Base.GuildID, i.Member.User.ID, h.Config.ResearchRoleID) {
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
		log.Printf("team: slash ack failed: %v", err)
		return
	}

	threadInfo, err := h.RefManager.FindThread(i.ChannelID)
	if err != nil || threadInfo == nil {
		msg := "This command must be used in a referendum thread."
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: &msg,
		})
		return
	}

	network := h.NetworkManager.GetByID(threadInfo.NetworkID)
	if network == nil {
		msg := "Failed to identify network."
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: &msg,
		})
		return
	}

	go h.runTeamWorkflowSlash(s, i, network.Name, uint32(threadInfo.RefID))
}

func (h *Handler) runTeamWorkflow(s *discordgo.Session, channelID string, network string, refID uint32) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	proposalContent, err := h.Cache.GetProposalContent(network, refID)
	if err != nil {
		s.ChannelMessageSend(channelID, "Proposal content not found. Please run !refresh first.")
		return
	}

	members, err := h.TeamsAnalyzer.ExtractTeamMembers(ctx, proposalContent)
	if err != nil {
		s.ChannelMessageSend(channelID, fmt.Sprintf("Error extracting team members: %v", err))
		return
	}

	if len(members) == 0 {
		s.ChannelMessageSend(channelID, "No team members found in the proposal.")
		return
	}

	headerMsg := fmt.Sprintf("👥 **Team Analysis for %s Referendum #%d**\n", network, refID)
	headerMsg += fmt.Sprintf("Found %d team members to analyze:\n", len(members))
	s.ChannelMessageSend(channelID, headerMsg)

	memberMessages := make(map[int]*discordgo.Message)
	for i, member := range members {
		msgContent := fmt.Sprintf("**Member %d:** %s", i+1, member.Name)
		if member.Role != "" {
			msgContent += fmt.Sprintf(" (%s)", member.Role)
		}
		msgContent += "\n⏳ *Analyzing...*"

		msg, err := s.ChannelMessageSend(channelID, msgContent)
		if err == nil {
			memberMessages[i] = msg
		}
		time.Sleep(100 * time.Millisecond)
	}

	results, err := h.TeamsAnalyzer.AnalyzeTeamMembers(ctx, members)
	if err != nil && err != context.DeadlineExceeded {
		log.Printf("team: analysis error: %v", err)
	}

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

		if msg, exists := memberMessages[i]; exists {
			updatedContent := fmt.Sprintf("**Member %d:** %s", i+1, result.Name)
			if result.Role != "" {
				updatedContent += fmt.Sprintf(" (%s)", result.Role)
			}
			updatedContent += fmt.Sprintf("\n%s\n💼 %s", statusIcons, result.Capability)

			if len(result.VerifiedURLs) > 0 {
				updatedContent += fmt.Sprintf("\n📌 Verified profiles: %s", shareddiscord.FormatURLsNoEmbed(result.VerifiedURLs))
			}

			updatedContent = shareddiscord.WrapURLsNoEmbed(updatedContent)
			s.ChannelMessageEdit(channelID, msg.ID, updatedContent)
		}
	}

	teamAssessment := "❌ Team unlikely to complete the proposed task"
	if len(results) > 0 && realCount == len(results) && skilledCount >= len(results)*3/4 {
		teamAssessment = "✅ Team appears capable of completing the proposed task"
	} else if realCount >= len(results)/2 && skilledCount >= len(results)/2 {
		teamAssessment = "⚠️ Team may be capable but has some concerns"
	}

	summaryMsg := "\n📊 **Team Analysis Complete**\n"
	summaryMsg += fmt.Sprintf("👤 Real People: %d/%d | 🎯 Verified Skills: %d/%d\n",
		realCount, len(results), skilledCount, len(results))
	summaryMsg += fmt.Sprintf("**Assessment:** %s", teamAssessment)

	s.ChannelMessageSend(channelID, summaryMsg)
}

func (h *Handler) runTeamWorkflowSlash(s *discordgo.Session, i *discordgo.InteractionCreate, network string, refID uint32) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	proposalContent, err := h.Cache.GetProposalContent(network, refID)
	if err != nil {
		msg := "Proposal content not found. Please run /refresh first."
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: &msg,
		})
		return
	}

	members, err := h.TeamsAnalyzer.ExtractTeamMembers(ctx, proposalContent)
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

	results, err := h.TeamsAnalyzer.AnalyzeTeamMembers(ctx, members)
	if err != nil {
		msg := fmt.Sprintf("Error analyzing team members: %v", err)
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: &msg,
		})
		return
	}

	claimMessages := make(map[int]*discordgo.Message)
	for idx := range members {
		msgContent := fmt.Sprintf("**Member %d:** %s\n⏳ *Analyzing...*", idx+1, members[idx].Name)
		msg, err := s.ChannelMessageSend(i.ChannelID, msgContent)
		if err == nil {
			claimMessages[idx] = msg
		}
		time.Sleep(100 * time.Millisecond)
	}

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
}
