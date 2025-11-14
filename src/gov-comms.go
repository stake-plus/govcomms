package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/stake-plus/govcomms/src/actions"
	shareddata "github.com/stake-plus/govcomms/src/shared/data"
)

func main() {
	enableQA := flag.Bool("enable-qa", envBool("ENABLE_QA", true), "Enable AI Q&A bot")
	enableResearch := flag.Bool("enable-research", envBool("ENABLE_RESEARCH", true), "Enable Research bot")
	enableFeedback := flag.Bool("enable-feedback", envBool("ENABLE_FEEDBACK", true), "Enable Feedback bot")
	flag.Parse()

	// Use a single DB connection for all modules
	dsn := shareddata.GetMySQLDSN()
	db, err := shareddata.ConnectMySQL(dsn)
	if err != nil {
		log.Fatalf("db: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	manager, err := actions.StartAll(ctx, db, actions.Options{
		EnableQA:       *enableQA,
		EnableResearch: *enableResearch,
		EnableFeedback: *enableFeedback,
	})
	if err != nil {
		log.Fatalf("actions start: %v", err)
	}

	// Wait for termination
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	<-sigs

	manager.Stop(ctx)
}

func envBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	if v == "1" || v == "true" || v == "TRUE" {
		return true
	}
	return false
}
