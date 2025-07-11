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
	&types.EmailSubscription{},
}

func migrate(db *gorm.DB) {
	err := db.AutoMigrate(allModels...)
	if err == nil {
		return // first attempt succeeded
	}
	log.Printf("auto‑migrate failed (%v) – dropping & recreating schema", err)
	_ = db.Migrator().DropTable(
		"email_subscriptions", "messages", "votes",
		"proposal_participants", "proposals", "dao_members",
		"rpcs", "networks",
	)
	if err2 := db.AutoMigrate(allModels...); err2 != nil {
		log.Fatalf("migrate after drop: %v", err2)
	}
}

func main() {
	cfg := config.Load()
	db := data.MustMySQL(cfg.MySQLDSN)
	migrate(db)

	// Seed Polkadot network + RPC if absent
	_ = db.FirstOrCreate(&types.Network{ID: 1}, types.Network{
		ID: 1, Name: "Polkadot", Symbol: "DOT", URL: "https://polkadot.network",
	}).Error
	_ = db.FirstOrCreate(&types.RPC{}, types.RPC{
		NetworkID: 1,
		// Use the canonical parity endpoint – dotters has occasional state issues
		URL:    "wss://rpc.polkadot.io",
		Active: true,
	}).Error

	rdb := data.MustRedis(cfg.RedisURL)

	ctx, cancel := context.WithCancel(context.Background())
	go data.StartRemarkWatcher(ctx, cfg.RPCURL, rdb)
	go data.StartIndexer(ctx, db, cfg)

	router := webserver.New(cfg, db, rdb)
	httpSrv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: router,
	}

	// start server
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http: %v", err)
		}
	}()
	log.Printf("GovComms API listening on %s", cfg.Port)

	// graceful shutdown
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	cancel()

	shutCtx, cancelShut := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShut()
	_ = httpSrv.Shutdown(shutCtx)
}
