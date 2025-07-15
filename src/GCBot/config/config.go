package config

import (
	"log"
	"os"

	"github.com/stake-plus/govcomms/src/GCApi/data"
	"gorm.io/gorm"
)

type Config struct {
	Token              string
	FeedbackRoleID     string
	GuildID            string
	PolkassemblyAPIKey string
	MySQLDSN           string
	RedisURL           string
}

func Load(db *gorm.DB) Config {
	// Load settings from database
	if err := data.LoadSettings(db); err != nil {
		log.Printf("Failed to load settings: %v", err)
	}

	// Get values from database with env fallbacks
	discordToken := data.GetSetting("discord_token")
	if discordToken == "" {
		discordToken = os.Getenv("DISCORD_TOKEN")
	}

	feedbackRoleID := data.GetSetting("feedback_role_id")
	if feedbackRoleID == "" {
		feedbackRoleID = os.Getenv("FEEDBACK_ROLE_ID")
	}

	guildID := data.GetSetting("guild_id")
	if guildID == "" {
		guildID = os.Getenv("GUILD_ID")
	}

	polkassemblyAPIKey := data.GetSetting("polkassembly_api_key")
	if polkassemblyAPIKey == "" {
		polkassemblyAPIKey = os.Getenv("POLKASSEMBLY_API_KEY")
	}

	return Config{
		Token:              discordToken,
		FeedbackRoleID:     feedbackRoleID,
		GuildID:            guildID,
		PolkassemblyAPIKey: polkassemblyAPIKey,
		MySQLDSN:           getenv("MYSQL_DSN", "govcomms:DK3mfv93jf4m@tcp(127.0.0.1:3306)/govcomms"),
		RedisURL:           getenv("REDIS_URL", "redis://127.0.0.1:6379/0"),
	}
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
