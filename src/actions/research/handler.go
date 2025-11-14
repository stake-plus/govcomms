package research

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/stake-plus/govcomms/src/actions/research/components/claims"
	cache "github.com/stake-plus/govcomms/src/cache"
	sharedconfig "github.com/stake-plus/govcomms/src/config"
	shareddiscord "github.com/stake-plus/govcomms/src/discord"
	sharedgov "github.com/stake-plus/govcomms/src/polkadot-go/governance"
)

// Handler manages the research action logic.
type Handler struct {
	Config         *sharedconfig.ResearchConfig
	Cache          *cache.Manager
	NetworkManager *sharedgov.NetworkManager
	RefManager     *sharedgov.ReferendumManager
	ClaimsAnalyzer *claims.Analyzer
}

// HandleMessage processes the legacy message-based research command.
func (h *Handler) HandleMessage(s *discordgo.Session, m *discordgo.MessageCreate) {
	if h == nil {
		return
	}

	if h.Config.ResearchRoleID != "" && !shareddiscord.HasRole(s, h.Config.GuildID, m.Author.ID, h.Config.ResearchRoleID) {
		sendStyledMessage(s, m.ChannelID, "Research", "You don't have permission to use this command.")
		return
	}

	threadInfo, err := h.RefManager.FindThread(m.ChannelID)
	if err != nil || threadInfo == nil {
		sendStyledMessage(s, m.ChannelID, "Research", "This command must be used in a referendum thread.")
		return
	}

	network := h.NetworkManager.GetByID(threadInfo.NetworkID)
	if network == nil {
		sendStyledMessage(s, m.ChannelID, "Research", "Failed to identify network.")
		return
	}

	s.ChannelTyping(m.ChannelID)

	go h.runResearchWorkflow(s, m.ChannelID, network.Name, uint32(threadInfo.RefID))
}

// HandleSlash processes the /research action.
func (h *Handler) HandleSlash(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if h == nil {
		return
	}

	if h.Config.ResearchRoleID != "" && !shareddiscord.HasRole(s, h.Config.Base.GuildID, i.Member.User.ID, h.Config.ResearchRoleID) {
		formatted := shareddiscord.FormatStyledBlock("Research", "You don't have permission to use this command.")
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
		log.Printf("research: slash ack failed: %v", err)
		return
	}

	threadInfo, err := h.RefManager.FindThread(i.ChannelID)
	if err != nil || threadInfo == nil {
		formatted := shareddiscord.FormatStyledBlock("Research", "This command must be used in a referendum thread.")
		shareddiscord.InteractionResponseEditNoEmbed(s, i.Interaction, &discordgo.WebhookEdit{
			Content: &formatted,
		})
		return
	}

	network := h.NetworkManager.GetByID(threadInfo.NetworkID)
	if network == nil {
		formatted := shareddiscord.FormatStyledBlock("Research", "Failed to identify network.")
		shareddiscord.InteractionResponseEditNoEmbed(s, i.Interaction, &discordgo.WebhookEdit{
			Content: &formatted,
		})
		return
	}

	go h.runResearchWorkflowSlash(s, i, network.Name, uint32(threadInfo.RefID))
}

func (h *Handler) runResearchWorkflow(s *discordgo.Session, channelID string, network string, refID uint32) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	proposalContent, err := h.Cache.GetProposalContent(network, refID)
	if err != nil {
		sendStyledMessage(s, channelID, "Research", "Proposal content not found. Please run !refresh first.")
		return
	}

	topClaims, totalClaims, err := h.ClaimsAnalyzer.ExtractTopClaims(ctx, proposalContent)
	if err != nil {
		sendStyledMessage(s, channelID, "Research", fmt.Sprintf("Error extracting claims: %v", err))
		return
	}

	if len(topClaims) == 0 {
		sendStyledMessage(s, channelID, "Research", "No verifiable historical claims found in the proposal.")
		return
	}

	headerBody := fmt.Sprintf("Found %d total historical claims, verifying top %d most important.", totalClaims, len(topClaims))
	sendStyledMessage(s, channelID, fmt.Sprintf("Claim Verification • %s #%d", network, refID), headerBody)

	claimMessages := make(map[int]*discordgo.Message)
	for i, claim := range topClaims {
		msgContent := shareddiscord.FormatStyledBlock(fmt.Sprintf("Claim %d", i+1), fmt.Sprintf("%s\n\n⏳ *Verifying...*", claim.Claim))
		msg, err := shareddiscord.SendMessageNoEmbed(s, channelID, msgContent)
		if err == nil {
			claimMessages[i] = msg
		}
		time.Sleep(100 * time.Millisecond)
	}

	results, err := h.ClaimsAnalyzer.VerifyClaims(ctx, topClaims)
	if err != nil && err != context.DeadlineExceeded {
		log.Printf("research: verification error: %v", err)
	}

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

		if msg, exists := claimMessages[i]; exists {
			body := fmt.Sprintf("%s\n\n%s **%s** - %s",
				topClaims[i].Claim,
				statusEmoji,
				result.Status,
				result.Evidence)

			if urls := shareddiscord.FormatURLsNoEmbedMultiline(result.SourceURLs); urls != "" {
				body += "\n\n" + urls
			}
			editStyledMessage(s, channelID, msg.ID, fmt.Sprintf("Claim %d", i+1), body)
		}
	}

	summaryMsg := fmt.Sprintf("✅ Valid: %d\n❌ Rejected: %d\n❓ Unknown: %d", validCount, rejectedCount, unknownCount)
	sendStyledMessage(s, channelID, "Claim Verification Complete", summaryMsg)
}

