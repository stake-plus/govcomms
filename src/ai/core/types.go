package core

import "context"

// Message represents a single chat turn.
type Message struct {
	Role    string
	Content string
}

// Tool represents a tool capability (e.g., web_search) for providers that support it.
type Tool struct {
	Type string
}

// Options controls model behavior; fields are optional per provider.
type Options struct {
	Model               string
	Temperature         float64
	MaxCompletionTokens int
	SystemPrompt        string
	EnableWebSearch     bool
	EnableDeepSearch    bool
}

// Client is a provider-agnostic interface for LLM operations we need.
type Client interface {
	// AnswerQuestion is a convenience for the ai-qa flow.
	AnswerQuestion(ctx context.Context, content string, question string, opts Options) (string, error)
	// Respond allows passing arbitrary input and optional tools for advanced flows.
	Respond(ctx context.Context, input string, tools []Tool, opts Options) (string, error)
}
