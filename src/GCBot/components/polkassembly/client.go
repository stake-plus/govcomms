package polkassembly

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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

// Login authenticates the user with Polkassembly
func (c *Client) Login() error {
	// Start login process
	loginStartReq := map[string]string{
		"address": c.signer.Address(),
		"wallet":  "polkadot-js",
	}

	headers := map[string]string{
		"Content-Type": "application/json",
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

	if err := json.Unmarshal(resp, &c.loginData); err != nil {
		return fmt.Errorf("parse login response: %w", err)
	}

	return nil
}

// Signup registers a new user with Polkassembly
func (c *Client) Signup(network string) error {
	// Check if already logged in to the same network
	if c.loginData != nil && c.loginData.Network == network {
		return nil
	}

	// If logged in to different network, logout first
	if c.loginData != nil && c.loginData.Network != network {
		c.Logout()
	}

	headers := map[string]string{
		"Content-Type": "application/json",
		"x-network":    network,
	}

	signupStartReq := map[string]string{
		"address":  c.signer.Address(),
		"multisig": "",
	}

	resp, err := c.postWithStatus("/auth/actions/addressSignupStart", signupStartReq, headers)
	if err != nil {
		// Check if already registered
		if httpErr, ok := err.(*HTTPError); ok && httpErr.StatusCode == 401 {
			var errResp struct {
				Message string `json:"message"`
			}
			if json.Unmarshal(httpErr.Body, &errResp) == nil {
				if errResp.Message == "There is already an account associated with this address, you cannot sign-up with this address." {
					// Already registered, try to login instead
					return c.Login()
				}
			}
		}
		return fmt.Errorf("signup start failed: %w", err)
	}

	var signupStartResp struct {
		SignMessage string `json:"signMessage"`
	}
	if err := json.Unmarshal(resp, &signupStartResp); err != nil {
		return fmt.Errorf("parse signup start response: %w", err)
	}

	// Sign the message
	signature, err := c.signer.Sign([]byte(signupStartResp.SignMessage))
	if err != nil {
		return fmt.Errorf("sign message: %w", err)
	}

	// Complete signup
	signupReq := map[string]string{
		"address":   c.signer.Address(),
		"multisig":  "",
		"signature": "0x" + hex.EncodeToString(signature),
		"wallet":    "polkadot-js",
	}

	resp, err = c.post("/auth/actions/addressSignupConfirm", signupReq, headers)
	if err != nil {
		return fmt.Errorf("signup confirm failed: %w", err)
	}

	var signupResp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(resp, &signupResp); err != nil {
		return fmt.Errorf("parse signup response: %w", err)
	}

	c.loginData = &LoginData{
		Token:   signupResp.Token,
		Network: network,
	}

	return nil
}

// PostComment posts a comment to a referendum
func (c *Client) PostComment(content string, postID int, network string) error {
	if c.loginData == nil {
		return fmt.Errorf("not logged in")
	}

	// Ensure we're on the right network
	if c.loginData.Network != network {
		if err := c.Signup(network); err != nil {
			return fmt.Errorf("switch network failed: %w", err)
		}
	}

	headers := map[string]string{
		"Content-Type":  "application/json",
		"Authorization": fmt.Sprintf("Bearer %s", c.loginData.Token),
		"x-network":     network,
	}

	// Get user ID
	userID, err := c.fetchUserID()
	if err != nil {
		return fmt.Errorf("fetch user ID: %w", err)
	}

	body := map[string]interface{}{
		"content":     content,
		"postId":      postID,
		"postType":    "referendums_v2",
		"sentiment":   0, // Neutral
		"trackNumber": 0, // Will be determined by the referendum
		"userId":      userID,
	}

	_, err = c.post("/auth/actions/addPostComment", body, headers)
	if err != nil {
		return fmt.Errorf("post comment failed: %w", err)
	}

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
func (c *Client) fetchUserID() (int, error) {
	url := fmt.Sprintf("%s/auth/data/profileWithAddress?address=%s", c.endpoint, c.signer.Address())

	resp, err := c.httpClient.Get(url)
	if err != nil {
		return 0, fmt.Errorf("fetch profile failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

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
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("HTTP %d", e.StatusCode)
}

// post makes a POST request and returns the response body
func (c *Client) post(path string, body interface{}, headers map[string]string) ([]byte, error) {
	return c.postWithStatus(path, body, headers)
}

// postWithStatus makes a POST request and returns the response body, including error responses
func (c *Client) postWithStatus(path string, body interface{}, headers map[string]string) ([]byte, error) {
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

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, &HTTPError{
			StatusCode: resp.StatusCode,
			Body:       respBody,
		}
	}

	return respBody, nil
}
