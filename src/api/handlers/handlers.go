// internal/handlers/auth.go
package handlers

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"github.com/stake-plus/polkadot-gov-comms/src/api/auth"
)

type AuthHandler struct {
	Rdb       *redis.Client
	JWTSecret string
}

type reqChallenge struct {
	Address string `json:"address" binding:"required"`
	Method  string `json:"method"  binding:"required"`
}

func (h AuthHandler) Challenge(c *gin.Context) {
	var req reqChallenge
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"err": err.Error()})
		return
	}
	nonce, err := auth.GenerateNonce(c, h.Rdb, req.Address)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"err": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"nonce": nonce})
}

type reqVerify struct {
	Address   string `json:"address"   binding:"required"`
	Signature string `json:"signature" binding:"required"`
}

func (h AuthHandler) Verify(c *gin.Context) {
	var req reqVerify
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"err": err.Error()})
		return
	}
	jwtStr, err := auth.CheckAndDeleteNonce(
		context.Background(), h.Rdb, req.Address, req.Signature, h.JWTSecret,
	)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"err": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": jwtStr})
}
