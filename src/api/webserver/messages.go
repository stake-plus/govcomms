package webserver

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stake-plus/polkadot-gov-comms/src/api/data"
	"github.com/stake-plus/polkadot-gov-comms/src/api/polkassembly"
	"github.com/stake-plus/polkadot-gov-comms/src/api/types"
	"gorm.io/gorm"
)

type Messages struct {
	db  *gorm.DB
	rdb *redis.Client
	pa  *polkassembly.Client
}

func NewMessages(db *gorm.DB, rdb *redis.Client) Messages {
	var paClient *polkassembly.Client
	if apiKey := os.Getenv("POLKASSEMBLY_API_KEY"); apiKey != "" {
		paClient = polkassembly.NewClient(apiKey)
	}
	return Messages{db: db, rdb: rdb, pa: paClient}
}

func (m Messages) Create(c *gin.Context) {
	var req struct {
		Proposal string   `json:"proposalRef" binding:"required"`
		Body     string   `json:"body" binding:"required"`
		Emails   []string `json:"emails"`
		Title    string   `json:"title"`
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
	netID := uint8(1)
	network := "polkadot"
	if parts[0] == "kusama" {
		netID = 2
		network = "kusama"
	}

	// ensure ref exists
	var ref types.Ref
	err := m.db.First(&ref, "network_id = ? AND ref_id = ?", netID, refID).Error
	if err != nil && err == gorm.ErrRecordNotFound {
		ref = types.Ref{
			NetworkID: netID,
			RefID:     refID,
			Submitter: c.GetString("addr"),
			Status:    "Unknown",
			Title:     req.Title,
		}
		if err = m.db.Create(&ref).Error; err == nil {
			_ = m.db.FirstOrCreate(&types.DaoMember{Address: ref.Submitter}).Error
			_ = m.db.FirstOrCreate(&types.RefProponent{
				RefID: ref.ID, Address: ref.Submitter, Role: "submitter", Active: 1,
			}).Error
		}
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"err": err.Error()})
		return
	}

	// check authorization
	var auth types.RefProponent
	if err := m.db.First(&auth,
		"ref_id = ? AND address = ?", ref.ID, c.GetString("addr")).Error; err != nil {
		c.JSON(http.StatusForbidden, gin.H{"err": "not authorised for this proposal"})
		return
	}

	// store message
	msg := types.RefMessage{
		RefID:     ref.ID,
		Author:    c.GetString("addr"),
		Body:      req.Body,
		CreatedAt: time.Now(),
	}
	if err := m.db.Create(&msg).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"err": err.Error()})
		return
	}

	for _, e := range req.Emails {
		_ = m.db.Create(&types.RefSub{MessageID: msg.ID, Email: e}).Error
	}

	// Check if this is the first message for this proposal
	var msgCount int64
	m.db.Model(&types.RefMessage{}).Where("ref_id = ?", ref.ID).Count(&msgCount)

	// If first message and we have Polkassembly client, post it
	if msgCount == 1 && m.pa != nil {
		frontendURL := data.GetSetting("gc_url")
		if frontendURL == "" {
			frontendURL = "https://gc.reeeeeeeeee.io"
		}
		link := fmt.Sprintf("%s/%s/%d", frontendURL, network, refID)
		content := fmt.Sprintf("%s\n\n[Continue discussion](%s)", msg.Body, link)

		go func() {
			if _, err := m.pa.PostComment(network, int(refID), content); err != nil {
				log.Printf("Failed to post to Polkassembly: %v", err)
			} else {
				log.Printf("Posted first message to Polkassembly for %s/%d", network, refID)
			}
		}()
	}

	// Publish to Redis for Discord bot
	_ = data.PublishMessage(context.Background(), m.rdb, map[string]any{
		"proposal": req.Proposal,
		"author":   msg.Author,
		"body":     msg.Body,
		"time":     msg.CreatedAt.Unix(),
		"id":       msg.ID,
		"network":  network,
		"ref_id":   refID,
	})

	c.JSON(http.StatusCreated, gin.H{"id": msg.ID})
}

func (m Messages) List(c *gin.Context) {
	net := c.Param("net")
	refNum, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	netID := uint8(1)
	if net == "kusama" {
		netID = 2
	}

	var ref types.Ref
	if err := m.db.First(&ref, "network_id = ? AND ref_id = ?", netID, refNum).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"err": "proposal not found"})
		return
	}

	var msgs []types.RefMessage
	m.db.Where("ref_id = ?", ref.ID).Order("created_at asc").Find(&msgs)

	// Add proposal info to response
	response := gin.H{
		"proposal": gin.H{
			"id":        ref.ID,
			"network":   net,
			"ref_id":    ref.RefID,
			"title":     ref.Title,
			"submitter": ref.Submitter,
			"status":    ref.Status,
			"track_id":  ref.TrackID,
		},
		"messages": msgs,
	}

	c.JSON(http.StatusOK, response)
}
