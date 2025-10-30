package config

import (
	"os"

	"github.com/stake-plus/govcomms/src/shared/data"
	"gorm.io/gorm"
)

// Base contains common configuration fields
type Base struct {
	Token    string
	GuildID  string
	MySQLDSN string
}

// LoadBase loads common configuration (discord token, guild ID, MySQL DSN)
func LoadBase(db *gorm.DB) Base {
	if err := data.LoadSettings(db); err != nil {
		// Log but continue - env fallbacks will work
	}

	token := data.GetSetting("discord_token")
	if token == "" {
		token = os.Getenv("DISCORD_TOKEN")
	}

	guildID := data.GetSetting("guild_id")
	if guildID == "" {
		guildID = os.Getenv("GUILD_ID")
	}

	return Base{
		Token:    token,
		GuildID:  guildID,
		MySQLDSN: data.GetMySQLDSN(),
	}
}

// GetSetting retrieves a setting with env fallback
func GetSetting(name, envKey, defaultValue string) string {
	val := data.GetSetting(name)
	if val == "" {
		val = os.Getenv(envKey)
	}
	if val == "" {
		val = defaultValue
	}
	return val
}

