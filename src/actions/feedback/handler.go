package feedback

import (
	"fmt"
	"log"
	"strings"
	"unicode/utf8"

	"github.com/bwmarrin/discordgo"
	"github.com/stake-plus/govcomms/src/actions/feedback/data"
	sharedconfig "github.com/stake-plus/govcomms/src/shared/config"
	shareddiscord "github.com/stake-plus/govcomms/src/shared/discord"
	sharedgov "github.com/stake-plus/govcomms/src/shared/gov"
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
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "Feedback must be between 10 and 5000 characters.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	if h.Config.FeedbackRoleID != "" && !shareddiscord.HasRole(s, h.Config.Base.GuildID, user.User.ID, h.Config.FeedbackRoleID) {
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
		log.Printf("feedback: failed to acknowledge interaction: %v", err)
		return
	}

	if h.Deps.EnsureThreadMapping == nil {
		msg := "Feedback action misconfigured: missing thread mapper."
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &msg})
		return
	}

	threadInfo, err := h.Deps.EnsureThreadMapping(i.ChannelID)
	if err != nil {
		msg := "This command must be used in a referendum thread."
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &msg})
		return
	}

	network := h.NetworkManager.GetByID(threadInfo.NetworkID)
	if network == nil {
		msg := "Unable to identify the associated network for this thread."
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &msg})
		return
	}

	var ref sharedgov.Ref
	if err := h.DB.First(&ref, threadInfo.RefDBID).Error; err != nil {
		log.Printf("feedback: failed to load referendum %d: %v", threadInfo.RefDBID, err)
		msg := "Could not load referendum details. Please try again later."
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &msg})
		return
	}

	authorTag := fmt.Sprintf("%s#%s", user.User.Username, user.User.Discriminator)

	if _, err := data.SaveFeedbackMessage(h.DB, &ref, authorTag, message); err != nil {
		log.Printf("feedback: failed to persist message: %v", err)
		msg := "Failed to store feedback. Please try again later."
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &msg})
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
	s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &response})
}
