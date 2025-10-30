package config

import (
	"os"
	"strconv"

	shareddata "github.com/stake-plus/govcomms/src/shared/data"
	"gorm.io/gorm"
)

// AIConfig holds AI-related configuration
type AIConfig struct {
	OpenAIKey      string
	ClaudeKey      string
	AIProvider     string
	AISystemPrompt string
	AIModel        string
	AIEnableWeb    bool
	AIEnableDeep   bool
}

// LoadAIConfig loads AI configuration
func LoadAIConfig(db *gorm.DB) AIConfig {
	openAIKey := GetSetting("openai_api_key", "OPENAI_API_KEY", "")
	claudeKey := GetSetting("claude_api_key", "CLAUDE_API_KEY", "")
	aiProvider := GetSetting("ai_provider", "AI_PROVIDER", "openai")

	aiSystemPrompt := GetSetting("ai_system_prompt", "AI_SYSTEM_PROMPT",
		`You are a helpful assistant that analyzes Polkadot/Kusama governance proposals and answers questions about them. 
Provide clear, concise, and accurate information based on the proposal content provided. 
Focus on facts from the proposal and avoid speculation. 
If information is not available in the provided content, clearly state that.`)

	aiModel := GetSetting("ai_model", "AI_MODEL", "")
	if aiModel == "" {
		if aiProvider == "claude" {
			aiModel = "claude-3-haiku-20240307"
		} else {
			aiModel = "gpt-5"
		}
	}
	aiEnableWeb := shareddata.GetSetting("ai_enable_web_search") == "1"
	aiEnableDeep := shareddata.GetSetting("ai_enable_deep_search") == "1"

	return AIConfig{
		OpenAIKey:      openAIKey,
		ClaudeKey:      claudeKey,
		AIProvider:     aiProvider,
		AISystemPrompt: aiSystemPrompt,
		AIModel:        aiModel,
		AIEnableWeb:    aiEnableWeb,
		AIEnableDeep:   aiEnableDeep,
	}
}

// QAConfig holds AI Q&A bot configuration
type QAConfig struct {
	Base
	AIConfig
	QARoleID string
	TempDir  string
}

// LoadQAConfig loads Q&A bot configuration
func LoadQAConfig(db *gorm.DB) QAConfig {
	base := LoadBase(db)
	ai := LoadAIConfig(db)
	qaRoleID := GetSetting("qa_role_id", "QA_ROLE_ID", "")
	tempDir := GetSetting("qa_temp_dir", "QA_TEMP_DIR", "/tmp/govcomms-qa")

	return QAConfig{
		Base:     base,
		AIConfig: ai,
		QARoleID: qaRoleID,
		TempDir:  tempDir,
	}
}

// ResearchConfig holds Research bot configuration
type ResearchConfig struct {
	Base
	OpenAIKey      string
	AIModel        string
	AIEnableWeb    bool
	ResearchRoleID string
	TempDir        string
}

// LoadResearchConfig loads Research bot configuration
func LoadResearchConfig(db *gorm.DB) ResearchConfig {
	base := LoadBase(db)
	researchRoleID := GetSetting("research_role_id", "RESEARCH_ROLE_ID", "")
	openAIKey := GetSetting("openai_api_key", "OPENAI_API_KEY", "")
	aiModel := GetSetting("ai_model", "AI_MODEL", "gpt-5")
	aiEnableWeb := shareddata.GetSetting("ai_enable_web_search") == "1"
	tempDir := GetSetting("qa_temp_dir", "QA_TEMP_DIR", "/tmp/govcomms-qa")

	return ResearchConfig{
		Base:          base,
		OpenAIKey:     openAIKey,
		AIModel:       aiModel,
		AIEnableWeb:   aiEnableWeb,
		ResearchRoleID: researchRoleID,
		TempDir:       tempDir,
	}
}

// FeedbackConfig holds Feedback bot configuration
type FeedbackConfig struct {
	Base
	FeedbackRoleID         string
	RedisURL                string
	IndexerWorkers          int
	IndexerIntervalMinutes  int
}

// LoadFeedbackConfig loads Feedback bot configuration
func LoadFeedbackConfig(db *gorm.DB) FeedbackConfig {
	base := LoadBase(db)
	feedbackRoleID := GetSetting("feedback_role_id", "FEEDBACK_ROLE_ID", "")
	redisURL := getenv("REDIS_URL", "redis://127.0.0.1:6379/0")

	workers := 10
	if workerStr := shareddata.GetSetting("indexer_workers"); workerStr != "" {
		if w, err := strconv.Atoi(workerStr); err == nil {
			workers = w
		}
	}

	intervalMinutes := 60
	if intervalStr := shareddata.GetSetting("indexer_interval_minutes"); intervalStr != "" {
		if i, err := strconv.Atoi(intervalStr); err == nil {
			intervalMinutes = i
		}
	}

	return FeedbackConfig{
		Base:                   base,
		FeedbackRoleID:         feedbackRoleID,
		RedisURL:                redisURL,
		IndexerWorkers:         workers,
		IndexerIntervalMinutes: intervalMinutes,
	}
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

