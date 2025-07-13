package webserver

import (
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/stake-plus/polkadot-gov-comms/src/GCApi/config"
)

func New(cfg config.Config, db *gorm.DB, rdb *redis.Client) *gin.Engine {
	g := gin.New()
	g.Use(gin.Logger(), gin.Recovery())
	attachRoutes(g, cfg, db, rdb)
	return g
}
