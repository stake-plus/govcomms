package webserver

import (
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/stake-plus/polkadot-gov-comms/src/api/config"
)

func attachRoutes(r *gin.Engine, cfg config.Config, db *gorm.DB, rdb *redis.Client) {
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
}
