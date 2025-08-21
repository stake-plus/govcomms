package config

import (
	"log"
	"os"
	"strconv"

	"github.com/stake-plus/govcomms/src/feedback/data"
	"gorm.io/gorm"
)

type Config struct {
	Token                  string
	FeedbackRoleID         string
	GuildID                string
	MySQLDSN               string
	RedisURL               string
	IndexerWorkers         int
	IndexerIntervalMinutes int
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

	workers := 10
	if workerStr := data.GetSetting("indexer_workers"); workerStr != "" {
		if w, err := strconv.Atoi(workerStr); err == nil {
			workers = w
		}
	}

	intervalMinutes := 60
	if intervalStr := data.GetSetting("indexer_interval_minutes"); intervalStr != "" {
		if i, err := strconv.Atoi(intervalStr); err == nil {
			intervalMinutes = i
		}
	}

	return Config{
		Token:                  discordToken,
		FeedbackRoleID:         feedbackRoleID,
		GuildID:                guildID,
		MySQLDSN:               GetMySQLDSN(),
		RedisURL:               getenv("REDIS_URL", "redis://127.0.0.1:6379/0"),
		IndexerWorkers:         workers,
		IndexerIntervalMinutes: intervalMinutes,
	}
}

func GetMySQLDSN() string {
	dsn := os.Getenv("MYSQL_DSN")
	if dsn == "" {
		dsn = "govcomms:DK3mfv93jf4m@tcp(127.0.0.1:3306)/govcomms"
	}
	return dsn
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
