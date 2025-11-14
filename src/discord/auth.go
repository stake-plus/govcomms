package discord

import "github.com/bwmarrin/discordgo"

// HasRole checks whether a user has a role in a guild. Empty roleID always returns true.
func HasRole(s *discordgo.Session, guildID, userID, roleID string) bool {
    if roleID == "" { return true }
    member, err := s.GuildMember(guildID, userID)
    if err != nil { return false }
    for _, role := range member.Roles {
        if role == roleID { return true }
    }
    return false
}


