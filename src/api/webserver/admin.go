package webserver

import (
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/stake-plus/polkadot-gov-comms/src/api/types"
	"gorm.io/gorm"
)

type Admin struct {
	db *gorm.DB
}

func NewAdmin(db *gorm.DB) Admin {
	return Admin{db: db}
}

func (a Admin) SetDiscordChannel(c *gin.Context) {
	var req struct {
		NetworkID        uint8  `json:"networkId" binding:"required,min=1,max=255"`
		DiscordChannelID string `json:"discordChannelId" binding:"required,min=10,max=30"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"err": err.Error()})
		return
	}

	// Validate Discord channel ID format (should be numeric)
	if _, err := strconv.ParseUint(req.DiscordChannelID, 10, 64); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"err": "invalid Discord channel ID"})
		return
	}

	// Verify network exists
	var network types.Network
	if err := a.db.First(&network, "id = ?", req.NetworkID).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"err": "network not found"})
		return
	}

	// Log admin action
	log.Printf("Admin %s updating Discord channel for network %d to %s",
		c.GetString("addr"), req.NetworkID, req.DiscordChannelID)

	// Update network's discord channel
	if err := a.db.Model(&types.Network{}).Where("id = ?", req.NetworkID).
		Update("discord_channel_id", req.DiscordChannelID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"err": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// Add new middleware function
func AdminMiddleware(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userAddr := c.GetString("addr")

		// Check if user is a DAO member with admin role
		var daoMember types.DaoMember
		if err := db.First(&daoMember, "address = ?", userAddr).Error; err != nil {
			c.JSON(http.StatusForbidden, gin.H{"err": "admin access required"})
			c.Abort()
			return
		}

		// Check if user has admin privileges
		if !daoMember.IsAdmin {
			c.JSON(http.StatusForbidden, gin.H{"err": "admin access required"})
			c.Abort()
			return
		}

		c.Next()
	}
}
