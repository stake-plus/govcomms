package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/stake-plus/govcomms/src/actions"
	"github.com/stake-plus/govcomms/src/agents"
	_ "github.com/stake-plus/govcomms/src/ai/providers"
	shareddata "github.com/stake-plus/govcomms/src/data"
)

func main() {
	// Use a single DB connection for all modules
	dsn := shareddata.GetMySQLDSN()
	db, err := shareddata.ConnectMySQL(dsn)
	if err != nil {
		log.Fatalf("db: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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
}
