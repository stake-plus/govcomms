package polkadot

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// GetReferendumInfo fetches and decodes referendum info
func (c *Client) GetReferendumInfo(refID uint32) (*ReferendumInfo, error) {
	key := StorageKeyUint32("Referenda", "ReferendumInfoFor", refID)
	hexData, err := c.GetStorage(key, nil)
	if err != nil {
		return nil, err
	}

	if hexData == "" || hexData == "0x" || hexData == "null" {
		return nil, fmt.Errorf("referendum %d not found", refID)
	}

	data, err := DecodeHex(hexData)
	if err != nil {
		return nil, err
	}

	return DecodeReferendumInfo(data)
}

// DecodeReferendumInfo decodes the raw referendum data
func DecodeReferendumInfo(data []byte) (*ReferendumInfo, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty referendum data")
	}

	info := &ReferendumInfo{}
	offset := 0

	// First byte is the enum variant
	variant := data[offset]
	offset++

	switch variant {
	case 0: // Ongoing
		info.Status = "Ongoing"
		return decodeOngoingReferendum(data[offset:], info)
	case 1: // Approved
		info.Status = "Approved"
		return decodeCompletedReferendum(data[offset:], info, true)
	case 2: // Rejected
		info.Status = "Rejected"
		return decodeCompletedReferendum(data[offset:], info, false)
	case 3: // Cancelled
		info.Status = "Cancelled"
		return decodeCancelledReferendum(data[offset:], info)
	case 4: // TimedOut
		info.Status = "TimedOut"
		return decodeCompletedReferendum(data[offset:], info, false)
	case 5: // Killed
		info.Status = "Killed"
		return decodeKilledReferendum(data[offset:], info)
	default:
		return nil, fmt.Errorf("unknown referendum status variant: %d", variant)
	}
}

func decodeOngoingReferendum(data []byte, info *ReferendumInfo) (*ReferendumInfo, error) {
	offset := 0

	// Track
	if len(data) < offset+2 {
		return nil, fmt.Errorf("insufficient data for track")
	}
	info.Track = binary.LittleEndian.Uint16(data[offset : offset+2])
	offset += 2

	// Origin (skip for now - complex type)
	// For now, skip origin bytes - this varies by runtime
	originLen := 1 // Simplified - actual length depends on origin type
	if len(data) < offset+originLen {
		return nil, fmt.Errorf("insufficient data for origin")
	}
	offset += originLen

	// Proposal (skip for now - would need to decode preimage)
	proposalLen := 32 // Hash
	if len(data) < offset+proposalLen {
		return nil, fmt.Errorf("insufficient data for proposal")
	}
	offset += proposalLen

	// Enactment (skip for now)
	enactmentLen := 5 // 1 byte enum + 4 bytes block number
	if len(data) < offset+enactmentLen {
		return nil, fmt.Errorf("insufficient data for enactment")
	}
	offset += enactmentLen

	// Submitted
	if len(data) < offset+4 {
		return nil, fmt.Errorf("insufficient data for submitted")
	}
	info.Submitted = binary.LittleEndian.Uint32(data[offset : offset+4])
	offset += 4

	// Submission deposit (skip)
	depositLen := 49 // who (32) + amount (16) + 1
	if len(data) < offset+depositLen {
		return nil, fmt.Errorf("insufficient data for deposit")
	}
	offset += depositLen

	// Decision deposit (optional)
	if len(data) > offset && data[offset] == 1 {
		offset++
		offset += depositLen // Skip decision deposit
	} else if len(data) > offset {
		offset++ // Skip None byte
	}

	// Deciding (optional)
	if len(data) > offset && data[offset] == 1 {
		offset++
		info.Decision = &DecisionStatus{}
		if len(data) < offset+4 {
			return nil, fmt.Errorf("insufficient data for decision since")
		}
		info.Decision.Since = binary.LittleEndian.Uint32(data[offset : offset+4])
		offset += 4

		// Confirming (optional)
		if len(data) > offset && data[offset] == 1 {
			offset++
			if len(data) < offset+4 {
				return nil, fmt.Errorf("insufficient data for confirming")
			}
			confirming := binary.LittleEndian.Uint32(data[offset : offset+4])
			info.Decision.Confirming = &confirming
			offset += 4
		} else if len(data) > offset {
			offset++
		}
	} else if len(data) > offset {
		offset++
	}

	// Tally
	if len(data) < offset+48 {
		// Not enough data for full tally, but that's ok
		return info, nil
	}

	// Ayes (u128 = 16 bytes)
	ayesBytes := data[offset : offset+16]
	info.Tally.Ayes = fmt.Sprintf("%x", reverseBytes(ayesBytes))
	offset += 16

	// Nays (u128 = 16 bytes)
	naysBytes := data[offset : offset+16]
	info.Tally.Nays = fmt.Sprintf("%x", reverseBytes(naysBytes))
	offset += 16

	// Support (u128 = 16 bytes)
	supportBytes := data[offset : offset+16]
	info.Tally.Support = fmt.Sprintf("%x", reverseBytes(supportBytes))

	return info, nil
}

