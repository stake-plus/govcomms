package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/stake-plus/govcomms/src/feedback/bot"
	"github.com/stake-plus/govcomms/src/feedback/config"
	"github.com/stake-plus/govcomms/src/feedback/data"
)

func main() {
	mysqlDSN := os.Getenv("MYSQL_DSN")
	if mysqlDSN == "" {
		mysqlDSN = "govcomms:DK3mfv93jf4m@tcp(127.0.0.1:3306)/govcomms"
	}

	db := data.MustMySQL(mysqlDSN)

	if err := data.LoadSettings(db); err != nil {
		log.Printf("Failed to load settings: %v", err)
	}

	cfg := config.Load(db)

	if cfg.Token == "" {
		log.Fatal("DISCORD_TOKEN not set in database or environment")
	}

	if cfg.FeedbackRoleID == "" {
		log.Fatal("FEEDBACK_ROLE_ID not set in database or environment")
	}

	if cfg.GuildID == "" {
		log.Fatal("GUILD_ID not set in database or environment")
	}

	rdb := data.MustRedis(cfg.RedisURL)

	b, err := bot.New(&cfg, db, rdb)
	if err != nil {
		log.Fatalf("Failed to create bot: %v", err)
	}

	if err := b.Start(); err != nil {
		log.Fatalf("Failed to start bot: %v", err)
	}

	log.Println("Discord bot is running. Press CTRL-C to exit.")

	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc

	b.Stop()
	log.Println("Discord bot stopped gracefully")
}
