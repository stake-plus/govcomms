package data

import (
	"context"
	"log"
	"time"

	sar "github.com/itering/substrate-api-rpc"
	"github.com/itering/substrate-api-rpc/expand"
	"github.com/itering/substrate-api-rpc/model"
	"gorm.io/gorm"

	"github.com/stake-plus/polkadot-gov-comms/src/api/config"
	"github.com/stake-plus/polkadot-gov-comms/src/api/types"
)

func StartIndexer(ctx context.Context, db *gorm.DB, cfg config.Config) {
	tick := time.NewTicker(time.Duration(cfg.PollInterval) * time.Second)
	defer tick.Stop()

	for {
		select {
		case <-tick.C:
			indexPolkadot(ctx, db, cfg.RPCURL)
		case <-ctx.Done():
			return
		}
	}
}

func indexPolkadot(ctx context.Context, db *gorm.DB, rpcURL string) {
	api, err := sar.NewSubstrateAPI(rpcURL)
	if err != nil {
		log.Printf("indexer connect: %v", err)
		return
	}

	var lastIdx uint32
	if err := api.RPC.State.GetStorageLatest("Referenda.ReferendumCount", &lastIdx); err != nil {
		log.Printf("ref count: %v", err)
		return
	}

	networkID := uint8(1)

	for i := uint32(0); i < lastIdx; i++ {
		var raw model.StorageDataRaw
		if err := api.RPC.State.GetStorageLatest(expand.RefInfoKey(i), &raw); err != nil || raw.IsEmpty() {
			continue
		}
		info, err := expand.ParseReferendumInfo(raw)
		if err != nil || info.Status != "Ongoing" {
			continue
		}

		var prop types.Proposal
		if err := db.FirstOrCreate(&prop, types.Proposal{
			NetworkID: networkID,
			RefID:     uint64(i),
		}).Error; err != nil {
			continue
		}
		if info.Submitter != "" {
			_ = db.Model(&prop).Update("submitter", info.Submitter).Error
		}
	}
}
