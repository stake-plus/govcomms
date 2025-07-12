package webserver

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/stake-plus/polkadot-gov-comms/src/api/types"
	"gorm.io/gorm"
)

type Votes struct{ db *gorm.DB }

func NewVotes(db *gorm.DB) Votes { return Votes{db: db} }

func (v Votes) Cast(c *gin.Context) {
	var req struct {
		Proposal string `json:"proposalRef" binding:"required"`
		Choice   string `json:"choice" binding:"required,oneof=aye nay abstain"`
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

	refNum, _ := strconv.ParseUint(parts[1], 10, 64)
	netID := uint8(1)
	if parts[0] == "kusama" {
		netID = 2
	}

	var ref types.Ref
	if err := v.db.First(&ref, "network_id = ? AND ref_id = ?", netID, refNum).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"err": "proposal not found"})
		return
	}

	// Convert choice to int
	choiceMap := map[string]int16{"aye": 1, "nay": 0, "abstain": 2}
	choiceValue := choiceMap[req.Choice]

	v.db.Where("ref_id = ? AND dao_member_id = ?", ref.ID, c.GetString("addr")).Delete(&types.DaoVote{})

	vote := types.DaoVote{
		RefID:       ref.ID,
		DaoMemberID: c.GetString("addr"),
		Choice:      choiceValue,
	}
	if err := v.db.Create(&vote).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"err": err.Error()})
		return
	}

	c.Status(http.StatusCreated)
}

func (v Votes) Summary(c *gin.Context) {
	net := c.Param("net")
	refNum, _ := strconv.ParseUint(c.Param("id"), 10, 64)

	netID := uint8(1)
	if net == "kusama" {
		netID = 2
	}

	var ref types.Ref
	if err := v.db.First(&ref, "network_id = ? AND ref_id = ?", netID, refNum).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"err": "proposal not found"})
		return
	}

	type agg struct {
		Choice int16
		Count  int
	}
	var rows []agg
	v.db.Table("dao_votes").Select("choice, count(*) as count").Where("ref_id = ?", ref.ID).Group("choice").Scan(&rows)

	out := map[string]int{"aye": 0, "nay": 0, "abstain": 0}
	for _, r := range rows {
		switch r.Choice {
		case 1:
			out["aye"] = r.Count
		case 0:
			out["nay"] = r.Count
		case 2:
			out["abstain"] = r.Count
		}
	}

	c.JSON(http.StatusOK, out)
}
