package webserver

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stake-plus/polkadot-gov-comms/src/GCApi/data"
	"github.com/stake-plus/polkadot-gov-comms/src/GCApi/types"
	"gorm.io/gorm"
)

type Auth struct {
	rdb       *redis.Client
	jwtSecret []byte
	db        *gorm.DB // Add this
}

func NewAuth(rdb *redis.Client, secret []byte, db *gorm.DB) Auth {
	return Auth{rdb: rdb, jwtSecret: secret, db: db}
}

func randomHex32() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "0x" + hex.EncodeToString(b), nil
}

func (a Auth) Challenge(c *gin.Context) {
	var req struct {
		Address string `json:"address" binding:"required,min=32,max=128"`
		Method  string `json:"method"  binding:"required,oneof=walletconnect polkadotjs airgap"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"err": err.Error()})
		return
	}

	// Log authentication attempt
	log.Printf("Auth challenge for %s from IP %s using %s", req.Address, c.ClientIP(), req.Method)

	// Remove the address validation - signature verification will catch invalid addresses

	var nonce string
	var err error
	switch req.Method {
	case "polkadotjs", "walletconnect":
		// Polkadot{.js} expects raw HEX data for signRaw → generate 32-byte hex
		nonce, err = randomHex32()
	default:
		// Air-gap remark still fine with UUID (human readable)
		nonce = uuid.NewString()
	}

	if err != nil {
		log.Printf("Failed to create nonce: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"err": "failed to create challenge"})
		return
	}

	if err := data.SetNonce(c, a.rdb, req.Address, nonce); err != nil {
		log.Printf("Failed to set nonce for %s: %v", req.Address, err)
		c.JSON(http.StatusInternalServerError, gin.H{"err": "failed to create challenge"})
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
		RefID     string `json:"refId,omitempty"`   // Add optional refId
		Network   string `json:"network,omitempty"` // Add optional network
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"err": err.Error()})
		return
	}

	// Validate address format
	if !isValidPolkadotAddress(req.Address) {
		c.JSON(http.StatusBadRequest, gin.H{"err": "invalid address format"})
		return
	}

	log.Printf("Verify attempt for %s using method %s", req.Address, req.Method)

	// For non-airgap methods, get nonce without deleting it first
	var nonce string
	var err error
	if req.Method != "airgap" {
		// Peek at the nonce without deleting
		nonce, err = data.GetNonce(c, a.rdb, req.Address)
		if err != nil {
			log.Printf("Failed to get nonce for %s: %v", req.Address, err)
			c.JSON(http.StatusUnauthorized, gin.H{"err": "challenge expired or not found"})
			return
		}
	}

	var token string
	switch req.Method {
	case "airgap":
		// For airgap, check if nonce is confirmed
		confirmedNonce, err := data.GetNonce(c, a.rdb, req.Address)
		if err != nil || confirmedNonce != "CONFIRMED" {
			log.Printf("Airgap remark not confirmed for %s", req.Address)
			c.JSON(http.StatusUnauthorized, gin.H{"err": "remark not confirmed"})
			return
		}
		// Delete the nonce after successful confirmation
		data.DelNonce(c, a.rdb, req.Address)

	default: // polkadotjs | walletconnect
		log.Printf("Verifying signature for %s with nonce %s", req.Address, nonce)
		if err := verifySignature(req.Address, req.Signature, nonce); err != nil {
			log.Printf("Signature verification failed for %s: %v", req.Address, err)
			c.JSON(http.StatusUnauthorized, gin.H{"err": "bad signature"})
			return
		}
		// Only delete nonce after successful verification
		data.DelNonce(c, a.rdb, req.Address)
	}

	// If a specific referendum is provided, check authorization
	if req.RefID != "" && req.Network != "" {
		// Validate network
		if req.Network != "polkadot" && req.Network != "kusama" {
			c.JSON(http.StatusBadRequest, gin.H{"err": "invalid network"})
			return
		}

		netID := uint8(1)
		if req.Network == "kusama" {
			netID = 2
		}

		refNum, err := strconv.ParseUint(req.RefID, 10, 64)
		if err == nil {
			// Check if user is authorized for this referendum
			var ref types.Ref
			if err := a.db.First(&ref, "network_id = ? AND ref_id = ?", netID, refNum).Error; err == nil {
				// Check if user is a proponent
				var auth types.RefProponent
				if err := a.db.First(&auth, "ref_id = ? AND address = ?", ref.ID, req.Address).Error; err != nil {
					// Not a proponent - check if DAO member
					var daoMember types.DaoMember
					if err := a.db.First(&daoMember, "address = ?", req.Address).Error; err != nil {
						c.JSON(http.StatusForbidden, gin.H{
							"err":     "not_authorized",
							"message": "You are not authorized for this referendum",
						})
						return
					}
				}
			}
		}
	}

	token, err = issueJWT(req.Address, a.jwtSecret)
	if err != nil {
		log.Printf("Failed to issue JWT for %s: %v", req.Address, err)
		c.JSON(http.StatusInternalServerError, gin.H{"err": err.Error()})
		return
	}

	log.Printf("Successfully authenticated %s", req.Address)
	c.JSON(http.StatusOK, gin.H{"token": token})
}

func isValidPolkadotAddress(addr string) bool {
	// Basic validation - starts with expected prefix and has reasonable length
	if strings.HasPrefix(addr, "0x") {
		// Hex format validation
		hexStr := strings.TrimPrefix(addr, "0x")
		if len(hexStr) != 64 { // 32 bytes = 64 hex chars
			return false
		}
		// Check if valid hex
		_, err := hex.DecodeString(hexStr)
		return err == nil
	}
	// SS58 addresses can vary in length (typically 47-50 chars)
	// Polkadot addresses typically start with '1', Kusama with capital letters
	if len(addr) < 46 || len(addr) > 52 {
		return false
	}
	// Check for valid SS58 characters (base58)
	validChars := "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
	for _, char := range addr {
		if !strings.ContainsRune(validChars, char) {
			return false
		}
	}
	return true
}
