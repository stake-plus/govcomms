package webserver

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stake-plus/polkadot-gov-comms/src/api/data"
)

type Auth struct {
	rdb       *redis.Client
	jwtSecret []byte
}

func NewAuth(rdb *redis.Client, secret []byte) Auth {
	return Auth{rdb: rdb, jwtSecret: secret}
}

func (a Auth) Challenge(c *gin.Context) {
	var req struct {
		Address string `json:"address" binding:"required"`
		Method  string `json:"method"  binding:"required,oneof=walletconnect polkadotjs airgap"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"err": err.Error()})
		return
	}

	nonce := uuid.NewString()
	if err := data.SetNonce(c, a.rdb, req.Address, nonce); err != nil {
		log.Printf("Failed to set nonce for %s: %v", req.Address, err)
		c.JSON(http.StatusInternalServerError, gin.H{"err": "Failed to create challenge"})
		return
	}

	log.Printf("Challenge created for %s with nonce %s", req.Address, nonce)
	c.JSON(http.StatusOK, gin.H{"nonce": nonce})
}

func (a Auth) Verify(c *gin.Context) {
	var req struct {
		Address   string `json:"address"   binding:"required"`
		Method    string `json:"method"    binding:"required,oneof=walletconnect polkadotjs airgap"`
		Signature string `json:"signature"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"err": err.Error()})
		return
	}

	log.Printf("Verify attempt for %s using method %s", req.Address, req.Method)

	nonce, err := data.GetAndDelNonce(c, a.rdb, req.Address)
	if err != nil {
		log.Printf("Failed to get nonce for %s: %v", req.Address, err)
		c.JSON(http.StatusUnauthorized, gin.H{"err": "challenge expired or not found"})
		return
	}

	var token string
	switch req.Method {
	case "airgap":
		if nonce != "CONFIRMED" {
			log.Printf("Airgap remark not confirmed for %s", req.Address)
			c.JSON(http.StatusUnauthorized, gin.H{"err": "remark not confirmed"})
			return
		}
		token, err = issueJWT(req.Address, a.jwtSecret)
	default:
		log.Printf("Verifying signature for %s with nonce %s", req.Address, nonce)
		if err := verifySignature(req.Address, req.Signature, nonce); err != nil {
			log.Printf("Signature verification failed for %s: %v", req.Address, err)
			c.JSON(http.StatusUnauthorized, gin.H{"err": "bad signature"})
			return
		}
		token, err = issueJWT(req.Address, a.jwtSecret)
	}

	if err != nil {
		log.Printf("Failed to issue JWT for %s: %v", req.Address, err)
		c.JSON(http.StatusInternalServerError, gin.H{"err": err.Error()})
		return
	}

	log.Printf("Successfully authenticated %s", req.Address)
	c.JSON(http.StatusOK, gin.H{"token": token})
}
