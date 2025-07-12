package webserver

import (
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stake-plus/polkadot-gov-comms/src/api/config"
	"gorm.io/gorm"
)

func attachRoutes(r *gin.Engine, cfg config.Config, db *gorm.DB, rdb *redis.Client) {
	// Add CORS middleware
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"http://localhost:3000", "https://govcomms.chaosdao.org"},
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
	{
		adminH := NewAdmin(db)
		admin.POST("/discord/channel", adminH.SetDiscordChannel)
	}
}
