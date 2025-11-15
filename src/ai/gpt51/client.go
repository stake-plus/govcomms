package gpt51

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
	core.RegisterProvider("gpt51", newClient)
}

type client struct {
	apiKey     string
	httpClient *http.Client
	defaults   core.Options
}

func newClient(cfg core.FactoryConfig) (core.Client, error) {
	if cfg.OpenAIKey == "" {
		return nil, fmt.Errorf("gpt51: OpenAI API key not configured")
	}

	return &client{
		apiKey:     cfg.OpenAIKey,
		httpClient: webclient.NewDefault(300 * time.Second),
		defaults: core.Options{
			Model:               valueOrDefault(cfg.Model, "gpt-5.1"),
			Temperature:         orFloat(cfg.Temperature, 1),
			MaxCompletionTokens: orInt(cfg.MaxCompletionTokens, 50000),
			SystemPrompt:        cfg.SystemPrompt,
		},
	}, nil
}

func (c *client) AnswerQuestion(ctx context.Context, content string, question string, opts core.Options) (string, error) {
	// Use Chat Completions
	merged := c.merge(opts)
	messages := []map[string]string{
		{"role": "system", "content": merged.SystemPrompt},
		{"role": "user", "content": fmt.Sprintf("Proposal Content:\n%s\n\nQuestion: %s\n\nProvide a direct, concise answer.", content, question)},
	}
	reqBody := map[string]interface{}{
		"model":       merged.Model,
		"messages":    messages,
		"temperature": merged.Temperature,
	}
	bodyBytes, _ := json.Marshal(reqBody)
	_, body, err := webclient.DoWithRetry(ctx, 3, 2*time.Second, func() (int, []byte, error) {
		req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", bytes.NewBuffer(bodyBytes))
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
			return resp.StatusCode, b, fmt.Errorf("status %d", resp.StatusCode)
		}
		return resp.StatusCode, b, nil
	})
	if err != nil {
		return "", fmt.Errorf("gpt51 API error: %w", err)
	}
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no response from OpenAI")
	}
	return result.Choices[0].Message.Content, nil
}

func (c *client) Respond(ctx context.Context, input string, tools []core.Tool, opts core.Options) (string, error) {
	// Use Responses API with optional tools like web_search
	merged := c.merge(opts)
	payload := map[string]interface{}{
		"model":             merged.Model,
		"input":             input,
		"temperature":       merged.Temperature,
		"max_output_tokens": merged.MaxCompletionTokens,
	}
	if len(tools) > 0 {
		var toolPayload []map[string]interface{}
		for _, t := range tools {
			toolPayload = append(toolPayload, map[string]interface{}{"type": t.Type})
		}
		payload["tools"] = toolPayload
		payload["tool_choice"] = "auto"
	}
	bodyBytes, _ := json.Marshal(payload)
	_, body, err := webclient.DoWithRetry(ctx, 3, 2*time.Second, func() (int, []byte, error) {
		req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/responses", bytes.NewBuffer(bodyBytes))
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
			return resp.StatusCode, b, fmt.Errorf("status %d", resp.StatusCode)
		}
		return resp.StatusCode, b, nil
	})
	if err != nil {
		return "", fmt.Errorf("gpt51 API error: %w", err)
	}
	// Tolerate multiple shapes by extracting text fields
	var result struct {
		Output []struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &result); err == nil {
		for _, o := range result.Output {
			for _, c := range o.Content {
				if c.Text != "" {
					return c.Text, nil
				}
			}
		}
	}
	// Fallback minimal parse for standard responses
	var alt struct {
		OutputText string `json:"output_text"`
	}
	if err := json.Unmarshal(body, &alt); err == nil && alt.OutputText != "" {
		return alt.OutputText, nil
	}
	return "", fmt.Errorf("failed to parse GPT-5 response")
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
	return out
}

func valueOrDefault(val, def string) string {
	if val != "" {
		return val
	}
	return def
}
func orInt(v, d int) int {
	if v != 0 {
		return v
	}
	return d
}
func orFloat(v, d float64) float64 {
	if v != 0 {
		return v
	}
	return d
}
