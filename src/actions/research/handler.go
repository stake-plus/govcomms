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

type cardContent struct {
	Title string
	Body  string
}

type columnGroup struct {
	MessageID string
	ChannelID string
	Indexes   []int
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

	headerTitle := fmt.Sprintf("Claim Verification • %s #%d", network, refID)
	headerBody := fmt.Sprintf("Found %d total historical claims, verifying top %d most important.", totalClaims, len(topClaims))
	headerHandle, err := shareddiscord.SendStyledHeaderMessage(s, channelID, headerTitle, headerBody)
	if err != nil {
		log.Printf("research: header send failed: %v", err)
	}

	claimCards := make([]cardContent, len(topClaims))
	for idx, claim := range topClaims {
		claimCards[idx] = cardContent{
			Title: fmt.Sprintf("Claim %d", idx+1),
			Body:  fmt.Sprintf("%s\n\n⏳ *Verifying...*", claim.Claim),
		}
	}

	claimGroups, claimLookup, err := sendColumnCardGroups(s, channelID, claimCards, 2)
	if err != nil {
		log.Printf("research: failed to send claim placeholders: %v", err)
		sendStyledMessage(s, channelID, "Research", "Failed to prepare verification output. Please try again.")
		return
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

		body := fmt.Sprintf("%s\n\n%s **%s** - %s",
			topClaims[i].Claim,
			statusEmoji,
			result.Status,
			result.Evidence)

		if urls := shareddiscord.FormatURLsNoEmbedMultiline(result.SourceURLs); urls != "" {
			body += "\n\n" + urls
		}

		claimCards[i].Body = body
		updateColumnCardMessage(s, claimGroups, claimLookup, claimCards, i, "research")
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
	headerBody := fmt.Sprintf("Found %d total historical claims, verifying top %d most important.", totalClaims, len(topClaims))
	headerHandle, err := shareddiscord.RespondStyledHeaderMessage(s, i.Interaction, headerTitle, headerBody)
	if err != nil {
		log.Printf("research: slash header send failed: %v", err)
		return
	}

	claimCards := make([]cardContent, len(topClaims))
	for idx, claim := range topClaims {
		claimCards[idx] = cardContent{
			Title: fmt.Sprintf("Claim %d", idx+1),
			Body:  fmt.Sprintf("%s\n\n⏳ *Verifying...*", claim.Claim),
		}
	}

	claimGroups, claimLookup, err := sendColumnCardGroups(s, i.ChannelID, claimCards, 2)
	if err != nil {
		log.Printf("research: slash column send failed: %v", err)
		sendStyledMessage(s, i.ChannelID, "Research", "Failed to prepare verification output. Please try again.")
		return
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

		body := fmt.Sprintf("%s\n\n%s **%s** - %s",
			topClaims[idx].Claim,
			statusEmoji,
			result.Status,
			result.Evidence)

		if urls := shareddiscord.FormatURLsNoEmbedMultiline(result.SourceURLs); urls != "" {
			body += "\n\n" + urls
		}

		claimCards[idx].Body = body
		updateColumnCardMessage(s, claimGroups, claimLookup, claimCards, idx, "research")
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
			indexes []int
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
