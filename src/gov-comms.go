package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	aiqabot "github.com/stake-plus/govcomms/src/ai-qa/bot"
	fbbot "github.com/stake-plus/govcomms/src/feedback/bot"
	fbdata "github.com/stake-plus/govcomms/src/feedback/data"
	rbbot "github.com/stake-plus/govcomms/src/research-bot/bot"
	sharedconfig "github.com/stake-plus/govcomms/src/shared/config"
	shareddata "github.com/stake-plus/govcomms/src/shared/data"
)

func main() {
	enableQA := flag.Bool("enable-qa", envBool("ENABLE_QA", true), "Enable AI Q&A bot")
	enableResearch := flag.Bool("enable-research", envBool("ENABLE_RESEARCH", true), "Enable Research bot")
	enableFeedback := flag.Bool("enable-feedback", envBool("ENABLE_FEEDBACK", false), "Enable Feedback bot")
	flag.Parse()

	// Use a single DB connection for all modules
	dsn := shareddata.GetMySQLDSN()
	db, err := shareddata.ConnectMySQL(dsn)
	if err != nil {
		log.Fatalf("db: %v", err)
	}

	// Start modules as requested
	var qa *aiqabot.Bot
	var research *rbbot.Bot
	var feedback *fbbot.Bot

	if *enableQA {
		cfg := sharedconfig.LoadQAConfig(db)
		qa, err = aiqabot.New(&cfg, db)
		if err != nil {
			log.Fatalf("qa bot: %v", err)
		}
		if err := qa.Start(); err != nil {
			log.Fatalf("qa start: %v", err)
		}
		log.Printf("AI Q&A started")
	}

	if *enableResearch {
		cfg := sharedconfig.LoadResearchConfig(db)
		research, err = rbbot.New(&cfg, db)
		if err != nil {
			log.Fatalf("research bot: %v", err)
		}
		if err := research.Start(); err != nil {
			log.Fatalf("research start: %v", err)
		}
		log.Printf("Research bot started")
	}

	if *enableFeedback {
		cfg := sharedconfig.LoadFeedbackConfig(db)
		rdb := fbdata.MustRedis(cfg.RedisURL)
		feedback, err = fbbot.New(&cfg, db, rdb)
		if err != nil {
			log.Fatalf("feedback bot: %v", err)
		}
		if err := feedback.Start(); err != nil {
			log.Fatalf("feedback start: %v", err)
		}
		log.Printf("Feedback bot started")
	}

	// Wait for termination
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	<-sigs

	if qa != nil {
		qa.Stop()
	}
	if research != nil {
		research.Stop()
	}
	if feedback != nil {
		feedback.Stop()
	}
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
