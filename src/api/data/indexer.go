package data

import (
	"context"
	"log"
	"strconv"
	"time"

	"github.com/itering/substrate-api-rpc/rpc"
	"github.com/itering/substrate-api-rpc/websocket"
	"gorm.io/gorm"

	"github.com/stake-plus/polkadot-gov-comms/src/api/config"
	"github.com/stake-plus/polkadot-gov-comms/src/api/types"
)

// StartIndexer launches a ticker that queries on‑chain referendum data
// and stores it in MySQL.
func StartIndexer(ctx context.Context, db *gorm.DB, cfg config.Config) {
	websocket.SetEndpoint(cfg.RPCURL)

	tick := time.NewTicker(time.Duration(cfg.PollInterval) * time.Second)
	defer tick.Stop()

	for {
		select {
		case <-tick.C:
			indexPolkadot(db)
		case <-ctx.Done():
			return
		}
	}
}

// indexPolkadot synchronises all “Ongoing” referenda from the chain into DB.
func indexPolkadot(db *gorm.DB) {
	pooled, err := websocket.Init()
	if err != nil {
		log.Printf("indexer: websocket connect: %v", err)
		return
	}
	defer pooled.Close()

	conn := pooled.Conn // recws.RecConn implements websocket.WsConn

	// Obtain the referendum counter.
	countRes, err := rpc.ReadStorage(conn, "Referenda", "ReferendumCount", "")
	if err != nil {
		log.Printf("indexer: ReferendumCount: %v", err)
		return
	}
	lastIdx := countRes.ToU32FromCodec()

	const networkID uint8 = 1 // Polkadot

	for i := uint32(0); i < lastIdx; i++ {
		infoRes, err := rpc.ReadStorage(
			conn, "Referenda", "ReferendumInfoFor", "", strconv.FormatUint(uint64(i), 10),
		)
		if err != nil {
			continue
		}
		// Empty storage → no referendum at that index.
		if infoRes.ToString() == "" {
			continue
		}

		var info map[string]interface{}
		infoRes.ToAny(&info)

		if status, ok := info["status"].(string); ok && status != "Ongoing" {
			continue
		}

		var prop types.Proposal
		if err := db.FirstOrCreate(&prop, types.Proposal{
			NetworkID: networkID,
			RefID:     uint64(i),
		}).Error; err != nil {
			continue
		}

		if sub, ok := info["submitter"].(string); ok && sub != "" && prop.Submitter == "" {
			_ = db.Model(&prop).Update("submitter", sub).Error
		}
	}
}
