package polkadot

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log"

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
		// Has value, decode the Vec<u8>
		// Read compact length
		compactLen, compactBytes, err := DecodeCompact(decoded[1:])
		if err != nil {
			return nil, err
		}

		// The preimage data follows the compact length
		start := 1 + compactBytes
		if uint64(len(decoded)) < uint64(start)+compactLen {
			return nil, fmt.Errorf("insufficient data for preimage")
		}

		return decoded[start : start+int(compactLen)], nil
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
		return pd.handleUtilityCall(callIndex, addresses, callData[2:])
	case 0x05: // Balances pallet
		return pd.handleBalancesCall(decoder, callIndex, addresses)
	case 0x06: // Staking pallet
		return pd.handleStakingCall(decoder, callIndex, addresses, callData[2:])
	case 0x13: // Treasury pallet
		return pd.handleTreasuryCall(decoder, callIndex, addresses, callData[2:])
	case 0x20: // Proxy pallet
		return pd.handleProxyCall(decoder, callIndex, addresses, callData[2:])
	default:
		// Try to extract any account IDs from the remaining data
		pd.extractAccountsFromRawData(callData[2:], addresses)
	}

	return nil
}

// handleUtilityCall processes utility pallet calls (batch, batch_all, etc)
func (pd *PreimageDecoder) handleUtilityCall(callIndex byte, addresses map[string]bool, remainingData []byte) error {
	switch callIndex {
	case 0x00, 0x02: // batch, batch_all
		// Read vector length as compact
		compactLen, bytesRead, err := DecodeCompact(remainingData)
		if err != nil {
			return err
		}

		// Skip past the compact length
		offset := bytesRead

		// For utility.batch, each call is encoded as a separate call
		// We need to parse them sequentially
		for i := uint64(0); i < compactLen && offset < len(remainingData); i++ {
			// Each call in a batch is a complete encoded call
			// Try to decode from current offset
			if err := pd.decodeCallAndExtractAddresses(remainingData[offset:], addresses); err != nil {
				log.Printf("Failed to decode nested call %d: %v", i, err)
				// Try to skip this call and continue
				// This is a heuristic - we skip a reasonable amount
				offset += 100
				if offset >= len(remainingData) {
					break
				}
			} else {
				// Successfully decoded, but we don't know how many bytes were consumed
				// This is a limitation - we'd need to track bytes consumed in decode
				// For now, try to find the next call by looking for valid pallet indices
				foundNext := false
				for j := offset + 10; j < len(remainingData)-1; j++ {
					if remainingData[j] < 50 { // Reasonable pallet index
						offset = j
						foundNext = true
						break
					}
				}
				if !foundNext {
					break
				}
			}
		}

	case 0x04: // force_batch
		// Similar to batch but doesn't stop on error
		return pd.handleUtilityCall(0x00, addresses, remainingData)

	default:
		// Extract from remaining data
		pd.extractAccountsFromRawData(remainingData, addresses)
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
		// For other calls, we can't easily extract addresses without knowing the structure
		// So we'll skip
	}

	return nil
}

// handleTreasuryCall extracts addresses from treasury calls
func (pd *PreimageDecoder) handleTreasuryCall(decoder *scale.Decoder, callIndex byte, addresses map[string]bool, remainingData []byte) error {
	switch callIndex {
	case 0x03: // spend
		// For treasury.spend, we need to skip the amount and get the beneficiary
		// Amount is a compact-encoded u128
		_, bytesRead, err := DecodeCompact(remainingData)
		if err != nil {
			return err
		}

		// Skip the amount bytes and decode beneficiary
		if bytesRead+32 <= len(remainingData) {
			beneficiaryBytes := remainingData[bytesRead : bytesRead+32]
			var beneficiary types.AccountID
			copy(beneficiary[:], beneficiaryBytes)
			addresses[accountIDToSS58(beneficiary)] = true
		}

	default:
		// Extract from remaining data
		pd.extractAccountsFromRawData(remainingData, addresses)
	}

	return nil
}

// handleStakingCall extracts addresses from staking calls
func (pd *PreimageDecoder) handleStakingCall(decoder *scale.Decoder, callIndex byte, addresses map[string]bool, remainingData []byte) error {
	switch callIndex {
	case 0x00: // bond
		// First param is controller AccountID
		if len(remainingData) >= 32 {
			var controller types.AccountID
			copy(controller[:], remainingData[:32])
			addresses[accountIDToSS58(controller)] = true
		}

	case 0x04: // nominate
		// Read vector of nominees
		compactLen, bytesRead, err := DecodeCompact(remainingData)
		if err == nil {
			offset := bytesRead
			// Each nominee is 32 bytes
			for i := uint64(0); i < compactLen && offset+32 <= len(remainingData); i++ {
				var nominee types.AccountID
				copy(nominee[:], remainingData[offset:offset+32])
				addresses[accountIDToSS58(nominee)] = true
				offset += 32
			}
		}

	default:
		pd.extractAccountsFromRawData(remainingData, addresses)
	}

	return nil
}

// handleProxyCall extracts addresses from proxy calls
func (pd *PreimageDecoder) handleProxyCall(decoder *scale.Decoder, callIndex byte, addresses map[string]bool, remainingData []byte) error {
	switch callIndex {
	case 0x00: // proxy
		// First param is real AccountID
		if len(remainingData) >= 32 {
			var real types.AccountID
			copy(real[:], remainingData[:32])
			addresses[accountIDToSS58(real)] = true

			// Skip account (32 bytes) and force_proxy_type (1 byte)
			if len(remainingData) > 33 {
				// The rest is the nested call
				pd.decodeCallAndExtractAddresses(remainingData[33:], addresses)
			}
		}

	default:
		pd.extractAccountsFromRawData(remainingData, addresses)
	}

	return nil
}

// extractAccountsFromData attempts to find account IDs in decoder
func (pd *PreimageDecoder) extractAccountsFromData(decoder *scale.Decoder, addresses map[string]bool) {
	// Since we can't easily read remaining bytes from decoder,
	// we'll try to decode up to 10 potential AccountIDs
	for i := 0; i < 10; i++ {
		var accountID types.AccountID
		if err := decoder.Decode(&accountID); err != nil {
			break
		}

		// Check if it looks like a valid account
		nonZero := 0
		for _, b := range accountID {
			if b != 0 {
				nonZero++
			}
		}

		if nonZero > 10 && nonZero < 30 {
			address := accountIDToSS58(accountID)
			if len(address) > 40 && len(address) < 50 {
				addresses[address] = true
			}
		}
	}
}

// extractAccountsFromRawData looks for potential AccountIDs in raw bytes
func (pd *PreimageDecoder) extractAccountsFromRawData(data []byte, addresses map[string]bool) {
	// Look for potential account IDs (32 bytes)
	for i := 0; i <= len(data)-32; i++ {
		// Check if this could be an account ID
		potentialAccount := data[i : i+32]

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
