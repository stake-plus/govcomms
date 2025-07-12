package polkadot

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"math/big"

	"github.com/centrifuge/go-substrate-rpc-client/v4/scale"
	"github.com/centrifuge/go-substrate-rpc-client/v4/types"
	"github.com/centrifuge/go-substrate-rpc-client/v4/types/codec"
)

// PreimageDecoder handles preimage fetching and decoding
type PreimageDecoder struct {
	client *Client
}

// NewPreimageDecoder creates a new preimage decoder
func NewPreimageDecoder(client *Client) *PreimageDecoder {
	return &PreimageDecoder{client: client}
}

// FetchAndDecodePreimage fetches a preimage and extracts all recipient addresses
func (pd *PreimageDecoder) FetchAndDecodePreimage(hash string, length uint32, blockNumber uint32) ([]string, error) {
	// First try current storage
	preimageData, err := pd.fetchPreimage(hash, length, nil)
	if err != nil || len(preimageData) == 0 {
		// Try historical fetch
		targetBlock := uint64(blockNumber)
		blockHash, err := pd.client.GetBlockHash(&targetBlock)
		if err != nil {
			return nil, fmt.Errorf("get block hash: %w", err)
		}

		preimageData, err = pd.fetchPreimage(hash, length, &blockHash)
		if err != nil {
			return nil, fmt.Errorf("fetch historical preimage: %w", err)
		}
	}

	if len(preimageData) == 0 {
		return nil, fmt.Errorf("preimage not found")
	}

	// Decode the call data and extract addresses
	addresses := make(map[string]bool)
	if err := pd.decodeCallAndExtractAddresses(preimageData, addresses); err != nil {
		return nil, fmt.Errorf("decode call: %w", err)
	}

	// Convert map to slice
	result := make([]string, 0, len(addresses))
	for addr := range addresses {
		result = append(result, addr)
	}

	return result, nil
}

// fetchPreimage retrieves preimage data from storage
func (pd *PreimageDecoder) fetchPreimage(hash string, length uint32, at *string) ([]byte, error) {
	// Try Preimage pallet first
	data, err := pd.fetchFromPreimagePallet(hash, length, at)
	if err == nil && len(data) > 0 {
		return data, nil
	}

	// Fallback to Democracy pallet for older preimages
	return pd.fetchFromDemocracyPallet(hash, at)
}

// fetchFromPreimagePallet fetches from the newer Preimage pallet
func (pd *PreimageDecoder) fetchFromPreimagePallet(hash string, length uint32, at *string) ([]byte, error) {
	// Create storage key for Preimage.PreimageFor
	palletHash := Twox128([]byte("Preimage"))
	storageHash := Twox128([]byte("PreimageFor"))

	// Hash is already the key parameter
	hashBytes, err := DecodeHex(hash)
	if err != nil {
		return nil, err
	}

	// For (hash, length) tuple, encode both
	var keyData []byte
	keyData = append(keyData, hashBytes...)
	lengthBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(lengthBytes, length)
	keyData = append(keyData, lengthBytes...)

	// Use Blake2_128_Concat for the key
	hashedKey := append(Blake2_128(keyData), keyData...)

	key := append(palletHash, storageHash...)
	key = append(key, hashedKey...)

	// Query storage
	data, err := pd.client.GetStorage(codec.HexEncodeToString(key), at)
	if err != nil {
		return nil, err
	}

	if data == "" {
		return nil, nil
	}

	// Decode the Option<Vec<u8>>
	decoded, err := DecodeHex(data)
	if err != nil {
		return nil, err
	}

	if len(decoded) > 0 && decoded[0] == 1 {
		// Has value
		decoder := scale.NewDecoder(bytes.NewReader(decoded[1:]))
		var preimageBytes []byte
		if err := decoder.Decode(&preimageBytes); err != nil {
			return nil, err
		}
		return preimageBytes, nil
	}

	return nil, nil
}

