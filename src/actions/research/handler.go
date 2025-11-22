package research

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/stake-plus/govcomms/src/actions/research/components/claims"
	aicore "github.com/stake-plus/govcomms/src/ai/core"
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

	if h.Config.ResearchRoleID != "" && (i.Member == nil || i.Member.User == nil || !shareddiscord.HasRole(s, h.Config.Base.GuildID, i.Member.User.ID, h.Config.ResearchRoleID)) {
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

	headerTitle := fmt.Sprintf("Claim Verification • %s #%d", network, refID)
	intro := fmt.Sprintf("Found %d total historical claims, verifying top %d most important.", totalClaims, len(topClaims))
	headerBody := fmt.Sprintf("%s\n\n%s", h.providerInfo(), intro)
	headerHandle, err := shareddiscord.SendStyledHeaderMessage(s, channelID, headerTitle, headerBody)
	if err != nil {
		log.Printf("research: header send failed: %v", err)
	}

	results, err := h.ClaimsAnalyzer.VerifyClaims(ctx, topClaims)
	if err != nil && err != context.DeadlineExceeded {
		log.Printf("research: verification error: %v", err)
	}

	validCount := 0
	rejectedCount := 0
	unknownCount := 0

	var claimPanels []shareddiscord.StyledMessage
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

		title := fmt.Sprintf("Claim: %s", topClaims[i].Claim)
		assessmentText := strings.TrimSpace(result.Evidence)
		if assessmentText == "" {
			assessmentText = "_No assessment provided_"
		}
		body := fmt.Sprintf("Assessment:\n\n%s\n\nVerification Status: %s %s",
			indentMultilineText(assessmentText, "    "),
			statusEmoji,
			result.Status)

		panel := shareddiscord.BuildStyledMessage(title, body)
		claimPanels = append(claimPanels, panel)
	}

	summaryMsg := fmt.Sprintf("✅ Valid: %d\n❌ Rejected: %d\n❓ Unknown: %d", validCount, rejectedCount, unknownCount)
	finalHeaderBody := fmt.Sprintf("%s\n\n%s", headerBody, summaryMsg)
	if headerHandle != nil {
		if err := headerHandle.Update(s, headerTitle, finalHeaderBody); err != nil {
			log.Printf("research: header update failed: %v", err)
			sendStyledMessage(s, channelID, headerTitle, finalHeaderBody)
		}
	} else {
		sendStyledMessage(s, channelID, headerTitle, finalHeaderBody)
	}

	dispatchPanels(s, channelID, claimPanels, "research")
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

	headerTitle := fmt.Sprintf("Claim Verification • %s #%d", network, refID)
	intro := fmt.Sprintf("Found %d total historical claims, verifying top %d most important.", totalClaims, len(topClaims))
	headerBody := fmt.Sprintf("%s\n\n%s", h.providerInfo(), intro)
	headerHandle, err := shareddiscord.RespondStyledHeaderMessage(s, i.Interaction, headerTitle, headerBody)
	if err != nil {
		log.Printf("research: slash header send failed: %v", err)
		return
	}

	results, err := h.ClaimsAnalyzer.VerifyClaims(ctx, topClaims)
	if err != nil && err != context.DeadlineExceeded {
		log.Printf("research: verification error: %v", err)
	}

	validCount := 0
	rejectedCount := 0
	unknownCount := 0

	var claimPanels []shareddiscord.StyledMessage
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

		title := fmt.Sprintf("Claim: %s", topClaims[idx].Claim)
		assessmentText := strings.TrimSpace(result.Evidence)
		if assessmentText == "" {
			assessmentText = "_No assessment provided_"
		}
		body := fmt.Sprintf("Assessment:\n\n%s\n\nVerification Status: %s %s",
			indentMultilineText(assessmentText, "    "),
			statusEmoji,
			result.Status)

		panel := shareddiscord.BuildStyledMessage(title, body)
		claimPanels = append(claimPanels, panel)
	}

	summaryMsg := fmt.Sprintf("✅ Valid: %d\n❌ Rejected: %d\n❓ Unknown: %d", validCount, rejectedCount, unknownCount)
	finalHeaderBody := fmt.Sprintf("%s\n\n%s", headerBody, summaryMsg)
	if headerHandle != nil {
		if err := headerHandle.Update(s, headerTitle, finalHeaderBody); err != nil {
			log.Printf("research: slash header update failed: %v", err)
			sendStyledMessage(s, i.ChannelID, headerTitle, finalHeaderBody)
		}
	} else {
		sendStyledMessage(s, i.ChannelID, headerTitle, finalHeaderBody)
	}

	dispatchPanels(s, i.ChannelID, claimPanels, "research")
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

func dispatchPanels(s *discordgo.Session, channelID string, panels []shareddiscord.StyledMessage, prefix string) {
	if len(panels) == 0 {
		return
	}

	for _, panel := range panels {
		msg := &discordgo.MessageSend{
			Content: panel.Content,
		}
		if len(panel.Components) > 0 {
			msg.Components = panel.Components
		}
		if _, err := shareddiscord.SendComplexMessageNoEmbed(s, channelID, msg); err != nil {
			log.Printf("%s: panel send failed: %v", prefix, err)
			return
		}
	}
}

func indentMultilineText(text string, indent string) string {
	if text == "" {
		return ""
	}

	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			lines[i] = ""
			continue
		}
		lines[i] = indent + line
	}
	return strings.Join(lines, "\n")
}

func (h *Handler) providerInfo() string {
	if h == nil || h.Config == nil {
		return formatProviderInfo("", "")
	}
	return formatProviderInfo(h.Config.AIConfig.AIProvider, h.Config.AIConfig.AIModel)
}

func formatProviderInfo(provider, model string) string {
	providerInfo, ok := aicore.GetProviderInfo(provider)
	if !ok {
		providerInfo = aicore.ProviderInfo{
			Company: "unknown",
			Website: "unknown",
			Model:   "unknown",
		}
	}
	resolved := strings.TrimSpace(aicore.ResolveModelName(provider, model))
	if resolved == "" {
		resolved = providerInfo.Model
	}
	if resolved == "" {
		resolved = "unknown"
	}
	return fmt.Sprintf("Provider: %s    Model: %s    Website: %s", providerInfo.Company, resolved, providerInfo.Website)
}
