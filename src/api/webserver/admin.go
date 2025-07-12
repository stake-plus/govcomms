package webserver

import (
	"net/http"

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
		NetworkID        uint8  `json:"networkId" binding:"required"`
		DiscordChannelID string `json:"discordChannelId" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"err": err.Error()})
		return
	}

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

		// You could add an admin flag to dao_members table or check a specific role
		// For now, let's assume only specific addresses are admins
		// This should be stored in the database instead
		adminAddresses := []string{
			// Add admin addresses here or create an admins table
		}

		isAdmin := false
		for _, addr := range adminAddresses {
			if addr == userAddr {
				isAdmin = true
				break
			}
		}

		if !isAdmin {
			c.JSON(http.StatusForbidden, gin.H{"err": "admin access required"})
			c.Abort()
			return
		}

		c.Next()
	}
}
