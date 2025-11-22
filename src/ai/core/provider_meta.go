package core

import (
	"strings"
)

type ProviderInfo struct {
	Company string
	Website string
	Model   string
}

var providerInfo = map[string]ProviderInfo{
	"gpt51": {
		Company: "OpenAI",
		Website: "https://openai.com",
		Model:   "gpt-5.1",
	},
	"gpt4o": {
		Company: "OpenAI",
		Website: "https://openai.com",
		Model:   "gpt-4o",
	},
	"deepseek3": {
		Company: "HangZhou DeepSeek",
		Website: "https://deepseek.com",
		Model:   "deepseek-chat",
	},
	"gemini25": {
		Company: "Google",
		Website: "https://ai.google.com",
		Model:   "gemini-2.5-flash",
	},
	"grok4": {
		Company: "X.ai",
		Website: "https://x.ai",
		Model:   "grok-4-fast-reasoning",
	},
	"haiku45": {
		Company: "Anthropic",
		Website: "https://anthropic.com",
		Model:   "claude-haiku-4-5",
	},
	"opus41": {
		Company: "Anthropic",
		Website: "https://anthropic.com",
		Model:   "claude-opus-4-1",
	},
	"sonnet45": {
		Company: "Anthropic",
		Website: "https://anthropic.com",
		Model:   "claude-sonnet-4-5",
	},
}

var providerDefaultModels = map[string]string{
	"gpt51":     "gpt-5.1",
	"gpt4o":     "gpt-4o",
	"deepseek3": "deepseek-chat",
	"gemini25":  "gemini-2.5-flash",
	"grok4":     "grok-4-fast-reasoning",
	"haiku45":   "claude-haiku-4-5",
	"opus41":    "claude-opus-4-1",
	"sonnet45":  "claude-sonnet-4-5",
}

// GetProviderInfo returns provider metadata (company, website, model) for a provider key.
func GetProviderInfo(provider string) (ProviderInfo, bool) {
	key := strings.ToLower(strings.TrimSpace(provider))
	info, ok := providerInfo[key]
	return info, ok
}

// DefaultModelForProvider returns the baked-in default model for a provider key.
func DefaultModelForProvider(provider string) string {
	key := strings.ToLower(strings.TrimSpace(provider))
	if val, ok := providerDefaultModels[key]; ok {
		return val
	}
	return ""
}

// ResolveModelName picks the configured model if provided, otherwise the provider's default.
func ResolveModelName(provider, configuredModel string) string {
	model := strings.TrimSpace(configuredModel)
	if model != "" {
		return model
	}
	if def := DefaultModelForProvider(provider); def != "" {
		return def
	}
	return "unknown"
}
