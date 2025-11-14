package discord

import (
	"github.com/bwmarrin/discordgo"
)

// HeaderHandle tracks the primary message for a multi-part response so it can be updated later.
type HeaderHandle struct {
	channelID      string
	messageID      string
	interaction    *discordgo.Interaction
	viaInteraction bool
}

// SendStyledHeaderMessage sends styled payloads to a channel and returns a handle for the first message.
func SendStyledHeaderMessage(s *discordgo.Session, channelID, title, body string) (*HeaderHandle, error) {
	payloads := BuildStyledMessages(title, body, "")
	if len(payloads) == 0 {
		return nil, nil
	}

	sent, err := dispatchStyledMessages(s, channelID, payloads)
	if err != nil {
		return nil, err
	}
	if len(sent) == 0 {
		return nil, nil
	}

	return &HeaderHandle{
		channelID: channelID,
		messageID: sent[0].ID,
	}, nil
}

// RespondStyledHeaderMessage edits the deferred interaction reply with styled content and returns a handle.
func RespondStyledHeaderMessage(s *discordgo.Session, interaction *discordgo.Interaction, title, body string) (*HeaderHandle, error) {
	payloads := BuildStyledMessages(title, body, "")
	if len(payloads) == 0 {
		empty := ""
		_, err := InteractionResponseEditNoEmbed(s, interaction, &discordgo.WebhookEdit{Content: &empty})
		return nil, err
	}

	first := payloads[0]
	edit := &discordgo.WebhookEdit{
		Content: &first.Content,
	}
	if len(first.Components) > 0 {
		components := first.Components
		edit.Components = &components
	} else {
		emptyComponents := []discordgo.MessageComponent{}
		edit.Components = &emptyComponents
	}

	msg, err := InteractionResponseEditNoEmbed(s, interaction, edit)
	if err != nil {
		return nil, err
	}

	if len(payloads) > 1 {
		if _, err := dispatchStyledMessages(s, interaction.ChannelID, payloads[1:]); err != nil {
			return &HeaderHandle{
				channelID:      interaction.ChannelID,
				messageID:      msg.ID,
				interaction:    interaction,
				viaInteraction: true,
			}, err
		}
	}

	return &HeaderHandle{
		channelID:      interaction.ChannelID,
		messageID:      msg.ID,
		interaction:    interaction,
		viaInteraction: true,
	}, nil
}

// Update refreshes the header message with new content and components.
func (h *HeaderHandle) Update(s *discordgo.Session, title, body string) error {
	if h == nil {
		return nil
	}

	payload := BuildStyledMessage(title, body)

	if h.viaInteraction && h.interaction != nil {
		edit := &discordgo.WebhookEdit{
			Content: &payload.Content,
		}
		if len(payload.Components) > 0 {
			components := payload.Components
			edit.Components = &components
		} else {
			empty := []discordgo.MessageComponent{}
			edit.Components = &empty
		}
		_, err := InteractionResponseEditNoEmbed(s, h.interaction, edit)
		return err
	}

	if h.channelID == "" || h.messageID == "" {
		return nil
	}

	edit := &discordgo.MessageEdit{
		ID:      h.messageID,
		Channel: h.channelID,
		Content: &payload.Content,
	}
	if len(payload.Components) > 0 {
		components := payload.Components
		edit.Components = &components
	} else {
		empty := []discordgo.MessageComponent{}
		edit.Components = &empty
	}

	_, err := EditMessageComplexNoEmbed(s, edit)
	return err
}

func dispatchStyledMessages(s *discordgo.Session, channelID string, payloads []StyledMessage) ([]*discordgo.Message, error) {
	var sent []*discordgo.Message
	for _, payload := range payloads {
		msg := &discordgo.MessageSend{
			Content: payload.Content,
		}
		if len(payload.Components) > 0 {
			msg.Components = payload.Components
		}

		sentMsg, err := SendComplexMessageNoEmbed(s, channelID, msg)
		if err != nil {
			return sent, err
		}
		sent = append(sent, sentMsg)
	}
	return sent, nil
}

