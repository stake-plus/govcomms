package polkassembly

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Client struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

type PostCommentRequest struct {
	PostID  int    `json:"post_id"`
	Content string `json:"content"`
	Network string `json:"network"`
}

type PostCommentResponse struct {
	Success bool   `json:"success"`
	ID      int    `json:"id"`
	Error   string `json:"error,omitempty"`
}

// Update NewClient to accept baseURL parameter
func NewClient(apiKey string, baseURL string) *Client {
	if baseURL == "" {
		baseURL = "https://api.polkassembly.io/api/v1"
	}

	return &Client{
		apiKey:  apiKey,
		baseURL: baseURL,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) PostComment(network string, refID int, content string) (*PostCommentResponse, error) {
	url := fmt.Sprintf("%s/auth/comment", c.baseURL)

	reqBody := PostCommentRequest{
		PostID:  refID,
		Content: content,
		Network: network,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result PostCommentResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	if !result.Success {
		return nil, fmt.Errorf("polkassembly error: %s", result.Error)
	}

	return &result, nil
}
