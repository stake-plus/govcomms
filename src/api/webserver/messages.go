package webserver

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/stake-plus/polkadot-gov-comms/src/api/data"
	"github.com/stake-plus/polkadot-gov-comms/src/api/types"
)

type Messages struct {
	db  *gorm.DB
	rdb *redis.Client
}

func NewMessages(db *gorm.DB, rdb *redis.Client) Messages { return Messages{db: db, rdb: rdb} }

func (m Messages) Create(c *gin.Context) {
	var req struct {
		Proposal string   `json:"proposalRef" binding:"required"`
		Body     string   `json:"body"        binding:"required"`
		Emails   []string `json:"emails"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"err": err.Error()})
		return
	}

	parts := strings.Split(req.Proposal, "/")
	if len(parts) != 2 {
		c.JSON(http.StatusBadRequest, gin.H{"err": "bad proposalRef"})
		return
	}
	refID, _ := strconv.ParseUint(parts[1], 10, 64)

	var netID uint8
	switch parts[0] {
	case "polkadot":
		netID = 1
	case "kusama":
		netID = 2
	default:
		c.JSON(http.StatusBadRequest, gin.H{"err": "unknown network"})
		return
	}

	var prop types.Proposal
	if err := m.db.FirstOrCreate(&prop, types.Proposal{
		NetworkID: netID,
		RefID:     refID,
	}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"err": err.Error()})
		return
	}

	msg := types.Message{
		ProposalID: prop.ID,
		Author:     c.GetString("addr"),
		Body:       req.Body,
		CreatedAt:  time.Now(),
	}
	if err := m.db.Create(&msg).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"err": err.Error()})
		return
	}

	for _, e := range req.Emails {
		_ = m.db.Create(&types.EmailSubscription{MessageID: msg.ID, Email: e}).Error
	}

	_ = data.PublishMessage(context.Background(), m.rdb, map[string]interface{}{
		"proposal": req.Proposal,
		"author":   msg.Author,
		"body":     msg.Body,
		"time":     msg.CreatedAt.Unix(),
	})

	c.JSON(http.StatusCreated, gin.H{"id": msg.ID})
}

func (m Messages) List(c *gin.Context) {
	net := c.Param("net")
	ref, _ := strconv.ParseUint(c.Param("id"), 10, 64)

	netID := uint8(1)
	if net == "kusama" {
		netID = 2
	}

	var prop types.Proposal
	if err := m.db.First(&prop, "network_id = ? AND ref_id = ?", netID, ref).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"err": "proposal not found"})
		return
	}

	var msgs []types.Message
	m.db.Where("proposal_id = ?", prop.ID).Order("created_at asc").Find(&msgs)
	c.JSON(http.StatusOK, msgs)
}
