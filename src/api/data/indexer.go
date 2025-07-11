package data

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/itering/substrate-api-rpc/rpc"
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

	// on‑chain referendum counter
	cntRes, err := rpc.ReadStorage(conn, "Referenda", "ReferendumCount", "")
	if err != nil {
		return fmt.Errorf("read ReferendumCount: %w", err)
	}
	remoteCnt := cntRes.ToU32FromCodec()
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

// ─────────────────────────────────────────────────────────────────────────────
// Proposal storage helpers
// ─────────────────────────────────────────────────────────────────────────────

func fetchAndStoreProposal(db *gorm.DB, conn websocket.WsConn, netID uint8, idx uint32) error {
	info, err := rpc.ReadStorage(conn, "Referenda", "ReferendumInfoFor", "",
		strconv.FormatUint(uint64(idx), 10))
	if err != nil || info.ToString() == "" {
		return fmt.Errorf("failed to read referendum info: %w", err)
	}

	var tmp struct {
		Info struct {
			Status     string               `json:"status"`
			Submitting struct{ Who string } `json:"Submitting"`
			Ongoing    struct {
				End        uint64               `json:"end"`
				Submitting struct{ Who string } `json:"submitting"`
			} `json:"Ongoing"`
			Finished struct {
				EndBlock uint64 `json:"end"`
			} `json:"Finished"`
		} `json:"info"`
	}
	info.ToAny(&tmp)

	submitter := tmp.Info.Submitting.Who
	if submitter == "" {
		submitter = tmp.Info.Ongoing.Submitting.Who
	}
	status := tmp.Info.Status
	end := tmp.Info.Ongoing.End
	if status == "Finished" {
		end = tmp.Info.Finished.EndBlock
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
		"end_block": end,
	}).Error

	addParticipant(db, prop.ID, submitter)
	return nil
}

func addParticipant(db *gorm.DB, propID uint64, addr string) {
	_ = db.FirstOrCreate(&types.DaoMember{Address: addr}).Error
	_ = db.FirstOrCreate(&types.ProposalParticipant{
		ProposalID: propID,
		Address:    addr,
	}).Error
}
