package config

import (
	"log"
	"os"
	"strings"

	"github.com/stake-plus/govcomms/src/data/mysql"
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
	if err := mysql.LoadSettings(db); err != nil {
		// Log but continue - env fallbacks will work
	}

	token := mysql.GetSetting("discord_token")
	if token == "" {
		token = os.Getenv("DISCORD_TOKEN")
	}

	guildID := mysql.GetSetting("guild_id")
	if guildID == "" {
		guildID = os.Getenv("GUILD_ID")
	}

	dsn, err := mysql.GetMySQLDSN()
	if err != nil {
		log.Printf("config: %v", err)
	}

	return Base{
		Token:    token,
		GuildID:  guildID,
		MySQLDSN: dsn,
	}
}

// GetSetting retrieves a setting with env fallback
func GetSetting(name, envKey, defaultValue string) string {
	val := mysql.GetSetting(name)
	if val == "" {
		val = os.Getenv(envKey)
	}
	if val == "" {
		val = defaultValue
	}
	return val
}

func getBoolSetting(settingKey, envKey string, defaultValue bool) bool {
	if v := mysql.GetSetting(settingKey); v != "" {
		return parseBoolDefault(v, defaultValue)
	}
	if envKey != "" {
		if v := os.Getenv(envKey); v != "" {
			return parseBoolDefault(v, defaultValue)
		}
	}
	return defaultValue
}

func parseBoolDefault(value string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}
