package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/stake-plus/govcomms/src/actions"
	"github.com/stake-plus/govcomms/src/agents"
	_ "github.com/stake-plus/govcomms/src/ai/providers"
	cachepkg "github.com/stake-plus/govcomms/src/cache"
	sharedconfig "github.com/stake-plus/govcomms/src/config"
	shareddata "github.com/stake-plus/govcomms/src/data"
	"github.com/stake-plus/govcomms/src/mcp"
	"gorm.io/gorm"
)

func main() {
	// Use a single DB connection for all modules
	dsn, err := shareddata.GetMySQLDSN()
	if err != nil {
		log.Fatalf("mysql dsn: %v", err)
	}
	db, err := shareddata.ConnectMySQL(dsn)
	if err != nil {
		log.Fatalf("db: %v", err)
	}

	if err := shareddata.LoadSettings(db); err != nil {
		log.Printf("settings load failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mcpServer := startMCPServer(ctx, db)

	actionManager, err := actions.StartAll(ctx, db)
	if err != nil {
		log.Fatalf("actions start: %v", err)
	}

	agentManager, err := agents.StartAll(ctx, db)
	if err != nil {
		log.Fatalf("agents start: %v", err)
	}

	// Wait for termination
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	<-sigs

	if actionManager != nil {
		actionManager.Stop(ctx)
	}
	if agentManager != nil {
		agentManager.Stop(ctx)
	}
	if mcpServer != nil {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := mcpServer.Stop(shutdownCtx); err != nil {
			log.Printf("mcp: shutdown error: %v", err)
		}
	}

	sqlDB, err := db.DB()
	if err != nil {
		log.Printf("db: failed to get sql.DB: %v", err)
	} else if sqlDB != nil {
		if err := sqlDB.Close(); err != nil {
			log.Printf("db: close error: %v", err)
		}
	}
}

func startMCPServer(ctx context.Context, db *gorm.DB) *mcp.Server {
	cfg := sharedconfig.LoadMCPConfig(db)
	if !cfg.Enabled {
		log.Printf("mcp: disabled via configuration")
		return nil
	}

	cacheManager, err := cachepkg.NewManager(cfg.CacheDir)
	if err != nil {
		log.Printf("mcp: cache init failed: %v", err)
		return nil
	}
	contextStore := cachepkg.NewContextStore(db)

	logger := log.New(os.Stdout, "[mcp] ", log.LstdFlags|log.Lmsgprefix)
	server, err := mcp.NewServer(mcp.Config{
		ListenAddr: cfg.Listen,
		AuthToken:  cfg.AuthToken,
		Logger:     logger,
	}, cacheManager, contextStore)
	if err != nil {
		log.Printf("mcp: server init failed: %v", err)
		return nil
	}

	go func() {
		if err := server.Start(ctx); err != nil &&
			!errors.Is(err, context.Canceled) &&
			!errors.Is(err, http.ErrServerClosed) {
			log.Printf("mcp: server stopped: %v", err)
		}
	}()

	return server
}
