package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Client struct {
	baseURL string
	client  *http.Client
}

func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

type MessageRequest struct {
	ProposalRef string   `json:"proposalRef"`
	Body        string   `json:"body"`
	Emails      []string `json:"emails"`
}

func (c *Client) PostMessage(proposalRef, body string) error {
	url := fmt.Sprintf("%s/v1/messages", c.baseURL)

	payload := MessageRequest{
		ProposalRef: proposalRef,
		Body:        body,
		Emails:      []string{},
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	// TODO: Add authentication when implemented

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	return nil
}
