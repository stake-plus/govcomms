package core

import (
	"fmt"
	"strings"
	"sync"
)

// FactoryConfig captures the inputs required to construct a provider client.
type FactoryConfig struct {
	Provider string

	SystemPrompt        string
	Model               string
	Temperature         float64
	MaxCompletionTokens int

	OpenAIKey   string
	ClaudeKey   string
	GeminiKey   string
	DeepSeekKey string
	GrokKey     string

	Extra map[string]string
}

// ProviderFactory implements provider-specific Client creation.
type ProviderFactory func(FactoryConfig) (Client, error)

var (
	mu         sync.RWMutex
	providers  = map[string]ProviderFactory{}
	defaultKey = "openai"
)

// RegisterProvider registers a provider factory under one or more names.
func RegisterProvider(name string, factory ProviderFactory, aliases ...string) {
	mu.Lock()
	defer mu.Unlock()

	all := append([]string{name}, aliases...)
	for _, n := range all {
		providers[strings.ToLower(n)] = factory
	}
}

// NewClient returns a provider-agnostic AI client.
func NewClient(cfg FactoryConfig) (Client, error) {
	providerName := cfg.Provider
	if strings.TrimSpace(providerName) == "" {
		providerName = defaultKey
	}

	mu.RLock()
	factory := providers[strings.ToLower(providerName)]
	mu.RUnlock()

	if factory == nil {
		return nil, fmt.Errorf("ai: provider %q not registered", providerName)
	}
	return factory(cfg)
}
