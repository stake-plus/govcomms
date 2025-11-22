package discord

import (
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"
)

const (
	CommandQuestion = "question"
	CommandRefresh  = "refresh"
	CommandContext  = "context"
	CommandSummary  = "summary"
	CommandResearch = "research"
	CommandTeam     = "team"
	CommandFeedback = "feedback"
)

var commandDefinitions = map[string]*discordgo.ApplicationCommand{
	CommandQuestion: {
		Name:        CommandQuestion,
		Description: "Ask a question about the current referendum",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "question",
				Description: "Your question about the referendum",
				Required:    true,
			},
		},
	},
	CommandRefresh: {
		Name:        CommandRefresh,
		Description: "Refresh the proposal content for this referendum",
	},
	CommandContext: {
		Name:        CommandContext,
		Description: "Show the Q&A context for this referendum",
	},
	CommandSummary: {
		Name:        CommandSummary,
		Description: "Show the summary for this referendum",
	},
	CommandResearch: {
		Name:        CommandResearch,
		Description: "Research and verify claims in this referendum",
	},
	CommandTeam: {
		Name:        CommandTeam,
		Description: "Analyze team members in this referendum",
	},
	CommandFeedback: {
		Name:        CommandFeedback,
		Description: "Submit feedback for this referendum",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "message",
				Description: "Your feedback message (10-5000 characters)",
				Required:    true,
			},
		},
	},
}

var defaultCommandOrder = []string{
	CommandQuestion,
	CommandRefresh,
	CommandContext,
	CommandSummary,
	CommandResearch,
	CommandTeam,
	CommandFeedback,
}

// RegisterSlashCommands registers the requested slash commands for a guild.
// When no command names are provided, all known commands are registered.
func RegisterSlashCommands(s *discordgo.Session, guildID string, names ...string) error {
	if guildID == "" {
		return fmt.Errorf("discord: guildID is required to register slash commands")
	}

	if len(names) == 0 {
		names = defaultCommandOrder
	}

	var failures []string
	for _, name := range names {
		definition, ok := commandDefinitions[name]
		if !ok {
			log.Printf("discord: unknown slash command %q", name)
			continue
		}

		_, err := s.ApplicationCommandCreate(s.State.User.ID, guildID, definition)
		if err != nil {
			if isDuplicateCommandError(err) {
				log.Printf("discord: slash command %q already registered", name)
				continue
			}
			failures = append(failures, fmt.Sprintf("%s: %v", name, err))
			log.Printf("discord: failed to register command %q: %v", name, err)
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("discord: slash command registration errors: %s", strings.Join(failures, "; "))
	}

	return nil
}

// DeleteSlashCommands removes all registered slash commands for a guild.
func DeleteSlashCommands(s *discordgo.Session, guildID string) error {
	if guildID == "" {
		return fmt.Errorf("discord: guildID is required to delete slash commands")
	}

	commands, err := s.ApplicationCommands(s.State.User.ID, guildID)
	if err != nil {
		return err
	}

	for _, cmd := range commands {
		if err := s.ApplicationCommandDelete(s.State.User.ID, guildID, cmd.ID); err != nil {
			return err
		}
	}

	return nil
}

func isDuplicateCommandError(err error) bool {
	var restErr *discordgo.RESTError
	if errors.As(err, &restErr) {
		if restErr.Message != nil {
			msg := strings.ToLower(restErr.Message.Message)
			if strings.Contains(msg, "already exists") {
				return true
			}
		}
	}

	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "50035") && strings.Contains(msg, "already exists")
}
