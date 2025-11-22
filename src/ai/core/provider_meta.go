package core

import (
	"strings"
)

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

