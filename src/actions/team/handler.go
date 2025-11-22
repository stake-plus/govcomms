package team

import (
	"context"
	"fmt"
	"log"
	"strings"

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

	if h.Config.ResearchRoleID != "" && (i.Member == nil || i.Member.User == nil || !shareddiscord.HasRole(s, h.Config.Base.GuildID, i.Member.User.ID, h.Config.ResearchRoleID)) {
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

	headerTitle := fmt.Sprintf("Team Analysis • %s #%d", network, refID)
	headerBody := fmt.Sprintf("Found %d team members to analyze for %s referendum #%d.", len(members), network, refID)
	headerHandle, err := shareddiscord.SendStyledHeaderMessage(s, channelID, headerTitle, headerBody)
	if err != nil {
		log.Printf("team: header send failed: %v", err)
	}

	results, err := h.TeamsAnalyzer.AnalyzeTeamMembers(ctx, members)
	if err != nil && err != context.DeadlineExceeded {
		log.Printf("team: analysis error: %v", err)
	}

	realCount := 0
	skilledCount := 0

	var memberPanels []shareddiscord.StyledMessage
	for _, result := range results {
		header := result.Name
		if result.Role != "" {
			header += fmt.Sprintf(" (%s)", result.Role)
		}

		nameLine := fmt.Sprintf("Name: %s", header)

		assessmentText := strings.TrimSpace(result.Capability)
		if assessmentText == "" {
			assessmentText = "_No assessment provided_"
		}
		assessmentLine := fmt.Sprintf("Assessment:\n\n%s", indentMultilineText(assessmentText, "    "))

		if result.IsReal {
			realCount++
		}
		if result.HasStatedSkills {
			skilledCount++
		}

		verificationStatus, verificationEmoji := determineVerificationDisplay(result)
		workSkillsStatus, workSkillsEmoji := determineWorkSkillsDisplay(result)
		verificationLine := fmt.Sprintf("Verification Status: %s %s", verificationEmoji, verificationStatus)
		workSkillsLine := fmt.Sprintf("Work Skills: %s %s", workSkillsEmoji, workSkillsStatus)

		infoBlock := fmt.Sprintf("%s\n%s", verificationLine, workSkillsLine)

		body := strings.Join([]string{nameLine, assessmentLine, infoBlock}, "\n\n")
		panel := shareddiscord.BuildStyledMessage("", body)
		if components := buildSourceButtons(result.VerifiedURLs); len(components) > 0 {
			panel.Components = components
		}
		memberPanels = append(memberPanels, panel)
	}

	teamAssessment := "❌ Team unlikely to complete the proposed task"
	if len(results) > 0 && realCount == len(results) && skilledCount >= len(results)*3/4 {
		teamAssessment = "✅ Team appears capable of completing the proposed task"
	} else if realCount >= len(results)/2 && skilledCount >= len(results)/2 {
		teamAssessment = "⚠️ Team may be capable but has some concerns"
	}

	summaryBody := fmt.Sprintf("👤 Real People: %d/%d | 🎯 Verified Skills: %d/%d\n\n**Assessment:** %s",
		realCount, len(results), skilledCount, len(results), teamAssessment)
	finalHeaderBody := headerBody + "\n\n" + summaryBody
	if headerHandle != nil {
		if err := headerHandle.Update(s, headerTitle, finalHeaderBody); err != nil {
			log.Printf("team: header update failed: %v", err)
			sendTeamStyledMessage(s, channelID, headerTitle, finalHeaderBody)
		}
	} else {
		sendTeamStyledMessage(s, channelID, headerTitle, finalHeaderBody)
	}

	dispatchPanels(s, channelID, memberPanels, "team")
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

	headerTitle := fmt.Sprintf("Team Analysis • %s #%d", network, refID)
	headerBody := fmt.Sprintf("Found %d team members to analyze for %s referendum #%d.", len(members), network, refID)
	headerHandle, err := shareddiscord.RespondStyledHeaderMessage(s, i.Interaction, headerTitle, headerBody)
	if err != nil {
		log.Printf("team: slash header send failed: %v", err)
		return
	}

	results, err := h.TeamsAnalyzer.AnalyzeTeamMembers(ctx, members)
	if err != nil {
		sendTeamWebhookEdit(s, i.Interaction, "Team", fmt.Sprintf("Error analyzing team members: %v", err))
		return
	}

	realCount := 0
	skilledCount := 0

	var memberPanels []shareddiscord.StyledMessage
	for _, result := range results {
		header := result.Name
		if result.Role != "" {
			header += fmt.Sprintf(" (%s)", result.Role)
		}

		nameLine := fmt.Sprintf("Name: %s", header)

		assessmentText := strings.TrimSpace(result.Capability)
		if assessmentText == "" {
			assessmentText = "_No assessment provided_"
		}
		assessmentLine := fmt.Sprintf("Assessment:\n\n%s", indentMultilineText(assessmentText, "    "))

		if result.IsReal {
			realCount++
		}
		if result.HasStatedSkills {
			skilledCount++
		}

		verificationStatus, verificationEmoji := determineVerificationDisplay(result)
		workSkillsStatus, workSkillsEmoji := determineWorkSkillsDisplay(result)
		verificationLine := fmt.Sprintf("Verification Status: %s %s", verificationEmoji, verificationStatus)
		workSkillsLine := fmt.Sprintf("Work Skills: %s %s", workSkillsEmoji, workSkillsStatus)

		infoBlock := fmt.Sprintf("%s\n%s", verificationLine, workSkillsLine)

		body := strings.Join([]string{nameLine, assessmentLine, infoBlock}, "\n\n")
		panel := shareddiscord.BuildStyledMessage("", body)
		if components := buildSourceButtons(result.VerifiedURLs); len(components) > 0 {
			panel.Components = components
		}
		memberPanels = append(memberPanels, panel)
	}

	teamAssessment := "❌ Team unlikely to complete the proposed task"
	if len(results) > 0 && realCount == len(results) && skilledCount >= len(results)*3/4 {
		teamAssessment = "✅ Team appears capable of completing the proposed task"
	} else if realCount >= len(results)/2 && skilledCount >= len(results)/2 {
		teamAssessment = "⚠️ Team may be capable but has some concerns"
	}

	finalHeaderBody := fmt.Sprintf("%s\n\n👤 Real People: %d/%d | 🎯 Verified Skills: %d/%d\n\n**Assessment:** %s",
		headerBody, realCount, len(results), skilledCount, len(results), teamAssessment)
	if headerHandle != nil {
		if err := headerHandle.Update(s, headerTitle, finalHeaderBody); err != nil {
			log.Printf("team: slash header update failed: %v", err)
			sendTeamStyledMessage(s, i.ChannelID, headerTitle, finalHeaderBody)
		}
	} else {
		sendTeamStyledMessage(s, i.ChannelID, headerTitle, finalHeaderBody)
	}

	dispatchPanels(s, i.ChannelID, memberPanels, "team")
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

func dispatchPanels(s *discordgo.Session, channelID string, panels []shareddiscord.StyledMessage, prefix string) {
	if len(panels) == 0 {
		return
	}

	for _, panel := range panels {
		msg := &discordgo.MessageSend{Content: panel.Content}
		if len(panel.Components) > 0 {
			msg.Components = panel.Components
		}
		if _, err := shareddiscord.SendComplexMessageNoEmbed(s, channelID, msg); err != nil {
			log.Printf("%s: panel send failed: %v", prefix, err)
			return
		}
	}
}

func determineVerificationDisplay(result teams.TeamAnalysisResult) (string, string) {
	if result.IsReal {
		return "Valid", "✅"
	}
	if isInconclusiveCapability(result.Capability) {
		return "Unknown", "❓"
	}
	return "Rejected", "❌"
}

func determineWorkSkillsDisplay(result teams.TeamAnalysisResult) (string, string) {
	if result.HasStatedSkills {
		return "Verified", "✅"
	}
	if isInconclusiveCapability(result.Capability) {
		return "Unknown", "❓"
	}
	return "Unverified", "⚠️"
}

func isInconclusiveCapability(capability string) bool {
	lowered := strings.ToLower(strings.TrimSpace(capability))
	if lowered == "" {
		return true
	}

	keywords := []string{
		"analysis timeout",
		"analysis timed out",
		"failed to analyze",
		"failed to analyse",
		"no response",
		"unable to assess",
		"unable to verify",
	}
	for _, keyword := range keywords {
		if strings.Contains(lowered, keyword) {
			return true
		}
	}
	return false
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

func buildSourceButtons(urls []string) []discordgo.MessageComponent {
	unique := dedupeVerifiedURLs(urls)
	if len(unique) == 0 {
		return nil
	}

	joined := strings.Join(unique, "\n")
	_, refs := shareddiscord.ReplaceURLsAndCollect(joined)
	if len(refs) == 0 {
		return nil
	}

	return shareddiscord.BuildLinkButtons(refs)
}

func dedupeVerifiedURLs(urls []string) []string {
	seen := make(map[string]struct{})
	var unique []string
	for _, raw := range urls {
		clean := strings.TrimSpace(raw)
		if clean == "" {
			continue
		}
		if _, exists := seen[clean]; exists {
			continue
		}
		seen[clean] = struct{}{}
		unique = append(unique, clean)
	}
	return unique
}
