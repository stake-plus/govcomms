package grok

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
	apiURL           = "https://api.x.ai/v1/chat/completions"
	defaultModel     = "grok-4-fast-reasoning"
	defaultMaxTokens = 16000
)

func init() {
	core.RegisterProvider("grok4", newClient)
}

type client struct {
	apiKey     string
	httpClient *http.Client
	defaults   core.Options
}

func newClient(cfg core.FactoryConfig) (core.Client, error) {
	if cfg.GrokKey == "" {
		return nil, fmt.Errorf("grok: API key not configured")
	}

	return &client{
		apiKey:     cfg.GrokKey,
		httpClient: webclient.NewDefault(240 * time.Second),
		defaults: core.Options{
			Model:               valueOrDefault(cfg.Model, defaultModel),
			Temperature:         orFloat(cfg.Temperature, 1.0),
			MaxCompletionTokens: orInt(cfg.MaxCompletionTokens, defaultMaxTokens),
			SystemPrompt:        cfg.SystemPrompt,
		},
	}, nil
}

func (c *client) AnswerQuestion(ctx context.Context, content string, question string, opts core.Options) (string, error) {
	merged := c.merge(opts)
	userPrompt := fmt.Sprintf("Proposal Content:\n%s\n\nQuestion: %s\n\nProvide a concise answer. Use the Grok internet tool if you need real-time information.", content, question)
	body := c.buildRequest(merged, userPrompt, false)
	return c.send(ctx, body)
}

func (c *client) Respond(ctx context.Context, input string, tools []core.Tool, opts core.Options) (string, error) {
	merged := c.merge(opts)
	body := c.buildRequest(merged, input, hasWebSearch(merged, tools))
	return c.send(ctx, body)
}

func (c *client) buildRequest(opts core.Options, userPrompt string, enableWeb bool) map[string]interface{} {
	messages := []map[string]string{}
	if strings.TrimSpace(opts.SystemPrompt) != "" {
		messages = append(messages, map[string]string{
			"role":    "system",
			"content": opts.SystemPrompt,
		})
	}
	messages = append(messages, map[string]string{
		"role":    "user",
		"content": userPrompt,
	})

	body := map[string]interface{}{
		"model":             opts.Model,
		"messages":          messages,
		"temperature":       opts.Temperature,
		"max_output_tokens": maxTokens(opts.MaxCompletionTokens),
		"stream":            false,
		"n":                 1,
		"top_p":             0.9,
	}

	if enableWeb {
		body["tools"] = []map[string]string{
			{"type": "internet"},
		}
		body["tool_choice"] = "auto"
	}

	return body
}

func (c *client) send(ctx context.Context, payload map[string]interface{}) (string, error) {
	bodyBytes, _ := json.Marshal(payload)
	_, body, err := webclient.DoWithRetry(ctx, 3, 2*time.Second, func() (int, []byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewBuffer(bodyBytes))
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
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
			return resp.StatusCode, b, fmt.Errorf("status %d: %s", resp.StatusCode, truncateErrorBody(b))
		}
		return resp.StatusCode, b, nil
	})
	if err != nil {
		return "", fmt.Errorf("grok API error: %w", err)
	}

	var result completionResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	text := result.FirstMessage()
	if text == "" {
		return "", fmt.Errorf("grok: empty response")
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
	return out
}

func hasWebSearch(opts core.Options, tools []core.Tool) bool {
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

func maxTokens(requested int) int {
	if requested <= 0 {
		return defaultMaxTokens
	}
	return requested
}

func truncateErrorBody(body []byte) string {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return "no response body"
	}
	const limit = 300
	if len(text) > limit {
		return text[:limit] + "..."
	}
	return text
}

type completionResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func (r completionResponse) FirstMessage() string {
	for _, choice := range r.Choices {
		content := strings.TrimSpace(choice.Message.Content)
		if content != "" {
			return content
		}
	}
	return ""
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
