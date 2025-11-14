package sonnet

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/stake-plus/govcomms/src/ai/core"
	"github.com/stake-plus/govcomms/src/webclient"
)

func init() {
	core.RegisterProvider("claude", newClient)
	core.RegisterProvider("sonnet", newClient)
	core.RegisterProvider("anthropic", newClient)
}

type client struct {
	apiKey     string
	httpClient *http.Client
	defaults   core.Options
}

func newClient(cfg core.FactoryConfig) (core.Client, error) {
	if cfg.ClaudeKey == "" {
		return nil, fmt.Errorf("sonnet: Claude/Anthropic API key not configured")
	}

	return &client{
		apiKey:     cfg.ClaudeKey,
		httpClient: webclient.NewDefault(60 * time.Second),
		defaults: core.Options{
			Model:        valueOrDefault(cfg.Model, "claude-3-haiku-20240307"),
			Temperature:  orFloat(cfg.Temperature, 0.2),
			SystemPrompt: cfg.SystemPrompt,
		},
	}, nil
}

func (c *client) AnswerQuestion(ctx context.Context, content string, question string, opts core.Options) (string, error) {
	merged := c.merge(opts)
	reqBody := map[string]interface{}{
		"model": merged.Model,
		"messages": []map[string]string{
			{"role": "user", "content": fmt.Sprintf("Proposal Content:\n%s\n\nQuestion: %s\n\nProvide a direct, concise answer.", content, question)},
		},
		"system":      merged.SystemPrompt,
		"max_tokens":  500,
		"temperature": merged.Temperature,
	}
	bodyBytes, _ := json.Marshal(reqBody)
	_, body, err := webclient.DoWithRetry(ctx, 3, 2*time.Second, func() (int, []byte, error) {
		req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewBuffer(bodyBytes))
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
		return "", fmt.Errorf("sonnet API error: %w", err)
	}
	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	if len(result.Content) == 0 {
		return "", fmt.Errorf("no response from Sonnet/Claude")
	}
	return result.Content[0].Text, nil
}

func (c *client) Respond(ctx context.Context, input string, tools []core.Tool, opts core.Options) (string, error) {
	// Claude Messages API has limited tool support compared to OpenAI; ignore tools for now
	merged := c.merge(opts)
	reqBody := map[string]interface{}{
		"model": merged.Model,
		"messages": []map[string]string{
			{"role": "user", "content": input},
		},
		"system":      merged.SystemPrompt,
		"max_tokens":  1000,
		"temperature": merged.Temperature,
	}
	bodyBytes, _ := json.Marshal(reqBody)
	_, body, err := webclient.DoWithRetry(ctx, 3, 2*time.Second, func() (int, []byte, error) {
		req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewBuffer(bodyBytes))
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
		return "", fmt.Errorf("sonnet API error: %w", err)
	}
	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	if len(result.Content) == 0 {
		return "", fmt.Errorf("no response from Sonnet/Claude")
	}
	return result.Content[0].Text, nil
}

func (c *client) merge(opts core.Options) core.Options {
	out := c.defaults
	if opts.Model != "" {
		out.Model = opts.Model
	}
	if opts.Temperature != 0 {
		out.Temperature = opts.Temperature
	}
	if opts.SystemPrompt != "" {
		out.SystemPrompt = opts.SystemPrompt
	}
	return out
}

func valueOrDefault(val, def string) string {
	if val != "" {
		return val
	}
	return def
}

func orFloat(v, d float64) float64 {
	if v != 0 {
		return v
	}
	return d
}

