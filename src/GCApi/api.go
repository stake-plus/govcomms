// File: src/api/api.go

package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/stake-plus/govcomms/src/GCApi/config"
	"github.com/stake-plus/govcomms/src/GCApi/data"
	"github.com/stake-plus/govcomms/src/GCApi/types"
	"github.com/stake-plus/govcomms/src/GCApi/webserver"
)

func main() {
	// Connect to database first
	mysqlDSN := os.Getenv("MYSQL_DSN")
	if mysqlDSN == "" {
		mysqlDSN = "dev:test@tcp(localhost:3306)/govcomms"
	}
	db := data.MustMySQL(mysqlDSN)

	// Load settings from database
	if err := data.LoadSettings(db); err != nil {
		log.Printf("Failed to load settings: %v", err)
	}

	// Ensure settings exist
	var settingsCount int64
	db.Model(&types.Setting{}).Count(&settingsCount)

	// Load config with database
	cfg := config.Load(db)
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

	// Create HTTP server
	httpSrv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server
	go func() {
		var err error
		if cfg.EnableSSL && cfg.SSLCert != "" && cfg.SSLKey != "" {
			log.Printf("Starting HTTPS server on port %s", cfg.Port)
			// Create TLS reloader
			tlsReloader, err := webserver.NewTLSReloader(cfg.SSLCert, cfg.SSLKey)
			if err != nil {
				log.Printf("Failed to create TLS reloader: %v. Falling back to HTTP", err)
				log.Printf("Starting HTTP server on port %s", cfg.Port)
				err = httpSrv.ListenAndServe()
			} else {
				// Use custom TLS config
				httpSrv.TLSConfig = tlsReloader.GetConfig()
				// ListenAndServeTLS with empty cert/key paths since we're using GetCertificate
				err = httpSrv.ListenAndServeTLS("", "")
			}
		} else {
			log.Printf("Starting HTTP server on port %s (SSL not configured)", cfg.Port)
			err = httpSrv.ListenAndServe()
		}

		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("http: %v", err)
		}
	}()

	log.Printf("GovComms API listening on %s (SSL: %v)", cfg.Port, cfg.EnableSSL && cfg.SSLCert != "" && cfg.SSLKey != "")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	cancel()
	shutCtx, cancelShut := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShut()
	_ = httpSrv.Shutdown(shutCtx)
}
