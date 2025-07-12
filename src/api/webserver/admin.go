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
