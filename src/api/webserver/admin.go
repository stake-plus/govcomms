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
		GuildID     string `json:"guildId" binding:"required"`
		ChannelID   string `json:"channelId" binding:"required"`
		NetworkID   uint8  `json:"networkId" binding:"required"`
		ChannelType string `json:"channelType" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"err": err.Error()})
		return
	}

	channel := types.DiscordChannel{
		GuildID:     req.GuildID,
		ChannelID:   req.ChannelID,
		NetworkID:   req.NetworkID,
		ChannelType: req.ChannelType,
	}

	// Upsert
	if err := a.db.Where("guild_id = ? AND network_id = ? AND channel_type = ?",
		req.GuildID, req.NetworkID, req.ChannelType).
		Assign(channel).
		FirstOrCreate(&channel).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"err": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"id": channel.ID})
}
