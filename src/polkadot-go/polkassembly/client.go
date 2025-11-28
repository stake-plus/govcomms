package polkassembly

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"strconv"
	"strings"
	"sync"
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
	stateMu    sync.RWMutex
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
	jar, _ := cookiejar.New(nil)

	return &Client{
		endpoint: endpoint,
		httpClient: &http.Client{
			Timeout: defaultTimeout,
			Jar:     jar,
		},
		signer:  signer,
		network: strings.ToLower(strings.TrimSpace(network)),
	}
}

// Login authenticates the user with Polkassembly
func (c *Client) Login() error {
	network := c.getNetwork()
	if network == "" {
		return fmt.Errorf("login: network not configured")
	}

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

	var loginResp LoginData
	if err := json.Unmarshal(resp, &loginResp); err != nil {
		return fmt.Errorf("parse login response: %w", err)
	}
	if loginResp.Network == "" {
		loginResp.Network = network
	}
	c.setLoginData(&loginResp)

	return nil
}

// Signup registers a new user with Polkassembly
func (c *Client) Signup(network string) error {
	if trimmed := strings.ToLower(strings.TrimSpace(network)); trimmed != "" {
		c.setNetwork(trimmed)
	}

	targetNetwork := c.getNetwork()
	if targetNetwork == "" {
		return fmt.Errorf("signup: network not specified")
	}

	loginData := c.getLoginData()
	if loginData != nil && strings.EqualFold(strings.TrimSpace(loginData.Network), targetNetwork) {
		return nil
	}

	if loginData != nil && !strings.EqualFold(strings.TrimSpace(loginData.Network), targetNetwork) {
		c.Logout()
	}

	headers := map[string]string{
		"Content-Type": "application/json",
		"x-network":    targetNetwork,
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

	if signupResp.Token != "" {
		c.setLoginData(&LoginData{
			Token:   signupResp.Token,
			Network: targetNetwork,
		})
		return nil
	}

	// Some API responses omit the token; follow up with a login to acquire one.
	return c.Login()
}

// PostComment posts a comment to a referendum
func (c *Client) PostComment(content string, postID int, network string) (string, error) {
	loginData := c.getLoginData()
	if loginData == nil {
		return "", fmt.Errorf("not logged in")
	}

	if trimmed := strings.ToLower(strings.TrimSpace(network)); trimmed != "" {
		c.setNetwork(trimmed)
	}
	targetNetwork := c.getNetwork()
	if targetNetwork == "" {
		return "", fmt.Errorf("post comment: network not specified")
	}

	if !strings.EqualFold(strings.TrimSpace(loginData.Network), targetNetwork) {
		if err := c.Signup(targetNetwork); err != nil {
			return "", fmt.Errorf("switch network failed: %w", err)
		}
		loginData = c.getLoginData()
		if loginData == nil {
			return "", fmt.Errorf("post comment: authentication missing after signup")
		}
		// After signup, wait a moment for the profile to be created
		time.Sleep(1 * time.Second)
	}

	for attempt := 0; attempt < 3; attempt++ {
		headers := map[string]string{
			"Content-Type":  "application/json",
			"Authorization": fmt.Sprintf("Bearer %s", loginData.Token),
			"x-network":     targetNetwork,
		}

		userID, err := c.fetchUserID(targetNetwork)
		if err != nil {
			// If profile doesn't exist yet after signup, wait and retry
			if attempt < 2 && strings.Contains(err.Error(), "profile not found") {
				log.Printf("polkassembly: profile not found, waiting and retrying (attempt %d)", attempt+1)
				time.Sleep(2 * time.Second)
				// Re-authenticate in case token expired
				if reauthErr := c.Login(); reauthErr != nil {
					log.Printf("polkassembly: reauthentication failed: %v", reauthErr)
				} else {
					loginData = c.getLoginData()
					if loginData == nil {
						return "", fmt.Errorf("post comment: authentication missing after retry")
					}
				}
				continue
			}
			return "", fmt.Errorf("fetch user ID: %w", err)
		}

		if userID == 0 {
			return "", fmt.Errorf("fetch user ID: user_id is 0")
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
			logCtx := fmt.Sprintf("status=%v", "unknown")
			if errors.As(err, &httpErr) {
				logCtx = fmt.Sprintf("status=%d body=%s", httpErr.StatusCode, strings.TrimSpace(string(httpErr.Body)))
			}
			log.Printf("polkassembly: addPostComment failed (%s)", logCtx)
			if errors.As(err, &httpErr) && (httpErr.StatusCode == http.StatusUnauthorized || httpErr.StatusCode == http.StatusForbidden) && attempt == 0 {
				c.Logout()
				if signupErr := c.Signup(targetNetwork); signupErr != nil {
					if loginErr := c.Login(); loginErr != nil {
						return "", fmt.Errorf("reauthenticate: %w", signupErr)
					}
				}
				loginData = c.getLoginData()
				if loginData == nil {
					return "", fmt.Errorf("reauthenticate: missing login data")
				}
				continue
			}
			if httpErr != nil && len(httpErr.Body) > 0 {
				msg := strings.TrimSpace(string(httpErr.Body))
				if msg != "" {
					return "", fmt.Errorf("post comment failed: %w: %s", err, msg)
				}
			}
			return "", fmt.Errorf("post comment failed: %w", err)
		}

		var commentResp struct {
			Comment struct {
				ID int `json:"id"`
			} `json:"comment"`
			ID int `json:"id"`
		}
		if err := json.Unmarshal(resp, &commentResp); err == nil {
			if commentResp.Comment.ID != 0 {
				return strconv.Itoa(commentResp.Comment.ID), nil
			}
			if commentResp.ID != 0 {
				return strconv.Itoa(commentResp.ID), nil
			}
		}

		if id := extractCommentID(resp); id != "" {
			return id, nil
		}

		log.Printf("polkassembly: post comment response without ID: %s", strings.TrimSpace(string(resp)))
		return "", fmt.Errorf("post comment: missing comment id in response")
	}

	return "", fmt.Errorf("post comment failed: unauthorized after retry")
}

// ListComments returns the comments for a referendum post.
func (c *Client) ListComments(postID int, network string) ([]Comment, error) {
	if postID <= 0 {
		return nil, fmt.Errorf("list comments: invalid post id")
	}

	if trimmed := strings.ToLower(strings.TrimSpace(network)); trimmed != "" {
		c.setNetwork(trimmed)
	}

	currentNetwork := c.getNetwork()
	if currentNetwork == "" {
		return nil, fmt.Errorf("list comments: network not specified")
	}

	headers := map[string]string{
		"x-network": currentNetwork,
	}
	if loginData := c.getLoginData(); loginData != nil && strings.TrimSpace(loginData.Token) != "" {
		headers["Authorization"] = fmt.Sprintf("Bearer %s", loginData.Token)
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
	c.setLoginData(nil)
}

// IsLoggedIn returns whether the client is authenticated
func (c *Client) IsLoggedIn() bool {
	return c.getLoginData() != nil
}

func (c *Client) getNetwork() string {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.network
}

func (c *Client) setNetwork(network string) {
	normalized := strings.ToLower(strings.TrimSpace(network))
	if normalized == "" {
		return
	}
	c.stateMu.Lock()
	c.network = normalized
	c.stateMu.Unlock()
}

func (c *Client) getLoginData() *LoginData {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	if c.loginData == nil {
		return nil
	}
	clone := *c.loginData
	return &clone
}

func (c *Client) setLoginData(data *LoginData) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if data == nil {
		c.loginData = nil
		return
	}
	clone := *data
	c.loginData = &clone
}

func (c *Client) fetchUserID(network string) (int, error) {
	if trimmed := strings.ToLower(strings.TrimSpace(network)); trimmed != "" {
		c.setNetwork(trimmed)
	}

	loginData := c.getLoginData()
	if loginData == nil || strings.TrimSpace(loginData.Token) == "" {
		return 0, fmt.Errorf("fetch profile: not authenticated")
	}

	currentNetwork := c.getNetwork()
	if currentNetwork == "" && loginData != nil && strings.TrimSpace(loginData.Network) != "" {
		c.setNetwork(loginData.Network)
		currentNetwork = c.getNetwork()
	}
	if currentNetwork == "" {
		return 0, fmt.Errorf("fetch profile: network not specified")
	}

	url := fmt.Sprintf("%s/auth/data/profileWithAddress?address=%s", c.endpoint, c.signer.Address())

	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return 0, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("x-network", currentNetwork)
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", loginData.Token))

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return 0, fmt.Errorf("fetch profile failed: %w", err)
		}

		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			resp.Body.Close()
			if err := c.Login(); err != nil {
				return 0, fmt.Errorf("reauthenticate: %w", err)
			}
			loginData = c.getLoginData()
			if loginData == nil {
				return 0, fmt.Errorf("reauthenticate: missing login data")
			}
			currentNetwork = c.getNetwork()
			continue
		}

		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return 0, fmt.Errorf("read response: %w", readErr)
		}

		if resp.StatusCode == http.StatusNotFound {
			// User profile doesn't exist yet - this can happen right after signup
			// Return an error so caller can handle it appropriately
			return 0, fmt.Errorf("user profile not found - may need to complete signup")
		}

		if resp.StatusCode != http.StatusOK {
			log.Printf("polkassembly: fetch profile unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
			return 0, fmt.Errorf("unexpected status: %d", resp.StatusCode)
		}

		var profile struct {
			UserID int `json:"user_id"`
		}
		if err := json.Unmarshal(body, &profile); err != nil {
			log.Printf("polkassembly: failed to parse profile response: %v, body: %s", err, strings.TrimSpace(string(body)))
			return 0, fmt.Errorf("parse profile: %w", err)
		}

		if profile.UserID == 0 {
			return 0, fmt.Errorf("user_id is 0 in profile response")
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

func extractCommentID(body []byte) string {
	var generic interface{}
	if err := json.Unmarshal(body, &generic); err != nil {
		return ""
	}
	return findID(generic)
}

func findID(v interface{}) string {
	switch val := v.(type) {
	case map[string]interface{}:
		if id := numberToString(val["id"]); id != "" {
			return id
		}
		if id := numberToString(val["comment_id"]); id != "" {
			return id
		}
		if id := findID(val["comment"]); id != "" {
			return id
		}
		if id := findID(val["data"]); id != "" {
			return id
		}
	case []interface{}:
		for _, item := range val {
			if id := findID(item); id != "" {
				return id
			}
		}
	}
	return ""
}

func numberToString(v interface{}) string {
	switch val := v.(type) {
	case string:
		val = strings.TrimSpace(val)
		if val != "" {
			return val
		}
	case float64:
		if val > 0 {
			return strconv.Itoa(int(val))
		}
	case int:
		if val > 0 {
			return strconv.Itoa(val)
		}
	}
	return ""
}
