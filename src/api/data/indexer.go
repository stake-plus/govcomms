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

	if hexVal == "" || hexVal == "0x" || hexVal == "null" {
		return nil, nil // No referendum info
	}

	// Decode the SCALE encoded data
	raw, err := hex.DecodeString(hexVal[2:])
	if err != nil {
		return nil, err
	}

	// Log for debugging active refs
	if len(raw) > 0 {
		log.Printf("indexer polkadot: ref %d has data, first byte (variant): %d", refID, raw[0])
	}

	// Parse the referendum info
	return decodeReferendumInfo(raw, refID)
}

func decodeReferendumInfo(data []byte, refID uint32) (map[string]interface{}, error) {
	if len(data) == 0 {
		return nil, nil
	}

	result := make(map[string]interface{})
	offset := 0

	// First byte is the enum variant
	if offset >= len(data) {
		return nil, fmt.Errorf("data too short")
	}
	variant := data[offset]
	offset++

	switch variant {
	case 0: // Ongoing
		result["status"] = "Ongoing"

		// Decode track (u16)
		if offset+2 > len(data) {
			return result, nil
		}
		track := binary.LittleEndian.Uint16(data[offset : offset+2])
		result["track"] = track
		offset += 2

		// The structure for Ongoing is complex:
		// - track: u16
		// - origin: RuntimeOrigin (enum, variable size)
		// - proposal: Bounded<CallOf<T, I>, T::Preimages> (variable size)
		// - enactment: DispatchTime<BlockNumberFor<T>> (enum)
		// - submitted: BlockNumberFor<T> (u32)
		// - submission_deposit: Deposit<T::AccountId, BalanceOf<T, I>>
		// - decision_deposit: Option<Deposit<T::AccountId, BalanceOf<T, I>>>
		// - deciding: Option<DecidingStatus<BlockNumberFor<T>>>
		// - tally: Tally<T::Votes, T::Currency>
		// - in_queue: bool
		// - alarm: Option<AlarmData>

		// Try to find the submitter by looking for account ID patterns
		// Account IDs are 32 bytes and typically appear after some known patterns
		for i := offset; i <= len(data)-32; i++ {
			// Look for potential account ID
			candidate := data[i : i+32]

			// Check if this could be an account ID
			nonZero := false
			allFF := true
			zeroCount := 0

			for _, b := range candidate {
				if b != 0 {
					nonZero = true
				}
				if b != 0xFF {
					allFF = false
				}
				if b == 0 {
					zeroCount++
				}
			}

			// Heuristics for valid account ID:
			// - Has some non-zero bytes
			// - Not all 0xFF
			// - Not mostly zeros (less than 28 zeros out of 32)
			if nonZero && !allFF && zeroCount < 28 {
				result["submitter"] = "0x" + hex.EncodeToString(candidate)
				break
			}
		}

	case 1: // Approved
		result["status"] = "Approved"
		// Structure: since: BlockNumberFor<T>, submission_deposit: Option<Deposit>, decision_deposit: Option<Deposit>
		// Read since block (u32)
		if offset+4 <= len(data) {
			since := binary.LittleEndian.Uint32(data[offset : offset+4])
			result["endBlock"] = since
		}

	case 2: // Rejected
		result["status"] = "Rejected"
		// Structure: since: BlockNumberFor<T>, submission_deposit: Option<Deposit>, decision_deposit: Option<Deposit>
		// Read since block (u32)
		if offset+4 <= len(data) {
			since := binary.LittleEndian.Uint32(data[offset : offset+4])
			result["endBlock"] = since
		}

	case 3: // Cancelled
		result["status"] = "Cancelled"
		// Structure: since: BlockNumberFor<T>, submission_deposit: Option<Deposit>, decision_deposit: Option<Deposit>
		// Read since block (u32)
		if offset+4 <= len(data) {
			since := binary.LittleEndian.Uint32(data[offset : offset+4])
			result["endBlock"] = since
		}

	case 4: // TimedOut
		result["status"] = "TimedOut"
		// Structure: since: BlockNumberFor<T>, submission_deposit: Option<Deposit>, decision_deposit: Option<Deposit>
		// Read since block (u32)
		if offset+4 <= len(data) {
			since := binary.LittleEndian.Uint32(data[offset : offset+4])
			result["endBlock"] = since
		}

	case 5: // Killed
		result["status"] = "Killed"
		// Structure: since: BlockNumberFor<T>
		// Read since block (u32)
		if offset+4 <= len(data) {
			since := binary.LittleEndian.Uint32(data[offset : offset+4])
			result["endBlock"] = since
		}

	default:
		result["status"] = "Unknown"
		log.Printf("Unknown referendum variant: %d", variant)
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
	log.Printf("indexer polkadot: chain reports %d referenda total", cnt)

	// Check what we have in database
	var dbCount int64
	db.Model(&types.Proposal{}).Where("network_id = ?", 1).Count(&dbCount)
	log.Printf("indexer polkadot: database has %d proposals", dbCount)

	// For now, let's try to get the most recent referenda that might still be active
	// We'll scan backwards from the latest referendum
	updated := 0
	created := 0
	skipped := 0
	errors := 0
	found := 0

	startFrom := cnt - 1
	endAt := uint32(0)

	log.Printf("indexer polkadot: scanning referenda from %d down to %d", startFrom, endAt)

	for i := startFrom; i >= endAt && found < 20; i-- {
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
			if i%10 == 0 {
				log.Printf("indexer polkadot: ref %d has no data (not active)", i)
			}
			continue
		}

		found++
		log.Printf("indexer polkadot: ref %d info: %+v", i, info)

		// Extract data from info map
		status, _ := info["status"].(string)
		track, _ := info["track"].(uint16)
		submitter, _ := info["submitter"].(string)
		origin, _ := info["origin"].(string)

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
				Origin:    origin,
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
				log.Printf("indexer polkadot: created proposal %d with status %s, track %d", i, status, track)
			}

			// Also create the proposal participant entry for the submitter
			if proposal.Submitter != "Unknown" && proposal.Submitter != "" {
				participant := types.ProposalParticipant{
					ProposalID: proposal.ID,
					Address:    proposal.Submitter,
				}
				db.Create(&participant)
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
			if origin != "" && proposal.Origin != origin {
				proposal.Origin = origin
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
		if (startFrom-i) > 0 && (startFrom-i)%20 == 0 {
			log.Printf("indexer polkadot: scanned %d referenda, found %d active (created: %d, updated: %d, skipped: %d, errors: %d)",
				startFrom-i, found, created, updated, skipped, errors)
		}

		// Small delay
		time.Sleep(50 * time.Millisecond)
	}

	log.Printf("indexer polkadot: sync complete - found %d active, created %d, updated %d, skipped %d, errors %d proposals",
		found, created, updated, skipped, errors)
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
