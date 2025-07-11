package data

import (
	"context"
	"database/sql"
	"encoding/hex"
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

// ────────────────────────────────────────────────────────────────────────────
// Indexer entry-point
// ────────────────────────────────────────────────────────────────────────────

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
		// Use configured RPC URL if available, otherwise use network's RPC
		rpcURL := cfg.RPCURL
		if rpcURL == "" || !strings.Contains(strings.ToLower(rpcURL), strings.ToLower(n.Name)) {
			rpcURL = n.RPCs[0].URL
		}
		log.Printf("indexer: starting sync for %s using RPC %s", n.Name, rpcURL)
		go syncNetwork(ctx, db, n, rpcURL, time.Duration(cfg.PollInterval)*time.Second)
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

// ────────────────────────────────────────────────────────────────────────────
// One-off sync for a single network
// ────────────────────────────────────────────────────────────────────────────

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

	// The correct storage key for Polkadot OpenGov is Referenda.ReferendumCount
	resp, err := rpc.ReadStorage(conn, "Referenda", "ReferendumCount", "")
	if err != nil {
		return 0, fmt.Errorf("failed to get referendum count: %w", err)
	}

	// Log the raw response
	respStr := resp.ToString()
	log.Printf("indexer %s: ReferendumCount raw response: %s", netName, respStr)

	// Decode manually since ToU32FromCodec seems to be failing
	hexStr := strings.TrimPrefix(respStr, "0x")
	if hexStr == "" {
		return 0, fmt.Errorf("empty response")
	}

	data, err := hex.DecodeString(hexStr)
	if err != nil {
		return 0, fmt.Errorf("failed to decode hex: %w", err)
	}

	if len(data) < 4 {
		return 0, fmt.Errorf("invalid data length for u32: %d", len(data))
	}

	// U32 is 4 bytes little-endian
	count := uint32(data[0]) | uint32(data[1])<<8 | uint32(data[2])<<16 | uint32(data[3])<<24
	log.Printf("indexer %s: found %d referenda", netName, count)
	return count, nil
}

// ────────────────────────────────────────────────────────────────────────────
// Proposal storage helpers
// ────────────────────────────────────────────────────────────────────────────

func fetchAndStoreProposal(db *gorm.DB, conn websocket.WsConn, netID uint8, idx uint32) error {
	// The correct storage key is Referenda.ReferendumInfoFor with the index as parameter
	resp, err := rpc.ReadStorage(conn, "Referenda", "ReferendumInfoFor", "", strconv.FormatUint(uint64(idx), 10))
	if err != nil {
		return fmt.Errorf("failed to get referendum info: %w", err)
	}

	// Get the raw hex string for processing
	respStr := resp.ToString()
	if respStr == "" || respStr == "0x" || respStr == "null" {
		return fmt.Errorf("referendum not found")
	}

	// Decode to check if closed variant
	hexStr := strings.TrimPrefix(respStr, "0x")
	data, err := hex.DecodeString(hexStr)
	if err != nil {
		return fmt.Errorf("failed to decode hex: %w", err)
	}

	if len(data) > 0 && (data[0] >= 2 && data[0] <= 6) {
		status := getStatusFromVariant(data[0])
		if len(data) > 4 {
			var dec types.ScaleDecoder
			dec.Init(scaleBytes.ScaleBytes{Data: data[1:]}, nil)
			bi := dec.ProcessAndUpdateData("U32")
			if n, ok := bi.(uint32); ok {
				log.Printf("indexer: referendum %d is %s at block %d", idx, status, n)
			}
		}
		return saveBasicProposal(db, netID, idx, status)
	}
	// Ongoing
	return parseReferendaProposal(db, "0x"+hexStr, netID, idx, "Ongoing")
}

func getStatusFromVariant(v byte) string {
	switch v {
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
	var prop apitypes.Proposal
	if err := db.FirstOrCreate(&prop, apitypes.Proposal{
		NetworkID: netID,
		RefID:     uint64(idx),
	}).Error; err != nil {
		return err
	}
	// rudimentary submitter extraction
	submitter := ""
	if status == "Ongoing" && len(hexData) > 100 {
		data, _ := hex.DecodeString(strings.TrimPrefix(hexData, "0x"))
		for i := 8; i+32 < len(data); i++ {
			if data[i] == 0x00 {
				cand := data[i+1 : i+33]
				if isValidAddress(cand) {
					submitter = "0x" + hex.EncodeToString(cand)
					break
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
	var p apitypes.Proposal
	if err := db.FirstOrCreate(&p, apitypes.Proposal{
		NetworkID: netID,
		RefID:     uint64(idx),
	}).Error; err != nil {
		return err
	}
	return db.Model(&p).Update("status", status).Error
}

func isValidAddress(b []byte) bool {
	if len(b) != 32 {
		return false
	}
	for _, x := range b {
		if x != 0 {
			return true
		}
	}
	return false
}

func addParticipant(db *gorm.DB, propID uint64, addr string) {
	_ = db.FirstOrCreate(&apitypes.DaoMember{Address: addr}).Error
	_ = db.FirstOrCreate(&apitypes.ProposalParticipant{
		ProposalID: propID,
		Address:    addr,
	}).Error
}
