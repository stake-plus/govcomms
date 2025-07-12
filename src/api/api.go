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
)

func main() {
	cfg := config.Load()

	db := data.MustMySQL(cfg.MySQLDSN)

	// SKIP GORM MIGRATION ENTIRELY - comment out this line
	// migrate(db)

	// Just ensure the networks exist
	var networkCount int64
	db.Model(&types.Network{}).Count(&networkCount)
	if networkCount == 0 {
		// Insert initial data only if not exists
		db.Exec(`INSERT IGNORE INTO networks (id, name, symbol, url) VALUES (1, 'Polkadot', 'DOT', 'https://polkadot.network')`)
		db.Exec(`INSERT IGNORE INTO networks (id, name, symbol, url) VALUES (2, 'Kusama', 'KSM', 'https://kusama.network')`)
		db.Exec(`INSERT IGNORE INTO network_rpcs (network_id, url, active) VALUES (1, ?, 1)`, cfg.RPCURL)
	}

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
