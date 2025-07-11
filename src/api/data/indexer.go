package data

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/itering/scale.go/types"
	"github.com/itering/scale.go/types/scaleBytes"
	"github.com/itering/substrate-api-rpc/rpc"
	"github.com/itering/substrate-api-rpc/websocket"
	"gorm.io/gorm"

	"github.com/stake-plus/polkadot-gov-comms/src/api/config"
	apitypes "github.com/stake-plus/polkadot-gov-comms/src/api/types"
)

// ────────────────────────────────────────────────────────────────────────────────
// Indexer entry‑point
// ────────────────────────────────────────────────────────────────────────────────

func StartIndexer(ctx context.Context, db *gorm.DB, cfg config.Config) {
	var nets []apitypes.Network
	if err := db.Preload("RPCs", "active = ?", true).Find(&nets).Error; err != nil {
		log.Printf("indexer: failed to load networks: %v", err)
		return
	}
	log.Printf("indexer: found %d networks", len(nets))

	for _, n := range nets {
		if len(n.RPCs) == 0 {
			log.Printf("indexer: no active RPCs for network %s", n.Name)
			continue
		}
		log.Printf("indexer: starting sync for %s using RPC %s", n.Name, n.RPCs[0].URL)
		go syncNetwork(ctx, db, n, n.RPCs[0].URL, time.Duration(cfg.PollInterval)*time.Second)
	}
}

func syncNetwork(ctx context.Context, db *gorm.DB, net apitypes.Network, rpcURL string, every time.Duration) {
	// first run
	if err := importMissing(db, net, rpcURL); err != nil {
		log.Printf("indexer %s: initial sync error: %v", net.Name, err)
	}
	// then poll
	tick := time.NewTicker(every)
	defer tick.Stop()

	for {
		select {
		case <-tick.C:
			if err := importMissing(db, net, rpcURL); err != nil {
				log.Printf("indexer %s: sync error: %v", net.Name, err)
			}
		case <-ctx.Done():
			log.Printf("indexer %s: shutting down", net.Name)
			return
		}
	}
}

// ────────────────────────────────────────────────────────────────────────────────
// One off sync for a single network
// ────────────────────────────────────────────────────────────────────────────────

func importMissing(db *gorm.DB, net apitypes.Network, rpcURL string) error {
	log.Printf("indexer %s: checking for missing proposals", net.Name)

	// fresh connection for THIS pass → avoids "close sent"
	websocket.SetEndpoint(rpcURL)
	pooled, err := websocket.Init()
	if err != nil {
		return fmt.Errorf("ws init: %w", err)
	}
	conn := pooled.Conn
	defer pooled.Close()

	// Get referendum count from OpenGov
	remoteCnt, err := getReferendumCount(conn, net.Name)
	if err != nil {
		return err
	}
	if remoteCnt == 0 {
		log.Printf("indexer %s: no referenda found on chain", net.Name)
		return nil
	}
	remoteMax := remoteCnt - 1

	// highest ref_id we already have
	var maxRef sql.NullInt64
	db.Table("proposals").
		Where("network_id = ?", net.ID).
		Select("MAX(ref_id)").Scan(&maxRef)

	start := uint32(0)
	if maxRef.Valid {
		start = uint32(maxRef.Int64) + 1
	}
	if start > remoteMax {
		log.Printf("indexer %s: up to date (local: %d, remote: %d)", net.Name, start-1, remoteMax)
		return nil
	}

	log.Printf("indexer %s: syncing referenda %d to %d", net.Name, start, remoteMax)
	for i := start; i <= remoteMax; i++ {
		if err := fetchAndStoreProposal(db, conn, net.ID, i); err != nil {
			log.Printf("import %s #%d: %v", net.Name, i, err)
		} else {
			log.Printf("indexer %s: imported referendum #%d", net.Name, i)
		}
	}
	return nil
}

// getReferendumCount gets the referendum count from OpenGov Referenda pallet
func getReferendumCount(conn websocket.WsConn, netName string) (uint32, error) {
	log.Printf("indexer %s: checking Referenda pallet (OpenGov)", netName)

	// Correct storage prefix & item names (case‑sensitive)
	resp, err := rpc.ReadStorage(conn, "Referenda", "ReferendumCount", "")
	if err != nil {
		return 0, fmt.Errorf("failed to get referendum count: %w", err)
	}

	// Convert response to string
	respStr := resp.ToString()
	if respStr == "" || respStr == "0x" {
		return 0, fmt.Errorf("empty result")
	}

	// Try to extract hex data from response
	var hexStr string
	if strings.HasPrefix(respStr, "{") {
		var jsonResp map[string]interface{}
		if err := json.Unmarshal([]byte(respStr), &jsonResp); err == nil {
			if result, ok := jsonResp["result"].(string); ok {
				hexStr = result
			}
		}
	} else {
		hexStr = respStr
	}
	if hexStr == "" || hexStr == "0x" {
		return 0, fmt.Errorf("no data in response")
	}
	hexStr = strings.TrimPrefix(hexStr, "0x")
	if len(hexStr) == 0 {
		return 0, fmt.Errorf("empty hex data")
	}

	// Convert from SCALE encoded u32
	decoder := types.ScaleDecoder{}
	data, _ := hex.DecodeString(hexStr)
	decoder.Init(scaleBytes.ScaleBytes{Data: data}, nil)

	var count uint32
	if v := decoder.ProcessAndUpdateData("U32"); v != nil {
		switch t := v.(type) {
		case uint32:
			count = t
		case int:
			count = uint32(t)
		case float64:
			count = uint32(t)
		default:
			return 0, fmt.Errorf("unexpected type for count: %T", t)
		}
	}

	log.Printf("indexer %s: found %d referenda", netName, count)
	return count, nil
}

