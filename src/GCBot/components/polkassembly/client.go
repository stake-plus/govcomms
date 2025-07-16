package polkassembly

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"time"
)

const (
	defaultTimeout = 60 * time.Second // Increased from 30s
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
	jar, _ := cookiejar.New(nil)
	return &Client{
		endpoint: endpoint,
		httpClient: &http.Client{
			Timeout: defaultTimeout,
			Jar:     jar,
		},
		signer: signer,
	}
}

// Login authenticates the user with Polkassembly for a specific network
func (c *Client) Login(network string) error {
	log.Printf("Polkassembly: Starting login for address: %s on network: %s", c.signer.Address(), network)

	// Build network-specific URL
	baseURL := fmt.Sprintf("https://%s.polkassembly.io", network)

	// Create a message to sign (they might use a specific format)
	message := fmt.Sprintf("Sign this message to login to Polkassembly.\n\nNetwork: %s\nAddress: %s\nTimestamp: %d",
		network, c.signer.Address(), time.Now().Unix())

	// Sign the message
	signature, err := c.signer.Sign([]byte(message))
	if err != nil {
		return fmt.Errorf("sign message: %w", err)
	}

	// Create the web3-auth request
	authReq := map[string]string{
		"address":   c.signer.Address(),
		"signature": "0x" + hex.EncodeToString(signature),
		"wallet":    "polkadot-js",
	}

	headers := map[string]string{
		"Content-Type": "application/json",
	}

	// Make the authentication request
	jsonBody, err := json.Marshal(authReq)
	if err != nil {
		return fmt.Errorf("marshal auth request: %w", err)
	}

	req, err := http.NewRequest("POST", baseURL+"/api/v2/auth/web3-auth", bytes.NewBuffer(jsonBody))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	log.Printf("Polkassembly: POST %s", req.URL.String())

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

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

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("authentication failed: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	// Check if authentication was successful
	var authResp struct {
		IsTFAEnabled bool   `json:"isTFAEnabled"`
		Message      string `json:"message"`
	}
	if err := json.Unmarshal(respBody, &authResp); err != nil {
		return fmt.Errorf("parse auth response: %w", err)
	}

	if authResp.Message != "Web3 authentication successful" {
		return fmt.Errorf("unexpected auth response: %s", authResp.Message)
	}

	// Store the network for this session
	c.loginData = &LoginData{
		Token:   "session", // Using session cookies instead of JWT
		Network: network,
	}

	log.Printf("Polkassembly: Login successful for network: %s", network)
	return nil
}

// PostComment posts a comment to a referendum with context support
func (c *Client) PostComment(content string, postID int, network string) error {
	return c.PostCommentWithContext(context.Background(), content, postID, network)
}

// PostCommentWithContext posts a comment to a referendum with custom context
func (c *Client) PostCommentWithContext(ctx context.Context, content string, postID int, network string) error {
	// Ensure we're logged in to the correct network
	if c.loginData == nil || c.loginData.Network != network {
		log.Printf("Polkassembly: Need to login to %s network", network)
		if err := c.Login(network); err != nil {
			return fmt.Errorf("login failed: %w", err)
		}
	}

	// Build the URL for v2 API with the network-specific subdomain
	baseURL := fmt.Sprintf("https://%s.polkassembly.io", network)
	path := fmt.Sprintf("/api/v2/ReferendumV2/%d/comments", postID)
	url := baseURL + path

	// Simple payload with just content
	body := map[string]interface{}{
		"content": content,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal body: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	// Add context to request
	req = req.WithContext(ctx)

	// Set headers - no Authorization header needed as we're using cookies
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Network", network)

	// Log request details
	log.Printf("Polkassembly: POST %s", url)

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

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(respBody))
	}

	log.Printf("Polkassembly: Successfully posted comment to referendum #%d", postID)
	return nil
}

// Logout clears the authentication data
func (c *Client) Logout() {
	c.loginData = nil
	// Clear cookies
	if c.httpClient.Jar != nil {
		jar, _ := cookiejar.New(nil)
		c.httpClient.Jar = jar
	}
}

// IsLoggedIn returns true if the client is authenticated
func (c *Client) IsLoggedIn() bool {
	return c.loginData != nil
}

// fetchUserID retrieves the user ID for the current address
func (c *Client) fetchUserID(network string) (int, error) {
	baseURL := fmt.Sprintf("https://%s.polkassembly.io", network)
	url := fmt.Sprintf("%s/api/v2/auth/data/profileWithAddress?address=%s", baseURL, c.signer.Address())

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

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
