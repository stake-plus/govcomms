package sonnet45

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
	defaultModel       = "claude-sonnet-4-5"
	anthropicEndpoint  = "https://api.anthropic.com/v1/messages"
	defaultMaxTokens   = 2048
	defaultTemperature = 0.1
	requestTimeout     = 90 * time.Second
)

func init() {
	core.RegisterProvider("sonnet45", newClient)
}

type client struct {
	apiKey     string
	httpClient *http.Client
	defaults   core.Options
}

func newClient(cfg core.FactoryConfig) (core.Client, error) {
	if cfg.ClaudeKey == "" {
		return nil, fmt.Errorf("sonnet-4.5: Claude API key not configured")
	}

	return &client{
		apiKey:     cfg.ClaudeKey,
		httpClient: webclient.NewDefault(requestTimeout),
		defaults: core.Options{
			Model:               valueOrDefault(cfg.Model, defaultModel),
			Temperature:         orFloat(cfg.Temperature, defaultTemperature),
			MaxCompletionTokens: orInt(cfg.MaxCompletionTokens, defaultMaxTokens),
			SystemPrompt:        cfg.SystemPrompt,
			EnableWebSearch:     cfg.Extra["enable_web_search"] == "1",
		},
	}, nil
}

func (c *client) AnswerQuestion(ctx context.Context, content string, question string, opts core.Options) (string, error) {
	merged := c.merge(opts)
	userPrompt := fmt.Sprintf("Proposal Content:\n%s\n\nQuestion: %s\n\nProvide a precise, concise answer grounded in the provided material.", content, question)
	return c.invoke(ctx, merged, userPrompt, nil)
}

func (c *client) Respond(ctx context.Context, input string, tools []core.Tool, opts core.Options) (string, error) {
	merged := c.merge(opts)
	if shouldEnableWebSearch(merged, tools) {
		input = "You may use browsing/search tools if the environment allows it before replying.\n\n" + input
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

	if shouldEnableWebSearch(opts, tools) {
		body["metadata"] = map[string]interface{}{
			"web_search_hint": true,
		}
	}

	bodyBytes, _ := json.Marshal(body)
	_, payload, err := webclient.DoWithRetry(ctx, 3, 2*time.Second, func() (int, []byte, error) {
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
			return resp.StatusCode, b, fmt.Errorf("sonnet-4.5: status %d", resp.StatusCode)
		}
		return resp.StatusCode, b, nil
	})
	if err != nil {
		return "", err
	}

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(payload, &result); err != nil {
		return "", fmt.Errorf("sonnet-4.5: parse error: %w", err)
	}

	text := extractText(result.Content)
	if text == "" {
		return "", fmt.Errorf("sonnet-4.5: empty response")
	}
	return text, nil
}

func (c *client) merge(opts core.Options) core.Options {
	out := c.defaults
	if strings.TrimSpace(opts.Model) != "" {
		out.Model = opts.Model
	}
	if opts.Temperature != 0 {
		out.Temperature = opts.Temperature
	}
	if opts.MaxCompletionTokens != 0 {
		out.MaxCompletionTokens = opts.MaxCompletionTokens
	}
	if strings.TrimSpace(opts.SystemPrompt) != "" {
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

func extractText(chunks []struct {
	Type string `json:"type"`
	Text string `json:"text"`
}) string {
	var builder strings.Builder
	for _, chunk := range chunks {
		if strings.TrimSpace(chunk.Text) == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString(chunk.Text)
	}
	return strings.TrimSpace(builder.String())
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
