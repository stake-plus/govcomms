package webserver

import (
	"log"
	"strings"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stake-plus/polkadot-gov-comms/src/api/config"
	"github.com/stake-plus/polkadot-gov-comms/src/api/data"
	"gorm.io/gorm"
)

// Update attachRoutes function
func attachRoutes(r *gin.Engine, cfg config.Config, db *gorm.DB, rdb *redis.Client) {
	// Load settings
	if err := data.LoadSettings(db); err != nil {
		log.Printf("Failed to load settings: %v", err)
	}

	// Get allowed origins from settings
	gcURL := data.GetSetting("gc_url")
	gcapiURL := data.GetSetting("gcapi_url")

	// Development defaults if not set
	if gcURL == "" {
		gcURL = "http://localhost:3000"
	}

	if gcapiURL == "" {
		gcapiURL = "http://localhost:443"
	}

	allowedOrigins := []string{"http://localhost:3000"} // Always allow localhost for dev
	if gcURL != "" {
		allowedOrigins = append(allowedOrigins, gcURL)
	}

	if gcapiURL != "" {
		allowedOrigins = append(allowedOrigins, gcapiURL)
	}

	// Always allow localhost for development
	if !strings.Contains(gcURL, "localhost") {
		allowedOrigins = append(allowedOrigins, "http://localhost:3000")
	}

	// Add CORS middleware
	r.Use(cors.New(cors.Config{
		AllowOrigins:     allowedOrigins,
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
	}))

	authH := NewAuth(rdb, []byte(cfg.JWTSecret))
	msgH := NewMessages(db, rdb)
	voteH := NewVotes(db)

	v1 := r.Group("/v1")
	{
		v1.POST("/auth/challenge", authH.Challenge)
		v1.POST("/auth/verify", authH.Verify)

		secured := v1.Use(JWTMiddleware([]byte(cfg.JWTSecret)))
		secured.POST("/messages", msgH.Create)
		secured.GET("/messages/:net/:id", msgH.List)
		secured.POST("/votes", voteH.Cast)
		secured.GET("/votes/:net/:id", voteH.Summary)
	}

	// Add in attachRoutes function
	admin := v1.Group("/admin")
	admin.Use(JWTMiddleware([]byte(cfg.JWTSecret)))
	admin.Use(AdminMiddleware(db)) // Add admin middleware
	{
		adminH := NewAdmin(db)
		admin.POST("/discord/channel", adminH.SetDiscordChannel)
	}
}
