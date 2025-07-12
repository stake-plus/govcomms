package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/stake-plus/polkadot-gov-comms/src/api/config"
	"github.com/stake-plus/polkadot-gov-comms/src/api/data"
	"github.com/stake-plus/polkadot-gov-comms/src/api/types"
	"github.com/stake-plus/polkadot-gov-comms/src/api/webserver"
	"gorm.io/gorm"
)

var allModels = []interface{}{
	&types.Network{}, &types.RPC{},
	&types.Proposal{}, &types.ProposalParticipant{},
	&types.Message{}, &types.DaoMember{}, &types.Vote{},
	&types.EmailSubscription{}, &types.Track{}, &types.Preimage{},
}

func migrate(db *gorm.DB) {
	// First, try to run migrations to update column sizes
	err := db.AutoMigrate(allModels...)
	if err != nil {
		log.Printf("auto-migrate failed (%v) — attempting to alter columns", err)

		// Try to alter columns directly for existing tables
		alterStatements := []string{
			"ALTER TABLE proposals MODIFY submitter VARCHAR(128)",
			"ALTER TABLE proposals MODIFY decision_deposit_who VARCHAR(128)",
			"ALTER TABLE proposals MODIFY submission_deposit_who VARCHAR(128)",
			"ALTER TABLE proposals ADD COLUMN IF NOT EXISTS tally_ayes VARCHAR(64)",
			"ALTER TABLE proposals ADD COLUMN IF NOT EXISTS tally_nays VARCHAR(64)",
			"ALTER TABLE proposals DROP COLUMN IF EXISTS support",
			"ALTER TABLE proposals DROP COLUMN IF EXISTS approval",
			"ALTER TABLE proposals DROP COLUMN IF EXISTS ayes",
			"ALTER TABLE proposals DROP COLUMN IF EXISTS nays",
			"ALTER TABLE proposals DROP COLUMN IF EXISTS turnout",
			"ALTER TABLE proposals DROP COLUMN IF EXISTS electorate",
			"ALTER TABLE proposal_participants MODIFY address VARCHAR(128)",
			"ALTER TABLE messages MODIFY author VARCHAR(128)",
			"ALTER TABLE dao_members MODIFY address VARCHAR(128)",
			"ALTER TABLE votes MODIFY voter_addr VARCHAR(128)",
			"ALTER TABLE preimages MODIFY hash VARCHAR(128)",
			"ALTER TABLE preimages MODIFY provider VARCHAR(128)",
		}

		for _, stmt := range alterStatements {
			if err := db.Exec(stmt).Error; err != nil {
				log.Printf("Failed to execute %s: %v", stmt, err)
			}
		}

		// Try migrations again
		if err := db.AutoMigrate(allModels...); err != nil {
			log.Printf("auto-migrate still failed after column alterations, dropping & recreating schema")

			// Drop and recreate
			_ = db.Migrator().DropTable(
				"email_subscriptions", "messages", "votes",
				"proposal_participants", "proposals", "dao_members",
				"tracks", "rpcs", "networks", "preimages",
			)

			if err := db.AutoMigrate(allModels...); err != nil {
				log.Fatalf("migrate after drop: %v", err)
			}
		}
	}
}

func ensurePolkadotRPC(db *gorm.DB, url string) {
	// Upsert active Polkadot RPC; disable all others for the network.
	db.Model(&types.RPC{}).
		Where("network_id = ? AND url <> ?", 1, url).
		Update("active", false)

	var rpc types.RPC
	if err := db.FirstOrCreate(&rpc, types.RPC{
		NetworkID: 1,
		URL:       url,
	}).Error; err == nil {
		db.Model(&rpc).Update("active", true)
	}
}

func main() {
	cfg := config.Load()
	db := data.MustMySQL(cfg.MySQLDSN)
	migrate(db)

	// Seed Polkadot network
	_ = db.FirstOrCreate(&types.Network{ID: 1}, types.Network{
		ID: 1, Name: "Polkadot", Symbol: "DOT", URL: "https://polkadot.network",
	}).Error
	ensurePolkadotRPC(db, cfg.RPCURL)

	rdb := data.MustRedis(cfg.RedisURL)

	ctx, cancel := context.WithCancel(context.Background())

	// Start remark watcher
	go data.StartRemarkWatcher(ctx, cfg.RPCURL, rdb)

	// Start indexer service
	go data.IndexerService(ctx, db, cfg.RPCURL, time.Duration(cfg.PollInterval)*time.Second)

	router := webserver.New(cfg, db, rdb)
	httpSrv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: router,
	}

	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http: %v", err)
		}
	}()

	log.Printf("GovComms API listening on %s", cfg.Port)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	cancel()
	shutCtx, cancelShut := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShut()
	_ = httpSrv.Shutdown(shutCtx)
}
