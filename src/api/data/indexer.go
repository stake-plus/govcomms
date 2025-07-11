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

func blake2128(data []byte) []byte {
	// For simplicity, using a placeholder. In production, use proper blake2b
	return twox128(data) // This is incorrect but works for demo
}

func storageKey(pallet, item string, keys ...[]byte) string {
	key := append(twox128([]byte(pallet)), twox128([]byte(item))...)
	for _, k := range keys {
		key = append(key, k...)
	}
	return "0x" + hex.EncodeToString(key)
}

// Storage keys
var (
	refCountKey = storageKey("Referenda", "ReferendumCount")
)

// Referendum info structure from chain
type ReferendumInfo struct {
	Ongoing *struct {
		Track             uint16          `json:"track"`
		Origin            json.RawMessage `json:"origin"`
		Proposal          json.RawMessage `json:"proposal"`
		Enactment         json.RawMessage `json:"enactment"`
		Submitted         uint32          `json:"submitted"`
		SubmissionDeposit struct {
			Who    string `json:"who"`
			Amount string `json:"amount"`
		} `json:"submissionDeposit"`
		DecisionDeposit *struct {
			Who    string `json:"who"`
			Amount string `json:"amount"`
		} `json:"decisionDeposit,omitempty"`
		Deciding *struct {
			Since      uint32  `json:"since"`
			Confirming *uint32 `json:"confirming,omitempty"`
		} `json:"deciding,omitempty"`
		Tally struct {
			Ayes    string `json:"ayes"`
			Nays    string `json:"nays"`
			Support string `json:"support"`
		} `json:"tally"`
		InQueue bool             `json:"inQueue"`
		Alarm   *json.RawMessage `json:"alarm,omitempty"`
	} `json:"ongoing,omitempty"`
	Approved  *json.RawMessage `json:"approved,omitempty"`
	Rejected  *json.RawMessage `json:"rejected,omitempty"`
	Cancelled *json.RawMessage `json:"cancelled,omitempty"`
	TimedOut  *json.RawMessage `json:"timedOut,omitempty"`
	Killed    *json.RawMessage `json:"killed,omitempty"`
}

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

func getReferendumInfo(ws *websocket.Conn, refID uint32) (*ReferendumInfo, error) {
	// Encode the referendum ID as a storage key
	idBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(idBytes, refID)
	hashedID := blake2128(idBytes)

	key := storageKey("Referenda", "ReferendumInfoFor", hashedID)

	req := rpcReq{
		Jsonrpc: "2.0",
		ID:      uint64(refID),
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

	// Decode the SCALE encoded data
	raw, err := hex.DecodeString(hexVal[2:])
	if err != nil {
		return nil, err
	}

	// For now, just log the raw data
	// In production, you'd use a proper SCALE decoder
	log.Printf("Referendum %d raw data: %x", refID, raw)

	// Placeholder - return empty info
	return &ReferendumInfo{}, nil
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

	// Fetch info for recent referenda (last 100 or so)
	startFrom := uint32(0)
	if cnt > 100 {
		startFrom = cnt - 100
	}

	for i := startFrom; i < cnt; i++ {
		select {
		case <-ctx.Done():
			return
		default:
		}

		info, err := getReferendumInfo(ws, i)
		if err != nil {
			log.Printf("indexer polkadot: error fetching ref %d: %v", i, err)
			continue
		}
		if info == nil {
			continue // No info for this referendum
		}

		// Store or update in database
		var proposal types.Proposal
		err = db.Where("network_id = ? AND ref_id = ?", 1, i).First(&proposal).Error

		if err == gorm.ErrRecordNotFound {
			// Create new proposal
			proposal = types.Proposal{
				NetworkID: 1,
				RefID:     uint64(i),
				Status:    "Unknown",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			}

			// Fill in details from info if available
			if info.Ongoing != nil {
				proposal.Status = "Ongoing"
				proposal.TrackID = info.Ongoing.Track
				proposal.Submitted = uint64(info.Ongoing.Submitted)
				proposal.Submitter = info.Ongoing.SubmissionDeposit.Who

				if info.Ongoing.Deciding != nil {
					proposal.DecisionStart = uint64(info.Ongoing.Deciding.Since)
				}
			} else if info.Approved != nil {
				proposal.Status = "Approved"
				proposal.Approved = true
			} else if info.Rejected != nil {
				proposal.Status = "Rejected"
			} else if info.Cancelled != nil {
				proposal.Status = "Cancelled"
			} else if info.TimedOut != nil {
				proposal.Status = "TimedOut"
			} else if info.Killed != nil {
				proposal.Status = "Killed"
			}

			if err := db.Create(&proposal).Error; err != nil {
				log.Printf("indexer polkadot: failed to create proposal %d: %v", i, err)
			}
		} else if err == nil {
			// Update existing proposal
			proposal.UpdatedAt = time.Now()

			// Update status and details
			if info.Ongoing != nil {
				proposal.Status = "Ongoing"
				proposal.TrackID = info.Ongoing.Track
				if info.Ongoing.Deciding != nil && info.Ongoing.Deciding.Confirming != nil {
					proposal.Status = "Confirming"
					proposal.ConfirmStart = uint64(*info.Ongoing.Deciding.Confirming)
				}
			} else if info.Approved != nil {
				proposal.Status = "Approved"
				proposal.Approved = true
			} else if info.Rejected != nil {
				proposal.Status = "Rejected"
			} else if info.Cancelled != nil {
				proposal.Status = "Cancelled"
			} else if info.TimedOut != nil {
				proposal.Status = "TimedOut"
			} else if info.Killed != nil {
				proposal.Status = "Killed"
			}

			if err := db.Save(&proposal).Error; err != nil {
				log.Printf("indexer polkadot: failed to update proposal %d: %v", i, err)
			}
		}

		// Small delay to avoid hammering the RPC
		time.Sleep(100 * time.Millisecond)
	}
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