func decodeCompletedReferendum(data []byte, info *ReferendumInfo, approved bool) (*ReferendumInfo, error) {
	// Completed referenda have: since, submission deposit, decision deposit (optional)
	offset := 0

	// Since (when it completed)
	if len(data) < offset+4 {
		return info, nil // Some old refs might not have this data
	}
	// Skip since for now
	offset += 4

	// Skip deposits
	// This is simplified - actual structure is more complex

	return info, nil
}

func decodeCancelledReferendum(data []byte, info *ReferendumInfo) (*ReferendumInfo, error) {
	// Cancelled referenda have: since, submission deposit, decision deposit (optional)
	// Similar to completed
	return info, nil
}

func decodeKilledReferendum(data []byte, info *ReferendumInfo) (*ReferendumInfo, error) {
	// Killed referenda have: block number
	return info, nil
}

func reverseBytes(b []byte) []byte {
	result := make([]byte, len(b))
	for i := 0; i < len(b); i++ {
		result[i] = b[len(b)-1-i]
	}
	return result
}

// GetReferendumCount gets the total number of referenda
func (c *Client) GetReferendumCount() (uint32, error) {
	key := StorageKey("Referenda", "ReferendumCount")
	hexData, err := c.GetStorage(key, nil)
	if err != nil {
		return 0, err
	}
	return DecodeU32(hexData)
}

// FindReferendumAtBlock searches for referendum data at different blocks
func (c *Client) FindReferendumAtBlock(refID uint32, startBlock uint64, endBlock uint64) (*ReferendumInfo, uint64, error) {
	// Binary search for the referendum data
	for block := endBlock; block >= startBlock && block > 0; block -= 1000 {
		hash, err := c.GetBlockHash(&block)
		if err != nil {
			continue
		}

		key := StorageKeyUint32("Referenda", "ReferendumInfoFor", refID)
		hexData, err := c.GetStorageAt(key, hash)
		if err != nil {
			continue
		}

		if hexData != "" && hexData != "0x" && hexData != "null" {
			data, err := DecodeHex(hexData)
			if err != nil {
				continue
			}

			info, err := DecodeReferendumInfo(data)
			if err == nil {
				return info, block, nil
			}
		}
	}

	return nil, 0, fmt.Errorf("referendum %d not found in block range", refID)
}

// GetAllReferendumInfo fetches info for all referenda up to current count
func (c *Client) GetAllReferendumInfo() (map[uint32]*ReferendumInfo, error) {
	count, err := c.GetReferendumCount()
	if err != nil {
		return nil, err
	}

	result := make(map[uint32]*ReferendumInfo)

	// Get all keys with prefix
	prefix := strings.TrimSuffix(StorageKey("Referenda", "ReferendumInfoFor"), "0x")
	keys, err := c.GetKeys(prefix, nil)
	if err != nil {
		return nil, err
	}

	// Fetch all values in batch
	for _, key := range keys {
		// Extract referendum ID from key
		// Key format: prefix + encoded_id
		keyBytes, _ := DecodeHex(strings.TrimPrefix(key, "0x"))
		if len(keyBytes) >= len(prefix)/2+4 {
			idBytes := keyBytes[len(prefix)/2:]
			if len(idBytes) >= 4 {
				refID := binary.LittleEndian.Uint32(idBytes[:4])

				hexData, err := c.GetStorage(key, nil)
				if err != nil {
					continue
				}

				if hexData != "" && hexData != "0x" && hexData != "null" {
					data, _ := DecodeHex(hexData)
					if info, err := DecodeReferendumInfo(data); err == nil {
						result[refID] = info
					}
				}
			}
		}
	}

	// Fill in missing ones as cleared
	for i := uint32(0); i < count; i++ {
		if _, exists := result[i]; !exists {
			result[i] = &ReferendumInfo{
				Status: "Cleared",
			}
		}
	}

	return result, nil
}
