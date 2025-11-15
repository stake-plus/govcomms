package config

import "os"

type AI struct {
	Provider     string
	OpenAIKey    string
	ClaudeKey    string
	GeminiKey    string
	DeepSeekKey  string
	GrokKey      string
	SystemPrompt string
	Model        string
	EnableWeb    bool
	EnableDeep   bool
}

// LoadAIFromEnv provides a simple env-only loader; services can merge DB settings over this.
func LoadAIFromEnv() AI {
	provider := os.Getenv("AI_PROVIDER")
	if provider == "" {
		provider = "openai"
	}
	model := os.Getenv("AI_MODEL")
	if model == "" {
		if provider == "claude" {
			model = "claude-3-haiku-20240307"
		} else {
			model = "gpt-5"
		}
	}
	return AI{
		Provider:     provider,
		OpenAIKey:    os.Getenv("OPENAI_API_KEY"),
		ClaudeKey:    os.Getenv("CLAUDE_API_KEY"),
		GeminiKey:    os.Getenv("GEMINI_API_KEY"),
		DeepSeekKey:  os.Getenv("DEEPSEEK_API_KEY"),
		GrokKey:      os.Getenv("GROK_API_KEY"),
		SystemPrompt: os.Getenv("AI_SYSTEM_PROMPT"),
		Model:        model,
		EnableWeb:    os.Getenv("AI_ENABLE_WEB_SEARCH") == "1",
		EnableDeep:   os.Getenv("AI_ENABLE_DEEP_SEARCH") == "1",
	}
}