// fetchFromDemocracyPallet fetches from the older Democracy pallet
func (pd *PreimageDecoder) fetchFromDemocracyPallet(hash string, at *string) ([]byte, error) {
	// Create storage key for Democracy.Preimages
	palletHash := Twox128([]byte("Democracy"))
	storageHash := Twox128([]byte("Preimages"))

	hashBytes, err := DecodeHex(hash)
	if err != nil {
		return nil, err
	}

	hashedKey := append(Blake2_128(hashBytes), hashBytes...)

	key := append(palletHash, storageHash...)
	key = append(key, hashedKey...)

	data, err := pd.client.GetStorage(codec.HexEncodeToString(key), at)
	if err != nil {
		return nil, err
	}

	if data == "" {
		return nil, nil
	}

	// Democracy preimages are stored differently
	decoded, err := DecodeHex(data)
	if err != nil {
		return nil, err
	}

	decoder := scale.NewDecoder(bytes.NewReader(decoded))

	// Skip status enum
	_, err = decoder.ReadOneByte()
	if err != nil {
		return nil, err
	}

	// Read the actual preimage data
	var preimageBytes []byte
	if err := decoder.Decode(&preimageBytes); err != nil {
		return nil, err
	}

	return preimageBytes, nil
}

// decodeCallAndExtractAddresses recursively decodes calls and extracts addresses
func (pd *PreimageDecoder) decodeCallAndExtractAddresses(callData []byte, addresses map[string]bool) error {
	if len(callData) < 2 {
		return fmt.Errorf("call data too short")
	}

	decoder := scale.NewDecoder(bytes.NewReader(callData))

	// Read pallet index
	palletIndex, err := decoder.ReadOneByte()
	if err != nil {
		return err
	}

	// Read call index
	callIndex, err := decoder.ReadOneByte()
	if err != nil {
		return err
	}

	// Handle based on pallet
	switch palletIndex {
	case 0x18: // Utility pallet (24)
		return pd.handleUtilityCall(decoder, callIndex, addresses)
	case 0x05: // Balances pallet
		return pd.handleBalancesCall(decoder, callIndex, addresses)
	case 0x06: // Staking pallet
		return pd.handleStakingCall(decoder, callIndex, addresses)
	case 0x13: // Treasury pallet
		return pd.handleTreasuryCall(decoder, callIndex, addresses)
	case 0x20: // Proxy pallet
		return pd.handleProxyCall(decoder, callIndex, addresses)
	default:
		// Try to extract any account IDs from the remaining data
		pd.extractAccountsFromData(decoder, addresses)
	}

	return nil
}

// handleUtilityCall processes utility pallet calls (batch, batch_all, etc)
func (pd *PreimageDecoder) handleUtilityCall(decoder *scale.Decoder, callIndex byte, addresses map[string]bool) error {
	switch callIndex {
	case 0x00, 0x02: // batch, batch_all
		// Read vector of calls
		var length types.UCompact
		if err := decoder.Decode(&length); err != nil {
			return err
		}

		lengthInt := big.NewInt(0).Set(length.Int).Uint64()

		for i := uint64(0); i < lengthInt; i++ {
			// Read call length (compact)
			var callLen types.UCompact
			if err := decoder.Decode(&callLen); err != nil {
				return err
			}

			callLenInt := big.NewInt(0).Set(callLen.Int).Uint64()

			// Read call data
			callData := make([]byte, callLenInt)
			if err := decoder.Read(callData); err != nil {
				return err
			}

			// Recursively decode
			if err := pd.decodeCallAndExtractAddresses(callData, addresses); err != nil {
				log.Printf("Failed to decode nested call %d: %v", i, err)
				continue
			}
		}

	case 0x04: // force_batch
		// Similar to batch but doesn't stop on error
		return pd.handleUtilityCall(decoder, 0x00, addresses)

	default:
		pd.extractAccountsFromData(decoder, addresses)
	}

	return nil
}

