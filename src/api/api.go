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

	// Load settings from database
	if err := data.LoadSettings(db); err != nil {
		log.Printf("Failed to load settings: %v", err)
	}

	// Ensure settings exist
	var settingsCount int64
	db.Model(&types.Setting{}).Count(&settingsCount)

	rdb := data.MustRedis(cfg.RedisURL)

	ctx, cancel := context.WithCancel(context.Background())

	// Start remark watcher - using first active RPC for Polkadot
	var polkadotRPC types.NetworkRPC
	if err := db.Where("network_id = ? AND active = ?", 1, true).First(&polkadotRPC).Error; err == nil {
		go data.StartRemarkWatcher(ctx, polkadotRPC.URL, rdb)
	} else {
		log.Printf("Warning: No active Polkadot RPC found for remark watcher")
	}

	// Start multi-network indexer service
	go data.IndexerService(ctx, db, time.Duration(cfg.PollInterval)*time.Second)

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
