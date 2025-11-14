package polkadot

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sync"

	gsrpc "github.com/centrifuge/go-substrate-rpc-client/v4"
	"github.com/centrifuge/go-substrate-rpc-client/v4/types"
	"github.com/centrifuge/go-substrate-rpc-client/v4/types/codec"
)

// Client is a Polkadot RPC client
type Client struct {
	api      *gsrpc.SubstrateAPI
	metadata *types.Metadata

	// Cache for constants
	constantsCache map[string]interface{}
	constantsMu    sync.RWMutex

	// SS58 prefix cache
	ss58Prefix uint16
	ss58Cached bool
}

// NewClient creates a new Polkadot client
func NewClient(url string) (*Client, error) {
	api, err := gsrpc.NewSubstrateAPI(url)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}

	// Get metadata
	meta, err := api.RPC.State.GetMetadataLatest()
	if err != nil {
		return nil, fmt.Errorf("failed to get metadata: %w", err)
	}

	client := &Client{
		api:            api,
		metadata:       meta,
		constantsCache: make(map[string]interface{}),
	}

	// Pre-cache SS58 prefix
	if prefix, err := client.GetSS58Prefix(); err == nil {
		client.ss58Prefix = prefix
		client.ss58Cached = true
	}

	return client, nil
}

// GetCachedSS58Prefix returns the cached SS58 prefix
func (c *Client) GetCachedSS58Prefix() uint16 {
	if c.ss58Cached {
		return c.ss58Prefix
	}
	// Default to generic substrate prefix
	return 42
}

// Close closes the connection
func (c *Client) Close() error {
	// No explicit close needed for gsrpc
	return nil
}

// GetStorage queries storage at a specific key
func (c *Client) GetStorage(key string, at *string) (string, error) {
	var raw types.StorageDataRaw
	var hash types.Hash

	// Decode the hex key
	keyBytes, err := DecodeHex(key)
	if err != nil {
		return "", err
	}
	storageKey := types.NewStorageKey(keyBytes)

	if at != nil {
		err = codec.DecodeFromHex(*at, &hash)
		if err != nil {
			return "", err
		}
		ok, err := c.api.RPC.State.GetStorage(storageKey, &raw, hash)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", nil
		}
	} else {
		ok, err := c.api.RPC.State.GetStorageLatest(storageKey, &raw)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", nil
		}
	}

	return codec.HexEncodeToString(raw), nil
}

// GetStorageRaw returns raw storage response
func (c *Client) GetStorageRaw(key string, at *string) ([]byte, error) {
	data, err := c.GetStorage(key, at)
	if err != nil {
		return nil, err
	}
	return []byte(fmt.Sprintf(`"%s"`, data)), nil
}

// GetStorageAt queries storage at a specific block
func (c *Client) GetStorageAt(key string, blockHash string) (string, error) {
	return c.GetStorage(key, &blockHash)
}

// GetBlockHash gets the block hash at a specific height
func (c *Client) GetBlockHash(height *uint64) (string, error) {
	var hash types.Hash

	if height != nil {
		h, err := c.api.RPC.Chain.GetBlockHash(*height)
		if err != nil {
			return "", err
		}
		hash = h
	} else {
		h, err := c.api.RPC.Chain.GetBlockHashLatest()
		if err != nil {
			return "", err
		}
		hash = h
	}

	return codec.HexEncodeToString(hash[:]), nil
}

// GetHeader gets the block header
func (c *Client) GetHeader(hash *string) (*Header, error) {
	var h types.Hash
	var header *types.Header
	var err error

	if hash != nil {
		err = codec.DecodeFromHex(*hash, &h)
		if err != nil {
			return nil, err
		}
		header, err = c.api.RPC.Chain.GetHeader(h)
	} else {
		header, err = c.api.RPC.Chain.GetHeaderLatest()
	}

	if err != nil {
		return nil, err
	}

	numberBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(numberBytes, uint32(header.Number))

	return &Header{
		Number: codec.HexEncodeToString(numberBytes),
	}, nil
}

// GetKeys gets all storage keys with a specific prefix
func (c *Client) GetKeys(prefix string, at *string) ([]string, error) {
	var keys []types.StorageKey
	var hash types.Hash

	prefixBytes, err := DecodeHex(prefix)
	if err != nil {
		return nil, err
	}

	if at != nil {
		err = codec.DecodeFromHex(*at, &hash)
		if err != nil {
			return nil, err
		}
		k, err := c.api.RPC.State.GetKeys(prefixBytes, hash)
		if err != nil {
			return nil, err
		}
		keys = k
	} else {
		k, err := c.api.RPC.State.GetKeysLatest(prefixBytes)
		if err != nil {
			return nil, err
		}
		keys = k
	}

	result := make([]string, len(keys))
	for i, key := range keys {
		result[i] = codec.HexEncodeToString(key)
	}

	return result, nil
}

// RPC makes a generic RPC call (compatibility method)
func (c *Client) RPC(method string, params []interface{}) (json.RawMessage, error) {
	// This is a compatibility stub
	return nil, fmt.Errorf("generic RPC not implemented, use specific methods")
}

// GetReferendumInfoAt gets referendum info at a specific block
func (c *Client) GetReferendumInfoAt(refID uint32, blockHash string) (*ReferendumInfo, error) {
	// Create storage key for ReferendumInfoFor
	key := createReferendumStorageKey(refID)

	// Query storage at specific block
	var raw types.StorageDataRaw
	var hash types.Hash
	err := codec.DecodeFromHex(blockHash, &hash)
	if err != nil {
		return nil, fmt.Errorf("decode block hash: %w", err)
	}

	storageKey := types.NewStorageKey(key)
	ok, err := c.api.RPC.State.GetStorage(storageKey, &raw, hash)
	if err != nil {
		return nil, fmt.Errorf("get storage at block: %w", err)
	}

	if !ok || len(raw) == 0 {
		return nil, fmt.Errorf("referendum %d does not exist at block %s", refID, blockHash)
	}

	// Decode the referendum data
	info, err := decodeReferendumInfo(raw, refID, c.accountIDToSS58)
	if err != nil {
		// Try legacy format
		info, err = decodeLegacyReferendumInfo(raw, refID, c.accountIDToSS58)
		if err != nil {
			return nil, fmt.Errorf("decode referendum info: %w", err)
		}
	}

	return info, nil
}
