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
		return
	}

	log.Printf("auto‑migrate failed (%v) – dropping & recreating schema", err)
	_ = db.Migrator().DropTable(
		"email_subscriptions", "messages", "votes",
		"proposal_participants", "proposals", "dao_members",
		"rpcs", "networks",
	)
	if err := db.AutoMigrate(allModels...); err != nil {
		log.Fatalf("migrate after drop: %v", err)
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
	ensurePolkadotRPC(db, "wss://rpc.polkadot.io")

	rdb := data.MustRedis(cfg.RedisURL)

	ctx, cancel := context.WithCancel(context.Background())
	go data.StartRemarkWatcher(ctx, cfg.RPCURL, rdb)
	go data.RunPolkadotIndexer(ctx)

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
