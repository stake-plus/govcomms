package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type ClaudeClient struct {
	apiKey       string
	systemPrompt string
	httpClient   *http.Client
}

func NewClaudeClient(apiKey, systemPrompt string) *ClaudeClient {
	return &ClaudeClient{
		apiKey:       apiKey,
		systemPrompt: systemPrompt,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *ClaudeClient) Ask(content string, question string) (string, error) {
	reqBody := map[string]interface{}{
		"model": "claude-3-opus-20240229",
		"messages": []map[string]string{
			{
				"role":    "user",
				"content": fmt.Sprintf("Proposal Content:\n%s\n\nQuestion: %s", content, question),
			},
		},
		"system":     c.systemPrompt,
		"max_tokens": 1000,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", bytes.NewBuffer(jsonBody))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Claude API error: %s", string(body))
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
		return "", fmt.Errorf("no response from Claude")
	}

	return result.Content[0].Text, nil
}
