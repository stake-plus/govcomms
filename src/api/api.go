package main

import (
	"log"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"

	"github.com/stake-plus/polkadot-gov-comms/src/api/db"
	"github.com/stake-plus/polkadot-gov-comms/src/api/router"
)

func main() {
	_ = godotenv.Load(".env") // optional

	dsn := os.Getenv("MYSQL_DSN")
	redisURL := os.Getenv("REDIS_URL")
	jwtSecret := os.Getenv("JWT_SECRET")

	sqlDB := db.MustMySQL(dsn)
	rdb := db.MustRedis(redisURL)

	engine := gin.New()
	engine.Use(gin.Logger(), gin.Recovery())

	router.Attach(engine, sqlDB, rdb, []byte(jwtSecret))

	if err := engine.Run(":8080"); err != nil {
		log.Fatal(err)
	}
}
