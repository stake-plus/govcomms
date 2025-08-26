package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Client struct {
	apiKey     string
	httpClient *http.Client
}

func NewClient(apiKey string) *Client {
	return &Client{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// ResponseRequest for the new Responses API
type ResponseRequest struct {
	Model               string                   `json:"model"`
	Input               string                   `json:"input"`
	Tools               []map[string]interface{} `json:"tools,omitempty"`
	ToolChoice          interface{}              `json:"tool_choice,omitempty"`
	Temperature         float64                  `json:"temperature,omitempty"`
	MaxCompletionTokens int                      `json:"max_completion_tokens,omitempty"`
}

// ResponseOutput from the Responses API
type ResponseOutput struct {
	Output []struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			Annotations []struct {
				Type  string `json:"type"`
				Title string `json:"title"`
				URL   string `json:"url"`
			} `json:"annotations,omitempty"`
		} `json:"content"`
	} `json:"output"`
}

func (c *Client) CreateResponse(ctx context.Context, request ResponseRequest) (*ResponseOutput, error) {
	// Set defaults if not specified
	if request.Temperature == 0 {
		request.Temperature = 1
	}
	if request.MaxCompletionTokens == 0 {
		request.MaxCompletionTokens = 4000
	}

	jsonBody, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/responses", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errorResp struct {
			Error struct {
				Message string `json:"message"`
				Type    string `json:"type"`
				Code    string `json:"code"`
			} `json:"error"`
		}

		if err := json.Unmarshal(body, &errorResp); err == nil && errorResp.Error.Message != "" {
			return nil, fmt.Errorf("OpenAI API error: %s (type: %s, code: %s)",
				errorResp.Error.Message, errorResp.Error.Type, errorResp.Error.Code)
		}

		return nil, fmt.Errorf("API error: status %d, body: %s", resp.StatusCode, string(body))
	}

	var result ResponseOutput
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &result, nil
}

func (c *Client) CreateResponseWithWebSearch(ctx context.Context, input string) (*ResponseOutput, error) {
	request := ResponseRequest{
		Model: "gpt-5-mini",
		Input: input,
		Tools: []map[string]interface{}{
			{
				"type": "web_search",
			},
		},
		ToolChoice:          "auto",
		Temperature:         1,
		MaxCompletionTokens: 100000,
	}

	return c.CreateResponse(ctx, request)
}

func (c *Client) CreateResponseNoSearch(ctx context.Context, input string) (*ResponseOutput, error) {
	request := ResponseRequest{
		Model:               "gpt-5-mini",
		Input:               input,
		Temperature:         1,
		MaxCompletionTokens: 100000,
	}

	return c.CreateResponse(ctx, request)
}

// CreateResponseWithOptions allows custom parameters
func (c *Client) CreateResponseWithOptions(ctx context.Context, input string, maxTokens int, useWebSearch bool) (*ResponseOutput, error) {
	request := ResponseRequest{
		Model:               "gpt-5-mini",
		Input:               input,
		Temperature:         1,
		MaxCompletionTokens: maxTokens,
	}

	if useWebSearch {
		request.Tools = []map[string]interface{}{
			{"type": "web_search"},
		}
		request.ToolChoice = "auto"
	}

	return c.CreateResponse(ctx, request)
}

// Helper to extract text from response
func (r *ResponseOutput) GetText() string {
	for _, item := range r.Output {
		for _, content := range item.Content {
			if content.Type == "output_text" {
				return content.Text
			}
		}
	}
	return ""
}

// Helper to extract citations/URLs from response
func (r *ResponseOutput) GetCitations() []string {
	seen := make(map[string]bool)
	var citations []string

	for _, item := range r.Output {
		for _, content := range item.Content {
			for _, ann := range content.Annotations {
				if ann.Type == "url_citation" && ann.URL != "" && !seen[ann.URL] {
					citations = append(citations, ann.URL)
					seen[ann.URL] = true
				}
			}
		}
	}
	return citations
}
