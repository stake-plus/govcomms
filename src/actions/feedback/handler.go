package feedback

import (
	"fmt"
	"log"
	"strings"
	"unicode/utf8"

	"github.com/bwmarrin/discordgo"
	"github.com/stake-plus/govcomms/src/actions/feedback/data"
	sharedconfig "github.com/stake-plus/govcomms/src/config"
	shareddiscord "github.com/stake-plus/govcomms/src/discord"
	sharedgov "github.com/stake-plus/govcomms/src/polkadot-go/governance"
	"gorm.io/gorm"
)

// Dependencies defines callbacks required from the feedback bot.
type Dependencies struct {
	EnsureThreadMapping              func(channelID string) (*sharedgov.ThreadInfo, error)
	PostFeedbackMessage              func(s *discordgo.Session, threadID string, network *sharedgov.Network, ref *sharedgov.Ref, authorTag, message string)
	SchedulePolkassemblyFirstMessage func(network *sharedgov.Network, ref *sharedgov.Ref)
}

// Handler encapsulates the /feedback action.
type Handler struct {
	Config         *sharedconfig.FeedbackConfig
	DB             *gorm.DB
	NetworkManager *sharedgov.NetworkManager
	RefManager     *sharedgov.ReferendumManager
	Deps           Dependencies
}

// HandleSlash executes the /feedback logic.
func (h *Handler) HandleSlash(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if h == nil {
		return
	}

	user := i.Member
	if user == nil {
		log.Printf("feedback: interaction missing member context")
		return
	}

	message := ""
	for _, opt := range i.ApplicationCommandData().Options {
		if opt.Name == "message" {
			message = strings.TrimSpace(opt.StringValue())
			break
		}
	}

	length := utf8.RuneCountInString(message)
	if length < 10 || length > 5000 {
		formatted := shareddiscord.FormatStyledBlock("Feedback", "Feedback must be between 10 and 5000 characters.")
		shareddiscord.InteractionRespondNoEmbed(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: formatted,
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	if h.Config.FeedbackRoleID != "" && !shareddiscord.HasRole(s, h.Config.Base.GuildID, user.User.ID, h.Config.FeedbackRoleID) {
		formatted := shareddiscord.FormatStyledBlock("Feedback", "You don't have permission to use this command.")
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
		log.Printf("feedback: failed to acknowledge interaction: %v", err)
		return
	}

	if h.Deps.EnsureThreadMapping == nil {
		respondFeedbackWithStyledEdit(s, i.Interaction, "Feedback", "Feedback action misconfigured: missing thread mapper.")
		return
	}

	threadInfo, err := h.Deps.EnsureThreadMapping(i.ChannelID)
	if err != nil {
		respondFeedbackWithStyledEdit(s, i.Interaction, "Feedback", "This command must be used in a referendum thread.")
		return
	}

	network := h.NetworkManager.GetByID(threadInfo.NetworkID)
	if network == nil {
		respondFeedbackWithStyledEdit(s, i.Interaction, "Feedback", "Unable to identify the associated network for this thread.")
		return
	}

	var ref sharedgov.Ref
	if err := h.DB.First(&ref, threadInfo.RefDBID).Error; err != nil {
		log.Printf("feedback: failed to load referendum %d: %v", threadInfo.RefDBID, err)
		respondFeedbackWithStyledEdit(s, i.Interaction, "Feedback", "Could not load referendum details. Please try again later.")
		return
	}

	authorTag := formatDiscordUsername(user.User.Username, user.User.Discriminator)

	if _, err := data.SaveFeedbackMessage(h.DB, &ref, authorTag, message); err != nil {
		log.Printf("feedback: failed to persist message: %v", err)
		respondFeedbackWithStyledEdit(s, i.Interaction, "Feedback", "Failed to store feedback. Please try again later.")
		return
	}

	if h.Deps.SchedulePolkassemblyFirstMessage != nil {
		h.Deps.SchedulePolkassemblyFirstMessage(network, &ref)
	}

	if h.Deps.PostFeedbackMessage != nil {
		h.Deps.PostFeedbackMessage(s, i.ChannelID, network, &ref, authorTag, message)
	}

	response := fmt.Sprintf("✅ Thank you %s! Your feedback for %s referendum #%d has been posted.",
		authorTag, network.Name, ref.RefID)
	respondFeedbackWithStyledEdit(s, i.Interaction, "Feedback", response)
}

func respondFeedbackWithStyledEdit(s *discordgo.Session, interaction *discordgo.Interaction, title, body string) {
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

// formatDiscordUsername formats a Discord username, handling the deprecated discriminator field
func formatDiscordUsername(username, discriminator string) string {
	if discriminator == "" || discriminator == "0" {
		return username
	}
	return fmt.Sprintf("%s#%s", username, discriminator)
}
