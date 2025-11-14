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

type cardContent struct {
	Title string
	Body  string
}

type columnGroup struct {
	MessageID string
	ChannelID string
	Indexes   []int
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

	headerTitle := fmt.Sprintf("Team Analysis • %s #%d", network, refID)
	headerBody := fmt.Sprintf("Found %d team members to analyze for %s referendum #%d.", len(members), network, refID)
	headerHandle, err := shareddiscord.SendStyledHeaderMessage(s, channelID, headerTitle, headerBody)
	if err != nil {
		log.Printf("team: header send failed: %v", err)
	}

	memberCards := make([]cardContent, len(members))
	for i, member := range members {
		body := member.Name
		if member.Role != "" {
			body += fmt.Sprintf(" (%s)", member.Role)
		}
		body += "\n⏳ *Analyzing...*"
		memberCards[i] = cardContent{
			Title: fmt.Sprintf("Member %d", i+1),
			Body:  body,
		}
	}

	memberGroups, memberLookup, err := sendColumnCardGroups(s, channelID, memberCards, 2)
	if err != nil {
		log.Printf("team: failed to send member placeholders: %v", err)
		sendTeamStyledMessage(s, channelID, "Team", "Failed to prepare analysis output. Please try again.")
		return
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

		memberCards[i].Body = strings.Join(sections, "\n\n")
		updateColumnCardMessage(s, memberGroups, memberLookup, memberCards, i, "team")
	}

	teamAssessment := "❌ Team unlikely to complete the proposed task"
	if len(results) > 0 && realCount == len(results) && skilledCount >= len(results)*3/4 {
		teamAssessment = "✅ Team appears capable of completing the proposed task"
	} else if realCount >= len(results)/2 && skilledCount >= len(results)/2 {
		teamAssessment = "⚠️ Team may be capable but has some concerns"
	}

	summaryBody := fmt.Sprintf("👤 Real People: %d/%d | 🎯 Verified Skills: %d/%d\n\n**Assessment:** %s",
		realCount, len(results), skilledCount, len(results), teamAssessment)
	finalHeaderBody := fmt.Sprintf("%s\n\n%s", headerBody, summaryBody)
	if headerHandle != nil {
		if err := headerHandle.Update(s, headerTitle, finalHeaderBody); err != nil {
			log.Printf("team: header update failed: %v", err)
			sendTeamStyledMessage(s, channelID, headerTitle, finalHeaderBody)
		}
	} else {
		sendTeamStyledMessage(s, channelID, headerTitle, finalHeaderBody)
	}
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

	memberCards := make([]cardContent, len(members))
	for idx, member := range members {
		body := member.Name
		if member.Role != "" {
			body += fmt.Sprintf(" (%s)", member.Role)
		}
		body += "\n⏳ *Analyzing...*"
		memberCards[idx] = cardContent{
			Title: fmt.Sprintf("Member %d", idx+1),
			Body:  body,
		}
	}

	memberGroups, memberLookup, err := sendColumnCardGroups(s, i.ChannelID, memberCards, 2)
	if err != nil {
		log.Printf("team: slash member placeholders failed: %v", err)
		sendTeamStyledMessage(s, i.ChannelID, "Team", "Failed to prepare analysis output. Please try again.")
		return
	}

	results, err := h.TeamsAnalyzer.AnalyzeTeamMembers(ctx, members)
	if err != nil {
		sendTeamWebhookEdit(s, i.Interaction, "Team", fmt.Sprintf("Error analyzing team members: %v", err))
		return
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

		memberCards[idx].Body = strings.Join(sections, "\n\n")
		updateColumnCardMessage(s, memberGroups, memberLookup, memberCards, idx, "team")
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

func sendColumnCardGroups(s *discordgo.Session, channelID string, cards []cardContent, columns int) ([]columnGroup, []int, error) {
	if columns < 1 {
		columns = 1
	}
	if len(cards) == 0 {
		return nil, nil, nil
	}

	var groups []columnGroup
	for start := 0; start < len(cards); start += columns {
		end := start + columns
		if end > len(cards) {
			end = len(cards)
		}

		var (
			indexes  []int
			payloads []shareddiscord.StyledMessage
		)

		for idx := start; idx < end; idx++ {
			indexes = append(indexes, idx)
			payloads = append(payloads, shareddiscord.BuildStyledMessage(cards[idx].Title, cards[idx].Body))
		}

		combined := shareddiscord.CombineStyledGroup(payloads)
		msg := &discordgo.MessageSend{
			Content: combined.Content,
		}
		if len(combined.Components) > 0 {
			msg.Components = combined.Components
		}

		sent, err := shareddiscord.SendComplexMessageNoEmbed(s, channelID, msg)
		if err != nil {
			return nil, nil, err
		}

		groups = append(groups, columnGroup{
			MessageID: sent.ID,
			ChannelID: channelID,
			Indexes:   indexes,
		})

		time.Sleep(100 * time.Millisecond)
	}

	lookup := make([]int, len(cards))
	for groupIdx, grp := range groups {
		for _, cardIdx := range grp.Indexes {
			lookup[cardIdx] = groupIdx
		}
	}

	return groups, lookup, nil
}

func updateColumnCardMessage(s *discordgo.Session, groups []columnGroup, lookup []int, cards []cardContent, targetIdx int, prefix string) {
	if targetIdx < 0 || targetIdx >= len(lookup) {
		return
	}
	if len(groups) == 0 {
		return
	}

	groupIdx := lookup[targetIdx]
	if groupIdx < 0 || groupIdx >= len(groups) {
		return
	}

	group := groups[groupIdx]
	combined := combineCardGroup(cards, group.Indexes)

	edit := &discordgo.MessageEdit{
		ID:      group.MessageID,
		Channel: group.ChannelID,
		Content: &combined.Content,
	}
	if len(combined.Components) > 0 {
		components := combined.Components
		edit.Components = &components
	} else {
		empty := []discordgo.MessageComponent{}
		edit.Components = &empty
	}

	if _, err := shareddiscord.EditMessageComplexNoEmbed(s, edit); err != nil {
		log.Printf("%s: update column message failed: %v", prefix, err)
	}
}

func combineCardGroup(cards []cardContent, indexes []int) shareddiscord.StyledMessage {
	if len(indexes) == 0 {
		return shareddiscord.StyledMessage{}
	}
	if len(indexes) == 1 {
		card := cards[indexes[0]]
		return shareddiscord.BuildStyledMessage(card.Title, card.Body)
	}

	var payloads []shareddiscord.StyledMessage
	for _, idx := range indexes {
		if idx < 0 || idx >= len(cards) {
			continue
		}
		payloads = append(payloads, shareddiscord.BuildStyledMessage(cards[idx].Title, cards[idx].Body))
	}
	return shareddiscord.CombineStyledGroup(payloads)
}
