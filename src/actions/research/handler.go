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

	go h.runResearchWorkflow(s, m.ChannelID, network.Name, uint32(threadInfo.RefID))
}

// HandleSlash processes the /research action.
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
		log.Printf("research: slash ack failed: %v", err)
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

	go h.runResearchWorkflowSlash(s, i, network.Name, uint32(threadInfo.RefID))
}

func (h *Handler) runResearchWorkflow(s *discordgo.Session, channelID string, network string, refID uint32) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	proposalContent, err := h.Cache.GetProposalContent(network, refID)
	if err != nil {
		s.ChannelMessageSend(channelID, "Proposal content not found. Please run !refresh first.")
		return
	}

	topClaims, totalClaims, err := h.ClaimsAnalyzer.ExtractTopClaims(ctx, proposalContent)
	if err != nil {
		s.ChannelMessageSend(channelID, fmt.Sprintf("Error extracting claims: %v", err))
		return
	}

	if len(topClaims) == 0 {
		s.ChannelMessageSend(channelID, "No verifiable historical claims found in the proposal.")
		return
	}

	headerMsg := fmt.Sprintf("🔍 **Verifying Historical Claims for %s Referendum #%d**\n", network, refID)
	headerMsg += fmt.Sprintf("Found %d total historical claims, verifying top %d most important:\n", totalClaims, len(topClaims))
	s.ChannelMessageSend(channelID, headerMsg)

	claimMessages := make(map[int]*discordgo.Message)
	for i, claim := range topClaims {
		msgContent := fmt.Sprintf("**Claim %d:** %s\n⏳ *Verifying...*", i+1, claim.Claim)
		msg, err := s.ChannelMessageSend(channelID, msgContent)
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
			updatedContent := fmt.Sprintf("**Claim %d:** %s\n%s **%s** - %s",
				i+1,
				topClaims[i].Claim,
				statusEmoji,
				result.Status,
				result.Evidence)

			if len(result.SourceURLs) > 0 {
				updatedContent += fmt.Sprintf("\n📌 Sources: %s", shareddiscord.FormatURLsNoEmbed(result.SourceURLs))
			}
			updatedContent = shareddiscord.WrapURLsNoEmbed(updatedContent)
			s.ChannelMessageEdit(channelID, msg.ID, updatedContent)
		}
	}

	summaryMsg := fmt.Sprintf("\n📊 **Verification Complete**\n✅ Valid: %d | ❌ Rejected: %d | ❓ Unknown: %d",
		validCount, rejectedCount, unknownCount)
	s.ChannelMessageSend(channelID, summaryMsg)
}

func (h *Handler) runResearchWorkflowSlash(s *discordgo.Session, i *discordgo.InteractionCreate, network string, refID uint32) {
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

	topClaims, totalClaims, err := h.ClaimsAnalyzer.ExtractTopClaims(ctx, proposalContent)
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

	headerMsg := fmt.Sprintf("🔍 **Verifying Historical Claims for %s Referendum #%d**\n", network, refID)
	headerMsg += fmt.Sprintf("Found %d total historical claims, verifying top %d most important:\n", totalClaims, len(topClaims))
	s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: &headerMsg,
	})

	claimMessages := make(map[int]*discordgo.Message)
	for idx, claim := range topClaims {
		msgContent := fmt.Sprintf("**Claim %d:** %s\n⏳ *Verifying...*", idx+1, claim.Claim)
		msg, err := s.ChannelMessageSend(i.ChannelID, msgContent)
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
}

