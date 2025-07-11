package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/itering/substrate-api-rpc/rpc"
	"github.com/itering/substrate-api-rpc/storage"
	"github.com/itering/substrate-api-rpc/websocket"
	"gorm.io/gorm"

	"github.com/stake-plus/polkadot-gov-comms/src/api/config"
	"github.com/stake-plus/polkadot-gov-comms/src/api/types"
)

// ─────────────────────────────────────────────────────────────────────────────
// Indexer entry‑point
// ─────────────────────────────────────────────────────────────────────────────

func StartIndexer(ctx context.Context, db *gorm.DB, cfg config.Config) {
	var nets []types.Network
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

func syncNetwork(ctx context.Context, db *gorm.DB, net types.Network, rpcURL string, every time.Duration) {
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

// ─────────────────────────────────────────────────────────────────────────────
// One off sync for a single network
// ─────────────────────────────────────────────────────────────────────────────

func importMissing(db *gorm.DB, net types.Network, rpcURL string) error {
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
	cntRes, err := rpc.ReadStorage(conn, "Referenda", "ReferendumCount", "")
	if err != nil {
		return 0, fmt.Errorf("failed to read referendum count: %w", err)
	}

	var count uint32
	if err := decodeU32(cntRes, &count); err != nil {
		return 0, fmt.Errorf("failed to decode referendum count: %w", err)
	}

	if count == 0 {
		return 0, fmt.Errorf("no referenda found in OpenGov")
	}

	log.Printf("indexer %s: found %d referenda in OpenGov", netName, count)
	return count, nil
}

// Helper to decode u32 from storage response
func decodeU32(res storage.StateStorage, out *uint32) error {
	// Get the raw value - this depends on the actual structure of StateStorage
	// You may need to adjust based on the actual implementation
	var rawValue string
	if data, err := json.Marshal(res); err == nil {
		var temp map[string]interface{}
		if json.Unmarshal(data, &temp) == nil {
			if val, ok := temp["result"].(string); ok {
				rawValue = val
			}
		}
	}

	if rawValue == "" {
		return fmt.Errorf("no result in response")
	}

	// Decode hex string to u32 (little-endian)
	hexStr := strings.TrimPrefix(rawValue, "0x")
	if len(hexStr) < 8 {
		// Pad with zeros if needed
		hexStr = hexStr + strings.Repeat("0", 8-len(hexStr))
	}

	count := uint32(0)
	for i := 0; i < 4; i++ {
		byteHex := hexStr[i*2 : i*2+2]
		byteVal, _ := strconv.ParseUint(byteHex, 16, 8)
		count |= uint32(byteVal) << (i * 8)
	}

	*out = count
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Proposal storage helpers
// ─────────────────────────────────────────────────────────────────────────────

func fetchAndStoreProposal(db *gorm.DB, conn websocket.WsConn, netID uint8, idx uint32) error {
	info, err := rpc.ReadStorage(conn, "Referenda", "ReferendumInfoFor", "",
		strconv.FormatUint(uint64(idx), 10))

	if err != nil {
		return fmt.Errorf("failed to read referendum info: %w", err)
	}

	return parseReferendaProposal(db, info, netID, idx)
}

func parseReferendaProposal(db *gorm.DB, info storage.StateStorage, netID uint8, idx uint32) error {
	// Convert storage response to JSON for parsing
	data, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("failed to marshal referendum info: %w", err)
	}

	var tmp struct {
		Ongoing struct {
			Track             uint16          `json:"track"`
			Origin            json.RawMessage `json:"origin"`
			Proposal          json.RawMessage `json:"proposal"`
			Enactment         json.RawMessage `json:"enactment"`
			Submitted         uint64          `json:"submitted"`
			SubmissionDeposit struct {
				Who    string `json:"who"`
				Amount string `json:"amount"`
			} `json:"submissionDeposit"`
			DecisionDeposit interface{} `json:"decisionDeposit"`
			Deciding        interface{} `json:"deciding"`
			Tally           struct {
				Ayes    string `json:"ayes"`
				Nays    string `json:"nays"`
				Support string `json:"support"`
			} `json:"tally"`
			InQueue bool        `json:"inQueue"`
			Alarm   interface{} `json:"alarm"`
		} `json:"Ongoing"`
		Approved  interface{} `json:"Approved"`
		Rejected  interface{} `json:"Rejected"`
		Cancelled interface{} `json:"Cancelled"`
		TimedOut  interface{} `json:"TimedOut"`
		Killed    interface{} `json:"Killed"`
	}

	if err := json.Unmarshal(data, &tmp); err != nil {
		return fmt.Errorf("failed to parse referendum info: %w", err)
	}

	status := "Unknown"
	submitter := ""
	endBlock := uint64(0)

	if tmp.Ongoing.SubmissionDeposit.Who != "" {
		status = "Ongoing"
		submitter = tmp.Ongoing.SubmissionDeposit.Who
		// For OpenGov, we don't have a simple end block, it depends on the track
		// For now, just use submitted + some default period
		endBlock = tmp.Ongoing.Submitted + 100800 // ~14 days at 6s blocks
	} else if tmp.Approved != nil {
		status = "Approved"
	} else if tmp.Rejected != nil {
		status = "Rejected"
	} else if tmp.Cancelled != nil {
		status = "Cancelled"
	} else if tmp.TimedOut != nil {
		status = "TimedOut"
	} else if tmp.Killed != nil {
		status = "Killed"
	}

	var prop types.Proposal
	if err := db.FirstOrCreate(&prop, types.Proposal{
		NetworkID: netID,
		RefID:     uint64(idx),
	}).Error; err != nil {
		return err
	}

	_ = db.Model(&prop).Updates(map[string]any{
		"submitter": submitter,
		"status":    status,
		"end_block": endBlock,
	}).Error

	if submitter != "" {
		addParticipant(db, prop.ID, submitter)
	}

	return nil
}

func addParticipant(db *gorm.DB, propID uint64, addr string) {
	_ = db.FirstOrCreate(&types.DaoMember{Address: addr}).Error
	_ = db.FirstOrCreate(&types.ProposalParticipant{
		ProposalID: propID,
		Address:    addr,
	}).Error
}
