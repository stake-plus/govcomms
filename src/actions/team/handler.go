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
	sharedconfig "github.com/stake-plus/govcomms/src/config"
	shareddiscord "github.com/stake-plus/govcomms/src/discord"
	sharedgov "github.com/stake-plus/govcomms/src/polkadot-go/governance"
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
		sendTeamStyledMessage(s, m.ChannelID, "Team", "You don't have permission to use this command.")
		return
	}

	threadInfo, err := h.RefManager.FindThread(m.ChannelID)
	if err != nil || threadInfo == nil {
		sendTeamStyledMessage(s, m.ChannelID, "Team", "This command must be used in a referendum thread.")
		return
	}

	network := h.NetworkManager.GetByID(threadInfo.NetworkID)
	if network == nil {
		sendTeamStyledMessage(s, m.ChannelID, "Team", "Failed to identify network.")
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
		formatted := shareddiscord.FormatStyledBlock("Team", "You don't have permission to use this command.")
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
		log.Printf("team: slash ack failed: %v", err)
		return
	}

	threadInfo, err := h.RefManager.FindThread(i.ChannelID)
	if err != nil || threadInfo == nil {
		sendTeamWebhookEdit(s, i.Interaction, "Team", "This command must be used in a referendum thread.")
		return
	}

	network := h.NetworkManager.GetByID(threadInfo.NetworkID)
	if network == nil {
		sendTeamWebhookEdit(s, i.Interaction, "Team", "Failed to identify network.")
		return
	}

	go h.runTeamWorkflowSlash(s, i, network.Name, uint32(threadInfo.RefID))
}

func (h *Handler) runTeamWorkflow(s *discordgo.Session, channelID string, network string, refID uint32) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	proposalContent, err := h.Cache.GetProposalContent(network, refID)
	if err != nil {
		sendTeamStyledMessage(s, channelID, "Team", "Proposal content not found. Please run !refresh first.")
		return
	}

	members, err := h.TeamsAnalyzer.ExtractTeamMembers(ctx, proposalContent)
	if err != nil {
		sendTeamStyledMessage(s, channelID, "Team", fmt.Sprintf("Error extracting team members: %v", err))
		return
	}

	if len(members) == 0 {
		sendTeamStyledMessage(s, channelID, "Team", "No team members found in the proposal.")
		return
	}

	headerBody := fmt.Sprintf("Found %d team members to analyze for %s referendum #%d.", len(members), network, refID)
	sendTeamStyledMessage(s, channelID, "Team Analysis", headerBody)

	memberMessages := make(map[int]*discordgo.Message)
	for i, member := range members {
		memberBody := member.Name
		if member.Role != "" {
			memberBody += fmt.Sprintf(" (%s)", member.Role)
		}
		memberBody += "\n⏳ *Analyzing...*"

		msgContent := shareddiscord.FormatStyledBlock(fmt.Sprintf("Member %d", i+1), memberBody)
		msg, err := shareddiscord.SendMessageNoEmbed(s, channelID, msgContent)
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
			var sections []string
			header := result.Name
			if result.Role != "" {
				header += fmt.Sprintf(" (%s)", result.Role)
			}
			sections = append(sections, header)
			sections = append(sections, statusIcons)
			if strings.TrimSpace(result.Capability) != "" {
				sections = append(sections, fmt.Sprintf("💼 %s", result.Capability))
			}
			if urls := shareddiscord.FormatURLsNoEmbedMultiline(result.VerifiedURLs); urls != "" {
				sections = append(sections, urls)
			}

			body := strings.Join(sections, "\n\n")
			payload := shareddiscord.BuildStyledMessage(fmt.Sprintf("Member %d", i+1), body)
			edit := &discordgo.MessageEdit{
				ID:      msg.ID,
				Channel: channelID,
				Content: &payload.Content,
			}
			if len(payload.Components) > 0 {
				components := payload.Components
				edit.Components = &components
			}
			shareddiscord.EditMessageComplexNoEmbed(s, edit)
		}
	}

	teamAssessment := "❌ Team unlikely to complete the proposed task"
	if len(results) > 0 && realCount == len(results) && skilledCount >= len(results)*3/4 {
		teamAssessment = "✅ Team appears capable of completing the proposed task"
	} else if realCount >= len(results)/2 && skilledCount >= len(results)/2 {
		teamAssessment = "⚠️ Team may be capable but has some concerns"
	}

	summaryBody := fmt.Sprintf("👤 Real People: %d/%d | 🎯 Verified Skills: %d/%d\n\n**Assessment:** %s",
		realCount, len(results), skilledCount, len(results), teamAssessment)
	sendTeamStyledMessage(s, channelID, "Team Analysis Complete", summaryBody)
}

