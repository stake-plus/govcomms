package router

import (
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/stake-plus/polkadot-gov-comms/src/api/handlers"
	"github.com/stake-plus/polkadot-gov-comms/src/api/middleware"
)

func Attach(r *gin.Engine, db *gorm.DB, rdb *redis.Client, secret []byte) {
	auth := handlers.AuthHandler{Rdb: rdb, JWTSecret: secret}
	msgH := handlers.Msg{DB: db}

	v1 := r.Group("/v1")
	{
		v1.POST("/auth/challenge", auth.Challenge)
		v1.POST("/auth/verify", auth.Verify)

		secured := v1.Use(middleware.JWT(secret))
		secured.POST("/messages", msgH.Create)
		secured.GET("/messages/:net/:id", msgH.List)
	}
}
