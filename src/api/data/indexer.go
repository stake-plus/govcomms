package data

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"time"

	"github.com/OneOfOne/xxhash"
	"github.com/gorilla/websocket"
	"github.com/stake-plus/polkadot-gov-comms/src/api/types"
	"gorm.io/gorm"
)

// ---------- tiny JSON-RPC helpers ----------

type rpcReq struct {
	Jsonrpc string        `json:"jsonrpc"`
	ID      uint64        `json:"id"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
}

type rpcResp struct {
	Jsonrpc string          `json:"jsonrpc"`
	ID      uint64          `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// ---------- TwoX-128 (Substrate) ----------

func twox128(data []byte) []byte {
	hash1 := xxhash.NewS64(0)
	hash1.Write(data)
	hash2 := xxhash.NewS64(1)
	hash2.Write(data)
	out := make([]byte, 16)
	binary.LittleEndian.PutUint64(out[0:], hash1.Sum64())
	binary.LittleEndian.PutUint64(out[8:], hash2.Sum64())
	return out
}

func twox64(data []byte) []byte {
	hash := xxhash.NewS64(0)
	hash.Write(data)
	out := make([]byte, 8)
	binary.LittleEndian.PutUint64(out, hash.Sum64())
	return out
}

func storageKey(pallet, item string, keys ...[]byte) string {
	key := append(twox128([]byte(pallet)), twox128([]byte(item))...)
	for _, k := range keys {
		key = append(key, k...)
	}
	return "0x" + hex.EncodeToString(key)
}

func storageKeyWithHash(pallet, item string, refID uint32) string {
	key := append(twox128([]byte(pallet)), twox128([]byte(item))...)

	// Encode referendum ID and hash it
	idBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(idBytes, refID)
	hashedID := append(twox64(idBytes), idBytes...)

	key = append(key, hashedID...)
	return "0x" + hex.EncodeToString(key)
}

// Storage keys
var (
	refCountKey = storageKey("Referenda", "ReferendumCount")
)

// ---------- core fetcher ----------

func getReferendumCount(ws *websocket.Conn) (uint32, error) {
	req := rpcReq{
		Jsonrpc: "2.0",
		ID:      1,
		Method:  "state_getStorage",
		Params:  []interface{}{refCountKey, nil},
	}
	if err := ws.WriteJSON(req); err != nil {
		return 0, err
	}

	var rsp rpcResp
	if err := ws.ReadJSON(&rsp); err != nil {
		return 0, err
	}
	if rsp.Error != nil {
		return 0, fmt.Errorf("RPC %d: %s", rsp.Error.Code, rsp.Error.Message)
	}

	var hexVal string
	if err := json.Unmarshal(rsp.Result, &hexVal); err != nil {
		return 0, err
	}
	if len(hexVal) < 3 {
		return 0, nil
	}

	raw, err := hex.DecodeString(hexVal[2:])
	if err != nil {
		return 0, err
	}
	if len(raw) < 4 {
		return 0, fmt.Errorf("unexpected storage length: %d", len(raw))
	}

	return binary.LittleEndian.Uint32(raw[:4]), nil
}

func getReferendumInfo(ws *websocket.Conn, refID uint32) (map[string]interface{}, error) {
	key := storageKeyWithHash("Referenda", "ReferendumInfoFor", refID)

	req := rpcReq{
		Jsonrpc: "2.0",
		ID:      uint64(refID + 1000),
		Method:  "state_getStorage",
		Params:  []interface{}{key, nil},
	}

	if err := ws.WriteJSON(req); err != nil {
		return nil, err
	}

	var rsp rpcResp
	if err := ws.ReadJSON(&rsp); err != nil {
		return nil, err
	}
	if rsp.Error != nil {
		return nil, fmt.Errorf("RPC error: %s", rsp.Error.Message)
	}

	var hexVal string
	if err := json.Unmarshal(rsp.Result, &hexVal); err != nil {
		return nil, err
	}
	if hexVal == "" || hexVal == "0x" {
		return nil, nil // No referendum info
	}

	// Log raw data for debugging
	if refID%50 == 0 {
		log.Printf("indexer polkadot: ref %d raw data: %s", refID, hexVal)
	}

	// Decode the SCALE encoded data
	raw, err := hex.DecodeString(hexVal[2:])
	if err != nil {
		return nil, err
	}

	// Parse the referendum info
	return decodeReferendumInfo(raw)
}

func decodeReferendumInfo(data []byte) (map[string]interface{}, error) {
	if len(data) == 0 {
		return nil, nil
	}

	result := make(map[string]interface{})

	// First byte is the enum variant
	variant := data[0]

	switch variant {
	case 0: // Ongoing
		result["status"] = "Ongoing"

		// Track is at offset 1, u16 little-endian
		if len(data) >= 3 {
			track := binary.LittleEndian.Uint16(data[1:3])
			result["track"] = uint16(track)
		}

		// For now, let's extract what we can from the raw data
		// Look for potential account IDs (32 bytes) in the data
		for i := 1; i < len(data)-32; i++ {
			// Simple heuristic: look for non-zero 32-byte sequences
			if data[i] != 0 && i+32 < len(data) {
				possibleAccount := data[i : i+32]
				// Check if it looks like an account (not all zeros)
				nonZero := false
				for _, b := range possibleAccount {
					if b != 0 {
						nonZero = true
						break
					}
				}
				if nonZero {
					result["submitter"] = "0x" + hex.EncodeToString(possibleAccount)
					break
				}
			}
		}

	case 1: // Approved
		result["status"] = "Approved"
		if len(data) >= 5 {
			endBlockVal := binary.LittleEndian.Uint32(data[1:5])
			result["endBlock"] = endBlockVal
		}

	case 2: // Rejected
		result["status"] = "Rejected"
		if len(data) >= 5 {
			endBlockVal := binary.LittleEndian.Uint32(data[1:5])
			result["endBlock"] = endBlockVal
		}

	case 3: // Cancelled
		result["status"] = "Cancelled"
		if len(data) >= 5 {
			endBlockVal := binary.LittleEndian.Uint32(data[1:5])
			result["endBlock"] = endBlockVal
		}

	case 4: // TimedOut
		result["status"] = "TimedOut"
		if len(data) >= 5 {
			endBlockVal := binary.LittleEndian.Uint32(data[1:5])
			result["endBlock"] = endBlockVal
		}

	case 5: // Killed
		result["status"] = "Killed"
		if len(data) >= 5 {
			endBlockVal := binary.LittleEndian.Uint32(data[1:5])
			result["endBlock"] = endBlockVal
		}

	default:
		result["status"] = "Unknown"
	}

	return result, nil
}

func getCurrentBlock(ws *websocket.Conn) (uint64, error) {
	req := rpcReq{
		Jsonrpc: "2.0",
		ID:      999,
		Method:  "chain_getHeader",
		Params:  []interface{}{},
	}

	if err := ws.WriteJSON(req); err != nil {
		return 0, err
	}

	var rsp rpcResp
	if err := ws.ReadJSON(&rsp); err != nil {
		return 0, err
	}
	if rsp.Error != nil {
		return 0, fmt.Errorf("RPC error: %s", rsp.Error.Message)
	}

	var header struct {
		Number string `json:"number"`
	}
	if err := json.Unmarshal(rsp.Result, &header); err != nil {
		return 0, err
	}

	// Parse hex number
	blockNum := new(big.Int)
	blockNum.SetString(header.Number[2:], 16)

	return blockNum.Uint64(), nil
}

// ---------- public entry-point ----------

func RunPolkadotIndexer(ctx context.Context, db *gorm.DB, rpcURL string) {
	ws, _, err := websocket.DefaultDialer.DialContext(ctx, rpcURL, nil)
	if err != nil {
		log.Printf("indexer polkadot: dial error: %v", err)
		return
	}
	defer ws.Close()

	// Get current block
	currentBlock, err := getCurrentBlock(ws)
	if err != nil {
		log.Printf("indexer polkadot: failed to get current block: %v", err)
		return
	}
	log.Printf("indexer polkadot: current block %d", currentBlock)

	// Get referendum count
	cnt, err := getReferendumCount(ws)
	if err != nil {
		log.Printf("indexer polkadot: failed to fetch count: %v", err)
		return
	}
	log.Printf("indexer polkadot: chain reports %d referenda", cnt)

	// Check what we have in database
	var dbCount int64
	db.Model(&types.Proposal{}).Where("network_id = ?", 1).Count(&dbCount)
	log.Printf("indexer polkadot: database has %d proposals", dbCount)

	// Process recent referenda (last 100)
	startFrom := uint32(0)
	if cnt > 100 {
		startFrom = cnt - 100
	}

	updated := 0
	created := 0
	skipped := 0
	errors := 0

	for i := startFrom; i < cnt; i++ {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Get referendum info
		info, err := getReferendumInfo(ws, i)
		if err != nil {
			log.Printf("indexer polkadot: error fetching ref %d: %v", i, err)
			errors++
			continue
		}
		if info == nil {
			skipped++
			continue
		}

		// Log first few referendum info
		if i < startFrom+5 {
			log.Printf("indexer polkadot: ref %d info: %+v", i, info)
		}

		// Extract data from info map
		status, _ := info["status"].(string)
		track, _ := info["track"].(uint16)
		submitter, _ := info["submitter"].(string)

		if status == "" {
			status = "Unknown"
		}

		// Update database
		var proposal types.Proposal
		err = db.Where("network_id = ? AND ref_id = ?", 1, i).First(&proposal).Error

		if err == gorm.ErrRecordNotFound {
			// Create new
			proposal = types.Proposal{
				NetworkID: 1,
				RefID:     uint64(i),
				Status:    status,
				TrackID:   track,
				Submitter: submitter,
				Approved:  status == "Approved",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			}

			// Set default submitter if empty
			if proposal.Submitter == "" {
				proposal.Submitter = "Unknown"
			}

			if err := db.Create(&proposal).Error; err != nil {
				log.Printf("indexer polkadot: failed to create proposal %d: %v", i, err)
			} else {
				created++
				log.Printf("indexer polkadot: created proposal %d with status %s", i, status)
			}
		} else if err == nil {
			// Update existing
			changed := false

			if proposal.Status != status && status != "" {
				log.Printf("indexer polkadot: updating ref %d status from %s to %s", i, proposal.Status, status)
				proposal.Status = status
				changed = true
			}

			if track > 0 && proposal.TrackID != track {
				proposal.TrackID = track
				changed = true
			}

			if submitter != "" && submitter != proposal.Submitter && proposal.Submitter == "Unknown" {
				proposal.Submitter = submitter
				changed = true
			}

			if status == "Approved" && !proposal.Approved {
				proposal.Approved = true
				changed = true
			}

			if changed {
				proposal.UpdatedAt = time.Now()
				if err := db.Save(&proposal).Error; err != nil {
					log.Printf("indexer polkadot: failed to update proposal %d: %v", i, err)
				} else {
					updated++
				}
			}
		} else {
			log.Printf("indexer polkadot: database error for ref %d: %v", i, err)
		}

		// Progress logging
		if i%20 == 0 && i > startFrom {
			log.Printf("indexer polkadot: processed %d/%d referenda (created: %d, updated: %d, skipped: %d, errors: %d)",
				i-startFrom, cnt-startFrom, created, updated, skipped, errors)
		}

		// Small delay
		time.Sleep(50 * time.Millisecond)
	}

	log.Printf("indexer polkadot: sync complete - created %d, updated %d, skipped %d, errors %d proposals",
		created, updated, skipped, errors)
}

// IndexerService runs the indexer periodically
func IndexerService(ctx context.Context, db *gorm.DB, rpcURL string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Run once immediately
	RunPolkadotIndexer(ctx, db, rpcURL)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			RunPolkadotIndexer(ctx, db, rpcURL)
		}
	}
}