func (h *Handler) runResearchWorkflowSlash(s *discordgo.Session, i *discordgo.InteractionCreate, network string, refID uint32) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	proposalContent, err := h.Cache.GetProposalContent(network, refID)
	if err != nil {
		formatted := shareddiscord.FormatStyledBlock("Research", "Proposal content not found. Please run /refresh first.")
		shareddiscord.InteractionResponseEditNoEmbed(s, i.Interaction, &discordgo.WebhookEdit{
			Content: &formatted,
		})
		return
	}

	topClaims, totalClaims, err := h.ClaimsAnalyzer.ExtractTopClaims(ctx, proposalContent)
	if err != nil {
		formatted := shareddiscord.FormatStyledBlock("Research", fmt.Sprintf("Error extracting claims: %v", err))
		shareddiscord.InteractionResponseEditNoEmbed(s, i.Interaction, &discordgo.WebhookEdit{
			Content: &formatted,
		})
		return
	}

	if len(topClaims) == 0 {
		formatted := shareddiscord.FormatStyledBlock("Research", "No verifiable historical claims found in the proposal.")
		shareddiscord.InteractionResponseEditNoEmbed(s, i.Interaction, &discordgo.WebhookEdit{
			Content: &formatted,
		})
		return
	}

	headerBody := fmt.Sprintf("Found %d total historical claims, verifying top %d most important.", totalClaims, len(topClaims))
	sendStyledSlashResponse(s, i, fmt.Sprintf("Claim Verification • %s #%d", network, refID), headerBody)

	claimMessages := make(map[int]*discordgo.Message)
	for idx, claim := range topClaims {
		msgContent := shareddiscord.FormatStyledBlock(fmt.Sprintf("Claim %d", idx+1), fmt.Sprintf("%s\n\n⏳ *Verifying...*", claim.Claim))
		msg, err := shareddiscord.SendMessageNoEmbed(s, i.ChannelID, msgContent)
		if err == nil {
			claimMessages[idx] = msg
		}
		time.Sleep(100 * time.Millisecond)
	}

	results, err := h.ClaimsAnalyzer.VerifyClaims(ctx, topClaims)
	if err != nil && err != context.DeadlineExceeded {
		log.Printf("research: verification error: %v", err)
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
			body := fmt.Sprintf("%s\n\n%s **%s** - %s",
				topClaims[idx].Claim,
				statusEmoji,
				result.Status,
				result.Evidence)

			if urls := shareddiscord.FormatURLsNoEmbedMultiline(result.SourceURLs); urls != "" {
				body += "\n\n" + urls
			}

			editStyledMessage(s, i.ChannelID, msg.ID, fmt.Sprintf("Claim %d", idx+1), body)
		}
	}

	summaryMsg := fmt.Sprintf("✅ Valid: %d\n❌ Rejected: %d\n❓ Unknown: %d", validCount, rejectedCount, unknownCount)
	sendStyledMessage(s, i.ChannelID, "Claim Verification Complete", summaryMsg)
}

func sendStyledMessage(s *discordgo.Session, channelID, title, body string) {
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
			log.Printf("research: send failed: %v", err)
			return
		}
	}
}

func sendStyledSlashResponse(s *discordgo.Session, i *discordgo.InteractionCreate, title, body string) {
	payloads := shareddiscord.BuildStyledMessages(title, body, "")
	if len(payloads) == 0 {
		empty := ""
		shareddiscord.InteractionResponseEditNoEmbed(s, i.Interaction, &discordgo.WebhookEdit{Content: &empty})
		return
	}

	first := payloads[0]
	edit := &discordgo.WebhookEdit{
		Content: &first.Content,
	}
	if len(first.Components) > 0 {
		components := first.Components
		edit.Components = &components
	}
	if _, err := shareddiscord.InteractionResponseEditNoEmbed(s, i.Interaction, edit); err != nil {
		log.Printf("research: slash response failed: %v", err)
		return
	}

	for _, payload := range payloads[1:] {
		msg := &discordgo.MessageSend{
			Content: payload.Content,
		}
		if len(payload.Components) > 0 {
			msg.Components = payload.Components
		}
		if _, err := shareddiscord.SendComplexMessageNoEmbed(s, i.ChannelID, msg); err != nil {
			log.Printf("research: follow-up send failed: %v", err)
			return
		}
	}
}

func editStyledMessage(s *discordgo.Session, channelID, messageID, title, body string) {
	payload := shareddiscord.BuildStyledMessage(title, body)
	edit := &discordgo.MessageEdit{
		ID:      messageID,
		Channel: channelID,
		Content: &payload.Content,
	}
	if len(payload.Components) > 0 {
		components := payload.Components
		edit.Components = &components
	}
	if _, err := shareddiscord.EditMessageComplexNoEmbed(s, edit); err != nil {
		log.Printf("research: edit failed: %v", err)
	}
}
