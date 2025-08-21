package config

import (
	"log"
	"os"

	"github.com/stake-plus/govcomms/src/ai-qa/data"
	"gorm.io/gorm"
)

type Config struct {
	Token          string
	GuildID        string
	MySQLDSN       string
	OpenAIKey      string
	ClaudeKey      string
	AIProvider     string
	AISystemPrompt string
	QARoleID       string
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

	qaRoleID := data.GetSetting("qa_role_id")
	if qaRoleID == "" {
		qaRoleID = os.Getenv("QA_ROLE_ID")
	}

	openAIKey := data.GetSetting("openai_api_key")
	if openAIKey == "" {
		openAIKey = os.Getenv("OPENAI_API_KEY")
	}

	claudeKey := data.GetSetting("claude_api_key")
	if claudeKey == "" {
		claudeKey = os.Getenv("CLAUDE_API_KEY")
	}

	aiProvider := data.GetSetting("ai_provider")
	if aiProvider == "" {
		aiProvider = "openai"
	}

	aiSystemPrompt := data.GetSetting("ai_system_prompt")
	if aiSystemPrompt == "" {
		aiSystemPrompt = `You are a helpful assistant that analyzes Polkadot/Kusama governance proposals and answers questions about them. 
Provide clear, concise, and accurate information based on the proposal content provided. 
Focus on facts from the proposal and avoid speculation. 
If information is not available in the provided content, clearly state that.`
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
		ClaudeKey:      claudeKey,
		AIProvider:     aiProvider,
		AISystemPrompt: aiSystemPrompt,
		QARoleID:       qaRoleID,
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
