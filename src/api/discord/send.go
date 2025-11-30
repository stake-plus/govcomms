package discord

import (
	"errors"

	"github.com/bwmarrin/discordgo"
)

// SendMessageNoEmbed sends a standard message after stripping Discord URL embeds.
func SendMessageNoEmbed(s *discordgo.Session, channelID, content string) (*discordgo.Message, error) {
	return s.ChannelMessageSend(channelID, WrapURLsNoEmbed(content))
}

// EditMessageNoEmbed edits an existing message ensuring URLs remain non-embedded.
func EditMessageNoEmbed(s *discordgo.Session, channelID, messageID, content string) (*discordgo.Message, error) {
	return s.ChannelMessageEdit(channelID, messageID, WrapURLsNoEmbed(content))
}

// EditMessageComplexNoEmbed edits an existing message and supports components/attachments.
func EditMessageComplexNoEmbed(s *discordgo.Session, edit *discordgo.MessageEdit) (*discordgo.Message, error) {
	if edit == nil {
		return nil, errors.New("discord: message edit payload cannot be nil")
	}

	if edit.Content != nil {
		cleaned := WrapURLsNoEmbed(*edit.Content)
		edit.Content = &cleaned
	}

	if edit.Embeds != nil {
		sanitizeEmbeds(*edit.Embeds)
	}
	return s.ChannelMessageEditComplex(edit)
}

// SendComplexMessageNoEmbed sends a complex message payload with sanitized embeds/content.
func SendComplexMessageNoEmbed(s *discordgo.Session, channelID string, msg *discordgo.MessageSend) (*discordgo.Message, error) {
	if msg == nil {
		return nil, errors.New("discord: message payload cannot be nil")
	}

	sanitizeMessageSend(msg)
	return s.ChannelMessageSendComplex(channelID, msg)
}

// InteractionRespondNoEmbed wraps InteractionRespond ensuring the data content is sanitized.
func InteractionRespondNoEmbed(s *discordgo.Session, interaction *discordgo.Interaction, resp *discordgo.InteractionResponse) error {
	sanitizeInteractionResponse(resp)
	return s.InteractionRespond(interaction, resp)
}

// InteractionResponseEditNoEmbed wraps InteractionResponseEdit ensuring any content/embeds are sanitized.
func InteractionResponseEditNoEmbed(s *discordgo.Session, interaction *discordgo.Interaction, edit *discordgo.WebhookEdit) (*discordgo.Message, error) {
	sanitizeWebhookEdit(edit)
	return s.InteractionResponseEdit(interaction, edit)
}

func sanitizeInteractionResponse(resp *discordgo.InteractionResponse) {
	if resp == nil || resp.Data == nil {
		return
	}
	sanitizeInteractionResponseData(resp.Data)
}

func sanitizeInteractionResponseData(data *discordgo.InteractionResponseData) {
	if data.Content != "" {
		data.Content = WrapURLsNoEmbed(data.Content)
	}
	sanitizeEmbeds(data.Embeds)
}

func sanitizeWebhookEdit(edit *discordgo.WebhookEdit) {
	if edit == nil {
		return
	}

	if edit.Content != nil {
		cleaned := WrapURLsNoEmbed(*edit.Content)
		edit.Content = &cleaned
	}

	if edit.Embeds != nil {
		sanitizeEmbeds(*edit.Embeds)
	}
}

func sanitizeMessageSend(msg *discordgo.MessageSend) {
	if msg == nil {
		return
	}

	if msg.Content != "" {
		msg.Content = WrapURLsNoEmbed(msg.Content)
	}

	sanitizeEmbeds(msg.Embeds)
}

func sanitizeEmbeds(embeds []*discordgo.MessageEmbed) {
	for _, embed := range embeds {
		if embed == nil {
			continue
		}

		if embed.Description != "" {
			embed.Description = WrapURLsNoEmbed(embed.Description)
		}

		if embed.Footer != nil && embed.Footer.Text != "" {
			embed.Footer.Text = WrapURLsNoEmbed(embed.Footer.Text)
		}

		for _, field := range embed.Fields {
			if field == nil {
				continue
			}
			if field.Name != "" {
				field.Name = WrapURLsNoEmbed(field.Name)
			}
			if field.Value != "" {
				field.Value = WrapURLsNoEmbed(field.Value)
			}
		}
	}
}
