package polkassembly

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

const (
	defaultTimeout = 30 * time.Second
)

// Client represents a Polkassembly API client
type Client struct {
	endpoint   string
	httpClient *http.Client
	signer     Signer
	loginData  *LoginData
}

// LoginData holds authentication information
type LoginData struct {
	Token   string `json:"token"`
	Network string `json:"network"`
}

// Signer interface for message signing
type Signer interface {
	Sign(message []byte) ([]byte, error)
	Address() string
}

// NewClient creates a new Polkassembly client
func NewClient(endpoint string, signer Signer) *Client {
	return &Client{
		endpoint: endpoint,
		httpClient: &http.Client{
			Timeout: defaultTimeout,
		},
		signer: signer,
	}
}

// Login authenticates the user with Polkassembly for a specific network
func (c *Client) Login(network string) error {
	log.Printf("Polkassembly: Starting login for address: %s on network: %s", c.signer.Address(), network)

	// Start login process
	loginStartReq := map[string]string{
		"address": c.signer.Address(),
		"wallet":  "polkadot-js",
	}

	headers := map[string]string{
		"Content-Type": "application/json",
		"x-network":    network,
	}

	resp, err := c.post("/auth/actions/addressLoginStart", loginStartReq, headers)
	if err != nil {
		return fmt.Errorf("login start failed: %w", err)
	}

	var loginStartResp struct {
		SignMessage string `json:"signMessage"`
	}
	if err := json.Unmarshal(resp, &loginStartResp); err != nil {
		return fmt.Errorf("parse login start response: %w", err)
	}

	log.Printf("Polkassembly: Received sign message: %s", loginStartResp.SignMessage)

	// Sign the message
	signature, err := c.signer.Sign([]byte(loginStartResp.SignMessage))
	if err != nil {
		return fmt.Errorf("sign message: %w", err)
	}

	// Complete login
	loginReq := map[string]string{
		"address":   c.signer.Address(),
		"multisig":  "",
		"signature": "0x" + hex.EncodeToString(signature),
		"wallet":    "polkadot-js",
	}

	resp, err = c.post("/auth/actions/addressLogin", loginReq, headers)
	if err != nil {
		return fmt.Errorf("login failed: %w", err)
	}

	var loginResp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(resp, &loginResp); err != nil {
		return fmt.Errorf("parse login response: %w", err)
	}

	c.loginData = &LoginData{
		Token:   loginResp.Token,
		Network: network,
	}

	log.Printf("Polkassembly: Login successful, token: %s..., network: %s",
		c.loginData.Token[:10], c.loginData.Network)

	return nil
}

// PostComment posts a comment to a referendum
func (c *Client) PostComment(content string, postID int, network string) error {
	// Ensure we're logged in to the correct network
	if c.loginData == nil || c.loginData.Network != network {
		log.Printf("Polkassembly: Need to login to %s network", network)
		if err := c.Login(network); err != nil {
			return fmt.Errorf("login failed: %w", err)
		}
	}

	// Use the same API version for posting as for login
	path := fmt.Sprintf("/auth/comments/create")

	// Match the exact payload structure from the example
	body := map[string]interface{}{
		"proposalType": "referendum_v2",
		"proposalId":   postID,
		"content":      content,
		"network":      network,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal body: %w", err)
	}

	req, err := http.NewRequest("POST", c.endpoint+path, bytes.NewBuffer(jsonBody))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-network", network)
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.loginData.Token))

	// Log request details
	log.Printf("Polkassembly: POST %s", req.URL.String())
	log.Printf("Polkassembly: Headers: %v", req.Header)
	log.Printf("Polkassembly: Body: %s", string(jsonBody))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	log.Printf("Polkassembly: Response status: %d", resp.StatusCode)
	log.Printf("Polkassembly: Response body: %s", string(respBody))

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(respBody))
	}

	log.Printf("Polkassembly: Successfully posted comment to referendum #%d", postID)
	return nil
}

// Logout clears the authentication data
func (c *Client) Logout() {
	c.loginData = nil
}

// IsLoggedIn returns true if the client is authenticated
func (c *Client) IsLoggedIn() bool {
	return c.loginData != nil
}

// fetchUserID retrieves the user ID for the current address
func (c *Client) fetchUserID(network string) (int, error) {
	url := fmt.Sprintf("%s/auth/data/profileWithAddress?address=%s", c.endpoint, c.signer.Address())

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, err
	}

	req.Header.Set("x-network", network)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("fetch profile failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("unexpected status: %d, body: %s", resp.StatusCode, string(body))
	}

	log.Printf("Polkassembly: Profile response: %s", string(body))

	var profile struct {
		UserID int `json:"user_id"`
	}
	if err := json.Unmarshal(body, &profile); err != nil {
		return 0, fmt.Errorf("parse profile: %w", err)
	}

	return profile.UserID, nil
}

// HTTPError represents an HTTP error response
type HTTPError struct {
	StatusCode int
	Body       []byte
	Message    string
}

func (e *HTTPError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, string(e.Body))
}

// post makes a POST request and returns the response body
func (c *Client) post(path string, body interface{}, headers map[string]string) ([]byte, error) {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal body: %w", err)
	}

	req, err := http.NewRequest("POST", c.endpoint+path, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	// Log request details
	log.Printf("Polkassembly: POST %s", req.URL.String())
	log.Printf("Polkassembly: Headers: %v", req.Header)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	log.Printf("Polkassembly: Response status: %d", resp.StatusCode)

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		log.Printf("Polkassembly: Response body: %s", string(respBody))
		return nil, &HTTPError{
			StatusCode: resp.StatusCode,
			Body:       respBody,
		}
	}

	return respBody, nil
}
