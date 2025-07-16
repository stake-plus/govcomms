package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/stake-plus/govcomms/src/GCApi/data"
	"github.com/stake-plus/govcomms/src/GCBot/bot"
)

func main() {
	// Connect to database first
	mysqlDSN := os.Getenv("MYSQL_DSN")
	if mysqlDSN == "" {
		mysqlDSN = "govcomms:DK3mfv93jf4m@tcp(127.0.0.1:3306)/govcomms"
	}
	db := data.MustMySQL(mysqlDSN)

	// Load settings from database
	if err := data.LoadSettings(db); err != nil {
		log.Printf("Failed to load settings: %v", err)
	}

	// Get configuration from database with env fallbacks
	token := data.GetSetting("discord_token")
	if token == "" {
		token = os.Getenv("DISCORD_TOKEN")
		if token == "" {
			log.Fatal("DISCORD_TOKEN not set in database or environment")
		}
	}

	feedbackRoleID := data.GetSetting("feedback_role_id")
	if feedbackRoleID == "" {
		feedbackRoleID = os.Getenv("FEEDBACK_ROLE_ID")
		if feedbackRoleID == "" {
			log.Fatal("FEEDBACK_ROLE_ID not set in database or environment")
		}
	}

	guildID := data.GetSetting("guild_id")
	if guildID == "" {
		guildID = os.Getenv("GUILD_ID")
		if guildID == "" {
			log.Fatal("GUILD_ID not set in database or environment")
		}
	}

	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://127.0.0.1:6379/0"
	}

	// Connect to Redis
	rdb := data.MustRedis(redisURL)

	// Create bot config
	config := bot.Config{
		Token:          token,
		FeedbackRoleID: feedbackRoleID,
		GuildID:        guildID,
		DB:             db,
		Redis:          rdb,
	}

	// Create bot
	b, err := bot.New(config)
	if err != nil {
		log.Fatalf("Failed to create bot: %v", err)
	}

	if err := b.Start(); err != nil {
		log.Fatalf("Failed to start bot: %v", err)
	}

	log.Println("Discord bot is running. Press CTRL-C to exit.")

	// Wait for interrupt
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc

	b.Stop()
	log.Println("Discord bot stopped gracefully")
}
