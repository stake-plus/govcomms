package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/stake-plus/polkadot-gov-comms/src/api/models"
)

type Msg struct{ DB *gorm.DB }

// POST /v1/messages
func (m Msg) Create(c *gin.Context) {
	var req struct {
		Proposal string `json:"proposalRef" binding:"required"` // "polkadot/582"
		Body     string `json:"body"        binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"err": "bad payload"})
		return
	}

	parts := strings.Split(req.Proposal, "/")
	if len(parts) != 2 {
		c.JSON(http.StatusBadRequest, gin.H{"err": "invalid ref"})
		return
	}
	ref, _ := strconv.ParseUint(parts[1], 10, 64)

	// stub proposal auto‑create; replace with real on‑chain fetch
	prop := models.Proposal{Network: parts[0], RefID: ref, Submitter: "TODO"}
	m.DB.FirstOrCreate(&prop, "network = ? AND ref_id = ?", prop.Network, prop.RefID)

	msg := models.Message{
		ProposalID: prop.ID,
		Author:     c.GetString("addr"),
		Body:       req.Body,
	}
	m.DB.Create(&msg)
	c.JSON(http.StatusCreated, gin.H{"id": msg.ID})
}

// GET /v1/messages/:net/:id
func (m Msg) List(c *gin.Context) {
	net := c.Param("net")
	ref, _ := strconv.ParseUint(c.Param("id"), 10, 64)

	var prop models.Proposal
	if err := m.DB.First(&prop, "network = ? AND ref_id = ?", net, ref).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"err": "proposal not found"})
		return
	}
	var msgs []models.Message
	m.DB.Where("proposal_id = ?", prop.ID).Order("created_at asc").Find(&msgs)
	c.JSON(http.StatusOK, msgs)
}
