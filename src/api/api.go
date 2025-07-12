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
	&types.Network{},
	&types.NetworkRPC{},
	&types.DaoMember{},
	&types.Ref{},
	&types.RefMessage{},
	&types.RefProponent{},
	&types.RefSub{},
	&types.DaoVote{},
}

func migrate(db *gorm.DB) {
	// Disable foreign key constraints during migration
	db.Exec("SET FOREIGN_KEY_CHECKS = 0")
	defer db.Exec("SET FOREIGN_KEY_CHECKS = 1")

	// Just create/update table structure, don't let GORM manage foreign keys
	err := db.AutoMigrate(allModels...)
	if err != nil {
		log.Printf("auto-migrate failed: %v", err)
	}
}

func ensurePolkadotRPC(db *gorm.DB, url string) {
	// Upsert active Polkadot RPC; disable all others for the network.
	db.Model(&types.NetworkRPC{}).
		Where("network_id = ? AND url <> ?", 1, url).
		Update("active", false)

	var rpc types.NetworkRPC
	if err := db.FirstOrCreate(&rpc, types.NetworkRPC{
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
