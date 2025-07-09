package webserver

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/stake-plus/polkadot-gov-comms/src/api/types"
)

type Votes struct{ db *gorm.DB }

func NewVotes(db *gorm.DB) Votes { return Votes{db: db} }

func (v Votes) Cast(c *gin.Context) {
	var req struct {
		Proposal string `json:"proposalRef" binding:"required"`
		Choice   string `json:"choice"      binding:"required,oneof=aye nay abstain"`
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
	ref, _ := strconv.ParseUint(parts[1], 10, 64)

	netID := uint8(1)
	if parts[0] == "kusama" {
		netID = 2
	}

	var prop types.Proposal
	if err := v.db.First(&prop, "network_id = ? AND ref_id = ?", netID, ref).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"err": "proposal not found"})
		return
	}

	v.db.Where("proposal_id = ? AND voter_addr = ?", prop.ID, c.GetString("addr")).Delete(&types.Vote{})
	vote := types.Vote{
		ProposalID: prop.ID,
		VoterAddr:  c.GetString("addr"),
		Choice:     req.Choice,
	}
	if err := v.db.Create(&vote).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"err": err.Error()})
		return
	}
	c.Status(http.StatusCreated)
}

func (v Votes) Summary(c *gin.Context) {
	net := c.Param("net")
	ref, _ := strconv.ParseUint(c.Param("id"), 10, 64)

	netID := uint8(1)
	if net == "kusama" {
		netID = 2
	}

	var prop types.Proposal
	if err := v.db.First(&prop, "network_id = ? AND ref_id = ?", netID, ref).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"err": "proposal not found"})
		return
	}

	type agg struct {
		Choice string
		Count  int
	}
	var rows []agg
	v.db.Table("votes").Select("choice, count(*) as count").Where("proposal_id = ?", prop.ID).Group("choice").Scan(&rows)
	out := map[string]int{"aye": 0, "nay": 0, "abstain": 0}
	for _, r := range rows {
		out[r.Choice] = r.Count
	}
	c.JSON(http.StatusOK, out)
}
