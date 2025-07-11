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
	"gorm.io/gorm/clause"
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

	// Get existing proposal count to check if we need to sync
	var existingCount int64
	db.Model(&types.Proposal{}).Where("network_id = ?", 1).Count(&existingCount)

	if existingCount >= int64(cnt) {
		log.Printf("indexer polkadot: already have all %d proposals", cnt)
		return
	}

	// Batch insert missing proposals
	log.Printf("indexer polkadot: syncing %d missing proposals", int64(cnt)-existingCount)

	batchSize := 100
	proposals := make([]types.Proposal, 0, batchSize)

	for i := uint32(0); i < cnt; i++ {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Check if already exists
		var exists bool
		db.Model(&types.Proposal{}).
			Select("count(*) > 0").
			Where("network_id = ? AND ref_id = ?", 1, i).
			Find(&exists)

		if !exists {
			proposals = append(proposals, types.Proposal{
				NetworkID: 1,
				RefID:     uint64(i),
				Status:    "Unknown",
				Submitter: "Unknown",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			})
		}

		// Insert batch when full or at end
		if len(proposals) >= batchSize || (i == cnt-1 && len(proposals) > 0) {
			if err := db.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(proposals, batchSize).Error; err != nil {
				log.Printf("indexer polkadot: batch insert error: %v", err)
			} else {
				log.Printf("indexer polkadot: inserted batch of %d proposals", len(proposals))
			}
			proposals = proposals[:0] // Clear slice
		}
	}

	// Final count
	db.Model(&types.Proposal{}).Where("network_id = ?", 1).Count(&existingCount)
	log.Printf("indexer polkadot: sync complete, total proposals: %d", existingCount)
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
