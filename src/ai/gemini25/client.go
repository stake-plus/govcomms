package gemini

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
	baseURL          = "https://generativelanguage.googleapis.com/v1beta"
	defaultModelName = "gemini-2.5-flash"
	defaultMaxTokens = 2048
)

func init() {
	core.RegisterProvider("gemini25", newClient)
}

type client struct {
	apiKey     string
	httpClient *http.Client
	defaults   core.Options
}

func newClient(cfg core.FactoryConfig) (core.Client, error) {
	if cfg.GeminiKey == "" {
		return nil, fmt.Errorf("gemini: API key not configured")
	}

	model := cfg.Model
	if strings.TrimSpace(model) == "" {
		model = defaultModelName
	}

	return &client{
		apiKey:     cfg.GeminiKey,
		httpClient: webclient.NewDefault(120 * time.Second),
		defaults: core.Options{
			Model:               model,
			Temperature:         orFloat(cfg.Temperature, 0.2),
			MaxCompletionTokens: orInt(cfg.MaxCompletionTokens, defaultMaxTokens),
			SystemPrompt:        cfg.SystemPrompt,
		},
	}, nil
}

func (c *client) AnswerQuestion(ctx context.Context, content string, question string, opts core.Options) (string, error) {
	merged := c.merge(opts)
	userText := fmt.Sprintf("Proposal Content:\n%s\n\nQuestion: %s\n\nProvide a direct, concise answer grounded in the provided context. Use web search only if the answer requires up-to-date information.", content, question)
	body := c.buildRequestBody(merged, userText, false)
	return c.send(ctx, merged.Model, body)
}

func (c *client) Respond(ctx context.Context, input string, tools []core.Tool, opts core.Options) (string, error) {
	merged := c.merge(opts)
	body := c.buildRequestBody(merged, input, hasWebSearch(merged, tools))
	return c.send(ctx, merged.Model, body)
}

func (c *client) buildRequestBody(opts core.Options, userText string, enableSearch bool) map[string]interface{} {
	content := map[string]interface{}{
		"role": "user",
		"parts": []map[string]string{
			{"text": userText},
		},
	}

	body := map[string]interface{}{
		"contents": []map[string]interface{}{content},
		"generationConfig": map[string]interface{}{
			"temperature":     opts.Temperature,
			"maxOutputTokens": maxTokens(opts.MaxCompletionTokens),
		},
	}

	if strings.TrimSpace(opts.SystemPrompt) != "" {
		body["systemInstruction"] = map[string]interface{}{
			"parts": []map[string]string{
				{"text": opts.SystemPrompt},
			},
		}
	}

	if enableSearch {
		body["toolConfig"] = map[string]interface{}{
			"googleSearchRetrieval": map[string]interface{}{},
		}
	}

	return body
}

func (c *client) send(ctx context.Context, model string, payload map[string]interface{}) (string, error) {
	modelPath := normalizeModel(model)
	url := fmt.Sprintf("%s/%s:generateContent?key=%s", baseURL, modelPath, c.apiKey)
	bodyBytes, _ := json.Marshal(payload)

	_, body, err := webclient.DoWithRetry(ctx, 3, 2*time.Second, func() (int, []byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(bodyBytes))
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Content-Type", "application/json")
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
		return "", fmt.Errorf("gemini API error: %w", err)
	}

	var result generateContentResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	text := result.FirstText()
	if text == "" {
		return "", fmt.Errorf("gemini: empty response")
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

func normalizeModel(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return defaultModelName
	}
	if strings.HasPrefix(model, "models/") {
		return model
	}
	return "models/" + model
}

type generateContentResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
}

func (r generateContentResponse) FirstText() string {
	for _, candidate := range r.Candidates {
		for _, part := range candidate.Content.Parts {
			if strings.TrimSpace(part.Text) != "" {
				return part.Text
			}
		}
	}
	return ""
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
