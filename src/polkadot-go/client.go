package polkadot

import (
	"encoding/json"
	"fmt"
	"sync/atomic"

	"github.com/gorilla/websocket"
)

// Client is a Polkadot RPC client
type Client struct {
	conn      *websocket.Conn
	url       string
	requestID uint64
}

// NewClient creates a new Polkadot client
func NewClient(url string) (*Client, error) {
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}

	return &Client{
		conn: conn,
		url:  url,
	}, nil
}

// Close closes the websocket connection
func (c *Client) Close() error {
	return c.conn.Close()
}

// RPC makes a generic RPC call
func (c *Client) RPC(method string, params []interface{}) (json.RawMessage, error) {
	id := atomic.AddUint64(&c.requestID, 1)

	req := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}

	if err := c.conn.WriteJSON(req); err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	var resp struct {
		Jsonrpc string          `json:"jsonrpc"`
		ID      uint64          `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := c.conn.ReadJSON(&resp); err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("RPC error %d: %s", resp.Error.Code, resp.Error.Message)
	}

	return resp.Result, nil
}

// GetStorage queries storage at a specific key
func (c *Client) GetStorage(key string, at *string) (string, error) {
	params := []interface{}{key}
	if at != nil {
		params = append(params, *at)
	}

	result, err := c.RPC("state_getStorage", params)
	if err != nil {
		return "", err
	}

	var hexData string
	if err := json.Unmarshal(result, &hexData); err != nil {
		// Could be null
		return "", nil
	}

	return hexData, nil
}

// GetStorageAt queries storage at a specific key and block hash
func (c *Client) GetStorageAt(key string, blockHash string) (string, error) {
	return c.GetStorage(key, &blockHash)
}

// GetBlockHash gets the block hash at a specific height
func (c *Client) GetBlockHash(height *uint64) (string, error) {
	params := []interface{}{}
	if height != nil {
		params = append(params, *height)
	}

	result, err := c.RPC("chain_getBlockHash", params)
	if err != nil {
		return "", err
	}

	var hash string
	if err := json.Unmarshal(result, &hash); err != nil {
		return "", fmt.Errorf("failed to parse block hash: %w", err)
	}

	return hash, nil
}

// GetHeader gets the block header
func (c *Client) GetHeader(hash *string) (*Header, error) {
	params := []interface{}{}
	if hash != nil {
		params = append(params, *hash)
	}

	result, err := c.RPC("chain_getHeader", params)
	if err != nil {
		return nil, err
	}

	var header Header
	if err := json.Unmarshal(result, &header); err != nil {
		return nil, fmt.Errorf("failed to parse header: %w", err)
	}

	return &header, nil
}

// GetMetadata gets the runtime metadata
func (c *Client) GetMetadata(at *string) (string, error) {
	params := []interface{}{}
	if at != nil {
		params = append(params, *at)
	}

	result, err := c.RPC("state_getMetadata", params)
	if err != nil {
		return "", err
	}

	var metadata string
	if err := json.Unmarshal(result, &metadata); err != nil {
		return "", fmt.Errorf("failed to parse metadata: %w", err)
	}

	return metadata, nil
}

// GetRuntimeVersion gets the runtime version
func (c *Client) GetRuntimeVersion(at *string) (*RuntimeVersion, error) {
	params := []interface{}{}
	if at != nil {
		params = append(params, *at)
	}

	result, err := c.RPC("state_getRuntimeVersion", params)
	if err != nil {
		return nil, err
	}

	var version RuntimeVersion
	if err := json.Unmarshal(result, &version); err != nil {
		return nil, fmt.Errorf("failed to parse runtime version: %w", err)
	}

	return &version, nil
}

// GetKeys gets all storage keys with a specific prefix
func (c *Client) GetKeys(prefix string, at *string) ([]string, error) {
	params := []interface{}{prefix}
	if at != nil {
		params = append(params, *at)
	}

	result, err := c.RPC("state_getKeys", params)
	if err != nil {
		return nil, err
	}

	var keys []string
	if err := json.Unmarshal(result, &keys); err != nil {
		return nil, fmt.Errorf("failed to parse keys: %w", err)
	}

	return keys, nil
}

// GetStorageSize gets the size of a storage value
func (c *Client) GetStorageSize(key string, at *string) (uint64, error) {
	params := []interface{}{key}
	if at != nil {
		params = append(params, *at)
	}

	result, err := c.RPC("state_getStorageSize", params)
	if err != nil {
		return 0, err
	}

	var size uint64
	if err := json.Unmarshal(result, &size); err != nil {
		return 0, fmt.Errorf("failed to parse storage size: %w", err)
	}

	return size, nil
}

// GetConstant gets a constant value from metadata
func (c *Client) GetConstant(pallet, name string) (string, error) {
	// Constants are stored in metadata, this is a helper that would need metadata parsing
	// For now, return error
	return "", fmt.Errorf("constant queries require metadata parsing - use GetMetadata first")
}
