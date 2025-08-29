package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type OpenAIClient struct {
	apiKey       string
	systemPrompt string
	httpClient   *http.Client
}

func NewOpenAIClient(apiKey, systemPrompt string) *OpenAIClient {
	return &OpenAIClient{
		apiKey:       apiKey,
		systemPrompt: systemPrompt,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

func (c *OpenAIClient) Ask(content string, question string) (string, error) {
	messages := []map[string]string{
		{
			"role":    "system",
			"content": c.systemPrompt,
		},
		{
			"role":    "user",
			"content": fmt.Sprintf("Proposal Content:\n%s\n\nQuestion: %s\n\nProvide a direct, concise answer.", content, question),
		},
	}

	reqBody := map[string]interface{}{
		"model":               "gpt-5",
		"messages":            messages,
		"temperature":         1,
		"max_response_tokens": 50000,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", "https://api.openai.com/v1/chat/completions", bytes.NewBuffer(jsonBody))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

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
		return "", fmt.Errorf("openAI API error: %s", string(body))
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
