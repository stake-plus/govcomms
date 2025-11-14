package polkassembly

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultTimeout = 30 * time.Second

// Client represents a Polkassembly API client
type Client struct {
	endpoint   string
	httpClient *http.Client
	signer     Signer
	loginData  *LoginData
	network    string
}

// Comment represents a comment returned by the Polkassembly API.
type Comment struct {
	ID        int    `json:"id"`
	ParentID  *int   `json:"parent_id"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
	User      struct {
		ID       int    `json:"id"`
		Username string `json:"username"`
	} `json:"user"`
}

// ParsedCreatedAt converts the comment timestamp into time.Time when possible.
func (c Comment) ParsedCreatedAt() time.Time {
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.999999",
		"2006-01-02T15:04:05",
	}
	for _, layout := range layouts {
		if ts, err := time.Parse(layout, c.CreatedAt); err == nil {
			return ts
		}
	}
	return time.Time{}
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
func NewClient(endpoint string, signer Signer, network string) *Client {
	return &Client{
		endpoint: endpoint,
		httpClient: &http.Client{
			Timeout: defaultTimeout,
		},
		signer:  signer,
		network: strings.ToLower(strings.TrimSpace(network)),
	}
}

// Login authenticates the user with Polkassembly
func (c *Client) Login() error {
	if c.network == "" {
		return fmt.Errorf("login: network not configured")
	}

	loginStartReq := map[string]string{
		"address": c.signer.Address(),
		"wallet":  "polkadot-js",
	}

	headers := map[string]string{
		"Content-Type": "application/json",
		"x-network":    c.network,
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

	signature, err := c.signer.Sign([]byte(loginStartResp.SignMessage))
	if err != nil {
		return fmt.Errorf("sign message: %w", err)
	}

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
	if c.loginData != nil && c.loginData.Network == "" {
		c.loginData.Network = c.network
	}

	return nil
}

// Signup registers a new user with Polkassembly
func (c *Client) Signup(network string) error {
	if network != "" {
		network = strings.ToLower(strings.TrimSpace(network))
	}
	if network != "" {
		c.network = network
	}
	if c.network == "" {
		return fmt.Errorf("signup: network not specified")
	}

	if c.loginData != nil && c.loginData.Network == network {
		return nil
	}

	if c.loginData != nil && c.loginData.Network != network {
		c.Logout()
	}

	headers := map[string]string{
		"Content-Type": "application/json",
		"x-network":    c.network,
	}

	signupStartReq := map[string]string{
		"address":  c.signer.Address(),
		"multisig": "",
	}

	resp, err := c.postWithStatus("/auth/actions/addressSignupStart", signupStartReq, headers)
	if err != nil {
		if httpErr, ok := err.(*HTTPError); ok && httpErr.StatusCode == http.StatusUnauthorized {
			var errResp struct {
				Message string `json:"message"`
			}
			if json.Unmarshal(httpErr.Body, &errResp) == nil {
				if errResp.Message == "There is already an account associated with this address, you cannot sign-up with this address." {
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

	signature, err := c.signer.Sign([]byte(signupStartResp.SignMessage))
	if err != nil {
		return fmt.Errorf("sign message: %w", err)
	}

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
		Network: c.network,
	}

	return nil
}

// PostComment posts a comment to a referendum
func (c *Client) PostComment(content string, postID int, network string) (int, error) {
	if c.loginData == nil {
		return 0, fmt.Errorf("not logged in")
	}

	network = strings.ToLower(strings.TrimSpace(network))
	if network == "" {
		network = c.network
	}
	if network == "" {
		return 0, fmt.Errorf("post comment: network not specified")
	}
	c.network = network

	if c.loginData.Network != network {
		if err := c.Signup(network); err != nil {
			return 0, fmt.Errorf("switch network failed: %w", err)
		}
	}

	for attempt := 0; attempt < 2; attempt++ {
		headers := map[string]string{
			"Content-Type":  "application/json",
			"Authorization": fmt.Sprintf("Bearer %s", c.loginData.Token),
			"x-network":     network,
		}

		userID, err := c.fetchUserID(network)
		if err != nil {
			return 0, fmt.Errorf("fetch user ID: %w", err)
		}

		body := map[string]interface{}{
			"content":     content,
			"postId":      postID,
			"postType":    "referendums_v2",
			"sentiment":   0,
			"trackNumber": 0,
			"userId":      userID,
		}

		resp, err := c.post("/auth/actions/addPostComment", body, headers)
		if err != nil {
			var httpErr *HTTPError
			if errors.As(err, &httpErr) && (httpErr.StatusCode == http.StatusUnauthorized || httpErr.StatusCode == http.StatusForbidden) && attempt == 0 {
				if signupErr := c.Signup(network); signupErr != nil {
					if loginErr := c.Login(); loginErr != nil {
						return 0, fmt.Errorf("reauthenticate: %w", signupErr)
					}
				}
				continue
			}
			if httpErr != nil && len(httpErr.Body) > 0 {
				msg := strings.TrimSpace(string(httpErr.Body))
				if msg != "" {
					return 0, fmt.Errorf("post comment failed: %w: %s", err, msg)
				}
			}
			return 0, fmt.Errorf("post comment failed: %w", err)
		}

		var commentResp struct {
			Comment struct {
				ID int `json:"id"`
			} `json:"comment"`
			ID int `json:"id"`
		}
		if err := json.Unmarshal(resp, &commentResp); err == nil {
			if commentResp.Comment.ID != 0 {
				return commentResp.Comment.ID, nil
			}
			if commentResp.ID != 0 {
				return commentResp.ID, nil
			}
		}

		return 0, nil
	}

	return 0, fmt.Errorf("post comment failed: unauthorized after retry")
}

// ListComments returns the comments for a referendum post.
func (c *Client) ListComments(postID int, network string) ([]Comment, error) {
	if postID <= 0 {
		return nil, fmt.Errorf("list comments: invalid post id")
	}

	if network != "" {
		network = strings.ToLower(strings.TrimSpace(network))
	}

	if c.network == "" && network != "" {
		c.network = network
	}
	if c.network == "" {
		return nil, fmt.Errorf("list comments: network not specified")
	}

	headers := map[string]string{
		"x-network": c.network,
	}
	if c.loginData != nil && c.loginData.Token != "" {
		headers["Authorization"] = fmt.Sprintf("Bearer %s", c.loginData.Token)
	}

	path := fmt.Sprintf("/comments/listing/on-chain-post?postId=%d&page=1&pageSize=50&proposalType=referendums_v2", postID)
	body, err := c.get(path, headers)
	if err != nil {
		return nil, fmt.Errorf("list comments failed: %w", err)
	}

	var results struct {
		Comments []Comment `json:"comments"`
	}

	if err := json.Unmarshal(body, &results); err != nil {
		var fallback []Comment
		if errAlt := json.Unmarshal(body, &fallback); errAlt == nil {
			results.Comments = fallback
		} else {
			return nil, fmt.Errorf("parse comments response: %w", err)
		}
	}

	for i := range results.Comments {
		results.Comments[i].Content = strings.TrimSpace(results.Comments[i].Content)
	}

	return results.Comments, nil
}

// Logout clears authentication state
func (c *Client) Logout() {
	c.loginData = nil
}

// IsLoggedIn returns whether the client is authenticated
func (c *Client) IsLoggedIn() bool {
	return c.loginData != nil
}

func (c *Client) fetchUserID(network string) (int, error) {
	if network != "" {
		c.network = strings.ToLower(strings.TrimSpace(network))
	}

	if c.loginData == nil || c.loginData.Token == "" {
		return 0, fmt.Errorf("fetch profile: not authenticated")
	}

	if c.network == "" && c.loginData != nil {
		c.network = c.loginData.Network
	}
	if c.network == "" {
		return 0, fmt.Errorf("fetch profile: network not specified")
	}

	url := fmt.Sprintf("%s/auth/data/profileWithAddress?address=%s", c.endpoint, c.signer.Address())

	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return 0, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("x-network", c.network)
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.loginData.Token))

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return 0, fmt.Errorf("fetch profile failed: %w", err)
		}

		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			resp.Body.Close()
			if err := c.Login(); err != nil {
				return 0, fmt.Errorf("reauthenticate: %w", err)
			}
			continue
		}

		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return 0, fmt.Errorf("read response: %w", readErr)
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

	return 0, fmt.Errorf("unable to fetch profile after retry")
}

// HTTPError represents an HTTP error response
type HTTPError struct {
	StatusCode int
	Body       []byte
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("HTTP %d", e.StatusCode)
}

func (c *Client) post(path string, body interface{}, headers map[string]string) ([]byte, error) {
	return c.postWithStatus(path, body, headers)
}

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

func (c *Client) get(path string, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequest("GET", c.endpoint+path, nil)
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
