package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/stake-plus/govcomms/src/ai/core"
	"github.com/stake-plus/govcomms/src/webclient"
)

const (
	anthropicEndpoint = "https://api.anthropic.com/v1/messages"
	defaultMaxTokens  = 1024
)

type client struct {
	apiKey     string
	httpClient *http.Client
	defaults   core.Options
}

// NewClient constructs an Anthropic-backed implementation of core.Client with the
// provided default model name.
func NewClient(cfg core.FactoryConfig, defaultModel string) (core.Client, error) {
	if cfg.ClaudeKey == "" {
		return nil, fmt.Errorf("anthropic: API key not configured")
	}

	return &client{
		apiKey:     cfg.ClaudeKey,
		httpClient: webclient.NewDefault(60 * time.Second),
		defaults: core.Options{
			Model:               valueOrDefault(cfg.Model, defaultModel),
			Temperature:         orFloat(cfg.Temperature, 0.2),
			MaxCompletionTokens: orInt(cfg.MaxCompletionTokens, defaultMaxTokens),
			SystemPrompt:        cfg.SystemPrompt,
		},
	}, nil
}

func (c *client) AnswerQuestion(ctx context.Context, content string, question string, opts core.Options) (string, error) {
	merged := c.merge(opts)
	userPrompt := fmt.Sprintf("Proposal Content:\n%s\n\nQuestion: %s\n\nProvide a direct, concise answer grounded only in the provided material unless instructed otherwise. If current information is required and web search is available in your environment, use it before responding.", content, question)
	return c.invoke(ctx, merged, userPrompt, nil)
}

func (c *client) Respond(ctx context.Context, input string, tools []core.Tool, opts core.Options) (string, error) {
	merged := c.merge(opts)
	if shouldEnableWebSearch(merged, tools) {
		input = "You may call web search or browsing capabilities if they are available in this environment before answering.\n\n" + input
	}
	return c.invoke(ctx, merged, input, tools)
}

func (c *client) invoke(ctx context.Context, opts core.Options, input string, tools []core.Tool) (string, error) {
	maxTokens := opts.MaxCompletionTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}

	body := map[string]interface{}{
		"model":       opts.Model,
		"system":      opts.SystemPrompt,
		"max_tokens":  maxTokens,
		"temperature": opts.Temperature,
		"messages": []map[string]interface{}{
			{
				"role": "user",
				"content": []map[string]string{
					{"type": "text", "text": input},
				},
			},
		},
	}

	// Anthropic's public API requires explicit tool definitions which in turn
	// require multi-turn orchestration. Until we add full tool execution we
	// annotate the request so the model knows it can opt into search/browsing
	// internally when available.
	if shouldEnableWebSearch(opts, tools) {
		body["metadata"] = map[string]any{
			"govcomms_web_search_hint": true,
		}
	}

	respBody, err := c.post(ctx, body)
	if err != nil {
		return "", err
	}

	text := extractText(respBody.Content)
	if text == "" {
		return "", fmt.Errorf("anthropic: empty response")
	}
	return text, nil
}

func (c *client) post(ctx context.Context, payload map[string]interface{}) (*anthropicResponse, error) {
	bodyBytes, _ := json.Marshal(payload)
	_, body, err := webclient.DoWithRetry(ctx, 3, 2*time.Second, func() (int, []byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicEndpoint, bytes.NewBuffer(bodyBytes))
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", c.apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return resp.StatusCode, nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return resp.StatusCode, b, fmt.Errorf("status %d", resp.StatusCode)
		}
		return resp.StatusCode, b, nil
	})
	if err != nil {
		return nil, fmt.Errorf("anthropic API error: %w", err)
	}

	var result anthropicResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *client) merge(opts core.Options) core.Options {
	out := c.defaults
	if opts.Model != "" {
		out.Model = opts.Model
	}
	if opts.Temperature != 0 {
		out.Temperature = opts.Temperature
	}
	if opts.MaxCompletionTokens != 0 {
		out.MaxCompletionTokens = opts.MaxCompletionTokens
	}
	if opts.SystemPrompt != "" {
		out.SystemPrompt = opts.SystemPrompt
	}
	if opts.EnableWebSearch {
		out.EnableWebSearch = true
	}
	if opts.EnableDeepSearch {
		out.EnableDeepSearch = true
	}
	return out
}

func shouldEnableWebSearch(opts core.Options, tools []core.Tool) bool {
	if opts.EnableWebSearch {
		return true
	}
	for _, tool := range tools {
		if strings.EqualFold(tool.Type, "web_search") {
			return true
		}
	}
	return false
}

func extractText(chunks []anthropicContent) string {
	var b strings.Builder
	for _, chunk := range chunks {
		if chunk.Text != "" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(chunk.Text)
		}
	}
	return strings.TrimSpace(b.String())
}

type anthropicContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anthropicResponse struct {
	Content []anthropicContent `json:"content"`
}

func valueOrDefault(val, def string) string {
	if strings.TrimSpace(val) != "" {
		return val
	}
	return def
}

func orInt(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}

func orFloat(v, def float64) float64 {
	if v != 0 {
		return v
	}
	return def
}
