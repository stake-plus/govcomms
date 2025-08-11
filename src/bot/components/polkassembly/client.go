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

const defaultTimeout = 120 * time.Second // Increased from 60 to 120 seconds

type Client struct {
	endpoint   string
	httpClient *http.Client
	signer     Signer
	loginData  *LoginData
}

type LoginData struct {
	Token   string `json:"token"`
	Network string `json:"network"`
}

type Signer interface {
	Sign(message []byte) ([]byte, error)
	Address() string
}

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

func (c *Client) Login(network string) error {
	log.Printf("Polkassembly: Starting login for address: %s on network: %s", c.signer.Address(), network)

	baseURL := fmt.Sprintf("https://%s.polkassembly.io", network)
	message := fmt.Sprintf("Sign this message to login to Polkassembly.\n\nNetwork: %s\nAddress: %s\nTimestamp: %d",
		network, c.signer.Address(), time.Now().Unix())

	signature, err := c.signer.Sign([]byte(message))
	if err != nil {
		return fmt.Errorf("sign message: %w", err)
	}

	authReq := map[string]string{
		"address":   c.signer.Address(),
		"signature": "0x" + hex.EncodeToString(signature),
		"wallet":    "polkadot-js",
	}

	jsonBody, err := json.Marshal(authReq)
	if err != nil {
		return fmt.Errorf("marshal auth request: %w", err)
	}

	req, err := http.NewRequest("POST", baseURL+"/api/v2/auth/web3-auth", bytes.NewBuffer(jsonBody))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

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

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("authentication failed: status %d, body: %s", resp.StatusCode, string(respBody))
	}

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

	c.loginData = &LoginData{
		Token:   "session",
		Network: network,
	}

	log.Printf("Polkassembly: Login successful for network: %s", network)
	return nil
}
func (c *Client) PostCommentWithResponse(ctx context.Context, content string, postID int, network string) ([]byte, error) {
	if c.loginData == nil || c.loginData.Network != network {
		log.Printf("Polkassembly: Need to login to %s network", network)
		if err := c.Login(network); err != nil {
			return nil, fmt.Errorf("login failed: %w", err)
		}
	}

	baseURL := fmt.Sprintf("https://%s.polkassembly.io", network)
	path := fmt.Sprintf("/api/v2/ReferendumV2/%d/comments", postID)
	url := baseURL + path

	body := map[string]interface{}{
		"content": content,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal body: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Network", network)

	log.Printf("Polkassembly: POST %s", url)
	log.Printf("Polkassembly: Request body: %s", string(jsonBody))

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
	log.Printf("Polkassembly: Response headers: %v", resp.Header)
	log.Printf("Polkassembly: Response body: %s", string(respBody))

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}