// ────────────────────────────────────────────────────────────────────────────────
// Proposal storage helpers
// ────────────────────────────────────────────────────────────────────────────────

func fetchAndStoreProposal(db *gorm.DB, conn websocket.WsConn, netID uint8, idx uint32) error {
	// Use ReadStorage with the index parameter
	resp, err := rpc.ReadStorage(conn, "Referenda", "ReferendumInfoFor", "", strconv.FormatUint(uint64(idx), 10))
	if err != nil {
		return fmt.Errorf("failed to get referendum info: %w", err)
	}
	respStr := resp.ToString()
	if respStr == "" || respStr == "0x" {
		return fmt.Errorf("referendum not found")
	}

	// Extract hex data
	var hexStr string
	if strings.HasPrefix(respStr, "{") {
		var jsonResp map[string]interface{}
		if err := json.Unmarshal([]byte(respStr), &jsonResp); err == nil {
			if result, ok := jsonResp["result"].(string); ok {
				hexStr = result
			}
		}
	} else {
		hexStr = respStr
	}
	if hexStr == "" || hexStr == "0x" {
		return fmt.Errorf("no referendum data")
	}

	// Decode to check if it's a historical reference
	hexStr = strings.TrimPrefix(hexStr, "0x")
	data, _ := hex.DecodeString(hexStr)

	// Check if this is a closed referendum with block reference
	if len(data) > 0 && (data[0] >= 2 && data[0] <= 6) {
		// This looks like a closed referendum (Approved=2, Rejected=3, Cancelled=4, TimedOut=5, Killed=6)
		status := getStatusFromVariant(data[0])

		// Extract block number if present
		if len(data) > 4 {
			blockDecoder := types.ScaleDecoder{}
			blockDecoder.Init(scaleBytes.ScaleBytes{Data: data[1:]}, nil)
			blockNumInterface := blockDecoder.ProcessAndUpdateData("U32")

			var blockNum uint32
			switch v := blockNumInterface.(type) {
			case uint32:
				blockNum = v
			case int:
				blockNum = uint32(v)
			case float64:
				blockNum = uint32(v)
			}

			log.Printf("indexer: referendum %d is %s at block %d", idx, status, blockNum)
			// For historical data, we would need to query at that block
			// For now, just save what we know
			return saveBasicProposal(db, netID, idx, status)
		}

		// If we can't get block number, at least save the status
		return saveBasicProposal(db, netID, idx, status)
	}

	// Parse as ongoing referendum
	return parseReferendaProposal(db, "0x"+hexStr, netID, idx, "Ongoing")
}

func getStatusFromVariant(variant byte) string {
	switch variant {
	case 0:
		return "Ongoing"
	case 2:
		return "Approved"
	case 3:
		return "Rejected"
	case 4:
		return "Cancelled"
	case 5:
		return "TimedOut"
	case 6:
		return "Killed"
	default:
		return "Unknown"
	}
}

func parseReferendaProposal(db *gorm.DB, hexData string, netID uint8, idx uint32, status string) error {
	// For now, just create a basic proposal record
	// In a real implementation, you'd fully decode the SCALE-encoded data
	var prop apitypes.Proposal
	if err := db.FirstOrCreate(&prop, apitypes.Proposal{
		NetworkID: netID,
		RefID:     uint64(idx),
	}).Error; err != nil {
		return err
	}

	// Try to extract submitter if it's an ongoing proposal
	submitter := ""
	if status == "Ongoing" && len(hexData) > 100 {
		// This is a simplified extraction - in production you'd properly decode the struct
		// Look for potential address pattern (32 bytes after some offset)
		data, _ := hex.DecodeString(strings.TrimPrefix(hexData, "0x"))
		if len(data) > 50 {
			// Skip variant byte and some fields, try to find submitter
			for i := 10; i < len(data)-32; i++ {
				// Look for what might be an address
				if data[i] == 0x00 && i+33 < len(data) {
					possibleAddr := data[i+1 : i+33]
					if isValidAddress(possibleAddr) {
						submitter = "0x" + hex.EncodeToString(possibleAddr)
						break
					}
				}
			}
		}
	}

	_ = db.Model(&prop).Updates(map[string]any{
		"status":    status,
		"submitter": submitter,
	}).Error

	if submitter != "" {
		addParticipant(db, prop.ID, submitter)
	}
	return nil
}

func saveBasicProposal(db *gorm.DB, netID uint8, idx uint32, status string) error {
	var prop apitypes.Proposal
	if err := db.FirstOrCreate(&prop, apitypes.Proposal{
		NetworkID: netID,
		RefID:     uint64(idx),
	}).Error; err != nil {
		return err
	}
	_ = db.Model(&prop).Updates(map[string]any{
		"status": status,
	}).Error
	return nil
}

func isValidAddress(data []byte) bool {
	// Basic check - address should be 32 bytes and not all zeros
	if len(data) != 32 {
		return false
	}
	allZero := true
	for _, b := range data {
		if b != 0 {
			allZero = false
			break
		}
	}
	return !allZero
}

func addParticipant(db *gorm.DB, propID uint64, addr string) {
	_ = db.FirstOrCreate(&apitypes.DaoMember{Address: addr}).Error
	_ = db.FirstOrCreate(&apitypes.ProposalParticipant{
		ProposalID: propID,
		Address:    addr,
	}).Error
}
