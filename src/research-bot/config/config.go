package config

import (
	"log"
	"os"

	"github.com/stake-plus/govcomms/src/research-bot/data"
	"gorm.io/gorm"
)

type Config struct {
	Token          string
	GuildID        string
	MySQLDSN       string
	OpenAIKey      string
	ResearchRoleID string
	TempDir        string
}

func Load(db *gorm.DB) Config {
	if err := data.LoadSettings(db); err != nil {
		log.Printf("Failed to load settings: %v", err)
	}

	discordToken := data.GetSetting("discord_token")
	if discordToken == "" {
		discordToken = os.Getenv("DISCORD_TOKEN")
	}

	guildID := data.GetSetting("guild_id")
	if guildID == "" {
		guildID = os.Getenv("GUILD_ID")
	}

	researchRoleID := data.GetSetting("research_role_id")
	if researchRoleID == "" {
		researchRoleID = os.Getenv("RESEARCH_ROLE_ID")
	}

	openAIKey := data.GetSetting("openai_api_key")
	if openAIKey == "" {
		openAIKey = os.Getenv("OPENAI_API_KEY")
	}

	tempDir := data.GetSetting("qa_temp_dir")
	if tempDir == "" {
		tempDir = "/tmp/govcomms-qa"
	}

	return Config{
		Token:          discordToken,
		GuildID:        guildID,
		MySQLDSN:       GetMySQLDSN(),
		OpenAIKey:      openAIKey,
		ResearchRoleID: researchRoleID,
		TempDir:        tempDir,
	}
}

func GetMySQLDSN() string {
	dsn := os.Getenv("MYSQL_DSN")
	if dsn == "" {
		dsn = "govcomms:DK3mfv93jf4m@tcp(127.0.0.1:3306)/govcomms"
	}
	return dsn
}