// handleBalancesCall extracts addresses from balance calls
func (pd *PreimageDecoder) handleBalancesCall(decoder *scale.Decoder, callIndex byte, addresses map[string]bool) error {
	switch callIndex {
	case 0x00: // transfer
		var dest types.AccountID
		if err := decoder.Decode(&dest); err == nil {
			addresses[accountIDToSS58(dest)] = true
		}

	case 0x07: // transfer_keep_alive
		var dest types.AccountID
		if err := decoder.Decode(&dest); err == nil {
			addresses[accountIDToSS58(dest)] = true
		}

	default:
		pd.extractAccountsFromData(decoder, addresses)
	}

	return nil
}

// handleTreasuryCall extracts addresses from treasury calls
func (pd *PreimageDecoder) handleTreasuryCall(decoder *scale.Decoder, callIndex byte, addresses map[string]bool) error {
	switch callIndex {
	case 0x03: // spend
		// Skip amount
		var amount types.UCompact
		decoder.Decode(&amount)

		// Read beneficiary
		var beneficiary types.AccountID
		if err := decoder.Decode(&beneficiary); err == nil {
			addresses[accountIDToSS58(beneficiary)] = true
		}

	default:
		pd.extractAccountsFromData(decoder, addresses)
	}

	return nil
}

// handleStakingCall extracts addresses from staking calls
func (pd *PreimageDecoder) handleStakingCall(decoder *scale.Decoder, callIndex byte, addresses map[string]bool) error {
	switch callIndex {
	case 0x00: // bond
		// Read controller
		var controller types.AccountID
		if err := decoder.Decode(&controller); err == nil {
			addresses[accountIDToSS58(controller)] = true
		}

	case 0x04: // nominate
		// Read vector of nominees
		var length types.UCompact
		if err := decoder.Decode(&length); err == nil {
			lengthInt := big.NewInt(0).Set(length.Int).Uint64()
			for i := uint64(0); i < lengthInt; i++ {
				var nominee types.AccountID
				if err := decoder.Decode(&nominee); err == nil {
					addresses[accountIDToSS58(nominee)] = true
				}
			}
		}

	default:
		pd.extractAccountsFromData(decoder, addresses)
	}

	return nil
}

// handleProxyCall extracts addresses from proxy calls
func (pd *PreimageDecoder) handleProxyCall(decoder *scale.Decoder, callIndex byte, addresses map[string]bool) error {
	switch callIndex {
	case 0x00: // proxy
		// Read real account
		var real types.AccountID
		if err := decoder.Decode(&real); err == nil {
			addresses[accountIDToSS58(real)] = true
		}

		// Skip force_proxy_type
		decoder.ReadOneByte()

		// Read nested call
		var callLen types.UCompact
		if err := decoder.Decode(&callLen); err == nil {
			callLenInt := big.NewInt(0).Set(callLen.Int).Uint64()
			callData := make([]byte, callLenInt)
			if err := decoder.Read(callData); err == nil {
				pd.decodeCallAndExtractAddresses(callData, addresses)
			}
		}

	default:
		pd.extractAccountsFromData(decoder, addresses)
	}

	return nil
}

// extractAccountsFromData attempts to find account IDs in remaining data
func (pd *PreimageDecoder) extractAccountsFromData(decoder *scale.Decoder, addresses map[string]bool) {
	// Read remaining data
	remaining := make([]byte, 1024)
	n, err := decoder.Read(remaining)
	if err != nil && err != io.EOF {
		return
	}
	remaining = remaining[:n]

	// Look for potential account IDs (32 bytes)
	for i := 0; i <= len(remaining)-32; i++ {
		// Check if this could be an account ID
		potentialAccount := remaining[i : i+32]

		// Simple heuristic: valid accounts usually have some non-zero bytes
		nonZero := 0
		for _, b := range potentialAccount {
			if b != 0 {
				nonZero++
			}
		}

		if nonZero > 10 && nonZero < 30 {
			var accountID types.AccountID
			copy(accountID[:], potentialAccount)
			address := accountIDToSS58(accountID)

			// Additional validation - check if it's a valid SS58
			if len(address) > 40 && len(address) < 50 {
				addresses[address] = true
			}
		}
	}
}