func (h *Handler) runTeamWorkflowSlash(s *discordgo.Session, i *discordgo.InteractionCreate, network string, refID uint32) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	proposalContent, err := h.Cache.GetProposalContent(network, refID)
	if err != nil {
		sendTeamWebhookEdit(s, i.Interaction, "Team", "Proposal content not found. Please run /refresh first.")
		return
	}

	members, err := h.TeamsAnalyzer.ExtractTeamMembers(ctx, proposalContent)
	if err != nil {
		sendTeamWebhookEdit(s, i.Interaction, "Team", fmt.Sprintf("Error extracting team members: %v", err))
		return
	}

	if len(members) == 0 {
		sendTeamWebhookEdit(s, i.Interaction, "Team", "No team members found in the proposal.")
		return
	}

	results, err := h.TeamsAnalyzer.AnalyzeTeamMembers(ctx, members)
	if err != nil {
		sendTeamWebhookEdit(s, i.Interaction, "Team", fmt.Sprintf("Error analyzing team members: %v", err))
		return
	}

	claimMessages := make(map[int]*discordgo.Message)
	for idx := range members {
		memberBody := fmt.Sprintf("%s\n⏳ *Analyzing...*", members[idx].Name)
		msgContent := shareddiscord.FormatStyledBlock(fmt.Sprintf("Member %d", idx+1), memberBody)
		msg, err := shareddiscord.SendMessageNoEmbed(s, i.ChannelID, msgContent)
		if err == nil {
			claimMessages[idx] = msg
		}
		time.Sleep(100 * time.Millisecond)
	}

	realCount := 0
	skilledCount := 0

	for idx, result := range results {
		if result.IsReal {
			realCount++
		}
		if result.HasStatedSkills {
			skilledCount++
		}

		if msg, exists := claimMessages[idx]; exists {
			var sections []string
			header := result.Name
			if result.Role != "" {
				header += fmt.Sprintf(" (%s)", result.Role)
			}
			sections = append(sections, header)

			if strings.TrimSpace(result.Capability) != "" {
				sections = append(sections, fmt.Sprintf("Assessment: %s", result.Capability))
			}

			status := ""
			if result.IsReal {
				status += "👤 Verified Real Person"
			} else {
				status += "❓ Verification Failed"
			}
			if result.HasStatedSkills {
				status += " • 🎯 Has Stated Skills"
			}
			if status != "" {
				sections = append(sections, status)
			}

			if urls := shareddiscord.FormatURLsNoEmbedMultiline(result.VerifiedURLs); urls != "" {
				sections = append(sections, urls)
			}

			body := strings.Join(sections, "\n\n")
			payload := shareddiscord.BuildStyledMessage(fmt.Sprintf("Member %d", idx+1), body)
			edit := &discordgo.MessageEdit{
				ID:      msg.ID,
				Channel: i.ChannelID,
				Content: &payload.Content,
			}
			if len(payload.Components) > 0 {
				components := payload.Components
				edit.Components = &components
			}
			shareddiscord.EditMessageComplexNoEmbed(s, edit)
		}
	}

	teamAssessment := "❌ Team unlikely to complete the proposed task"
	if len(results) > 0 && realCount == len(results) && skilledCount >= len(results)*3/4 {
		teamAssessment = "✅ Team appears capable of completing the proposed task"
	} else if realCount >= len(results)/2 && skilledCount >= len(results)/2 {
		teamAssessment = "⚠️ Team may be capable but has some concerns"
	}

	summaryBody := fmt.Sprintf("👤 Real People: %d/%d | 🎯 Verified Skills: %d/%d\n\n**Assessment:** %s",
		realCount, len(results), skilledCount, len(results), teamAssessment)
	sendTeamStyledMessage(s, i.ChannelID, "Team Analysis Complete", summaryBody)
}

func sendTeamStyledMessage(s *discordgo.Session, channelID, title, body string) {
	payloads := shareddiscord.BuildStyledMessages(title, body, "")
	if len(payloads) == 0 {
		return
	}
	for _, payload := range payloads {
		msg := &discordgo.MessageSend{
			Content: payload.Content,
		}
		if len(payload.Components) > 0 {
			msg.Components = payload.Components
		}
		if _, err := shareddiscord.SendComplexMessageNoEmbed(s, channelID, msg); err != nil {
			log.Printf("team: send failed: %v", err)
			return
		}
	}
}

func sendTeamWebhookEdit(s *discordgo.Session, interaction *discordgo.Interaction, title, body string) {
	payload := shareddiscord.BuildStyledMessage(title, body)
	edit := &discordgo.WebhookEdit{
		Content: &payload.Content,
	}
	if len(payload.Components) > 0 {
		components := payload.Components
		edit.Components = &components
	}
	shareddiscord.InteractionResponseEditNoEmbed(s, interaction, edit)
}
