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

	headers := map[string]string{
		"Content-Type":  "application/json",
		"Authorization": fmt.Sprintf("Bearer %s", c.loginData.Token),
		"x-network":     network,
	}

	// Use the v1 API format
	body := map[string]interface{}{
		"content": content,
	}

	// Log request details
	bodyJSON, _ := json.MarshalIndent(body, "", "  ")
	log.Printf("Polkassembly: Request body:\n%s", string(bodyJSON))

	// Use the correct v1 endpoint format
	endpoint := fmt.Sprintf("/v1/posts/%d/comment", postID)

	respBody, err := c.postComment(endpoint, body, headers, network)
	if err != nil {
		// Enhanced error logging
		if httpErr, ok := err.(*HTTPError); ok {
			log.Printf("Polkassembly: HTTP Error %d, Body: %s",
				httpErr.StatusCode, string(httpErr.Body))
			// Try to parse error response
			var errResp struct {
				Error   string `json:"error"`
				Message string `json:"message"`
				Errors  []struct {
					Field   string `json:"field"`
					Message string `json:"message"`
				} `json:"errors"`
			}
			if json.Unmarshal(httpErr.Body, &errResp) == nil {
				if errResp.Error != "" {
					return fmt.Errorf("post comment failed: %s", errResp.Error)
				}
				if errResp.Message != "" {
					return fmt.Errorf("post comment failed: %s", errResp.Message)
				}
				if len(errResp.Errors) > 0 {
					return fmt.Errorf("post comment failed: %s - %s",
						errResp.Errors[0].Field, errResp.Errors[0].Message)
				}
			}
		}
		return fmt.Errorf("post comment failed: %w", err)
	}

	// Log the response to see what we got back
	log.Printf("Polkassembly: Response body: %s", string(respBody))

	// Try to parse the response to get comment ID or any other info
	var commentResp map[string]interface{}
	if err := json.Unmarshal(respBody, &commentResp); err == nil {
		log.Printf("Polkassembly: Comment response: %+v", commentResp)
		if commentID, ok := commentResp["id"]; ok {
			log.Printf("Polkassembly: Comment ID: %v", commentID)
		}
		if commentURL, ok := commentResp["url"]; ok {
			log.Printf("Polkassembly: Comment URL: %v", commentURL)
		}
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

// fetchReferendumTrack fetches the track number for a referendum
func (c *Client) fetchReferendumTrack(refID int, network string) (int, error) {
	url := fmt.Sprintf("%s/posts/on-chain-post?postId=%d&proposalType=referendums_v2",
		c.endpoint, refID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("x-network", network)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	log.Printf("Polkassembly: Track response for ref %d: %s", refID, string(body))

	var result struct {
		TrackNumber int `json:"track_number"`
		TrackNo     int `json:"trackNo"`
		Track       int `json:"track"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return 0, err
	}

	// Try different field names
	if result.TrackNumber != 0 {
		return result.TrackNumber, nil
	}
	if result.TrackNo != 0 {
		return result.TrackNo, nil
	}
	if result.Track != 0 {
		return result.Track, nil
	}

	return 0, nil
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

// postComment makes a POST request specifically for comments with network-specific URL
func (c *Client) postComment(path string, body interface{}, headers map[string]string, network string) ([]byte, error) {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal body: %w", err)
	}

	// Construct network-specific URL for comments
	baseURL := fmt.Sprintf("https://%s.polkassembly.io/api", network)
	fullURL := baseURL + path

	req, err := http.NewRequest("POST", fullURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	// Log request details
	log.Printf("Polkassembly: POST %s", fullURL)
	log.Printf("Polkassembly: Headers: %v", req.Header)
	log.Printf("Polkassembly: Body: %s", string(jsonBody))

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
