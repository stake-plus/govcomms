package ai

// Factory inputs to construct a client without leaking provider details.
type FactoryConfig struct {
    Provider     string // "openai" or "claude"
    OpenAIKey    string
    ClaudeKey    string
    SystemPrompt string
    // Defaults
    Model               string
    Temperature         float64
    MaxCompletionTokens int
}

// NewClient returns a provider-agnostic AI client.
func NewClient(cfg FactoryConfig) Client {
    switch cfg.Provider {
    case "claude":
        return newClaudeClient(cfg)
    default:
        return newOpenAIClient(cfg)
    }
}


