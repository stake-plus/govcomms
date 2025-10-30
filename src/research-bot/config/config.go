package config

import (
	"log"
	"os"

	"github.com/stake-plus/govcomms/src/research-bot/data"
	shareddata "github.com/stake-plus/govcomms/src/shared/data"
	"gorm.io/gorm"
)

type Config struct {
	Token          string
	GuildID        string
	MySQLDSN       string
	OpenAIKey      string
	AIModel        string
	AIEnableWeb    bool
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

	aiModel := data.GetSetting("ai_model")
	if aiModel == "" {
		aiModel = "gpt-5"
	}
	aiEnableWeb := data.GetSetting("ai_enable_web_search") == "1"

	tempDir := data.GetSetting("qa_temp_dir")
	if tempDir == "" {
		tempDir = "/tmp/govcomms-qa"
	}

	return Config{
		Token:          discordToken,
		GuildID:        guildID,
		MySQLDSN:       shareddata.GetMySQLDSN(),
		OpenAIKey:      openAIKey,
		AIModel:        aiModel,
		AIEnableWeb:    aiEnableWeb,
		ResearchRoleID: researchRoleID,
		TempDir:        tempDir,
	}
}
