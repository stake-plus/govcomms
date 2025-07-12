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
		baseURL := data.GetSetting("polkassembly_api")
		paClient = polkassembly.NewClient(apiKey, baseURL)
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

	// Get user address from JWT
	userAddr := c.GetString("addr")

	// Check if referendum exists
	var ref types.Ref
	err := m.db.First(&ref, "network_id = ? AND ref_id = ?", netID, refID).Error

	if err == gorm.ErrRecordNotFound {
		// Only allow creating new referendum if user is submitting it
		ref = types.Ref{
			NetworkID: netID,
			RefID:     refID,
			Submitter: userAddr,
			Status:    "Unknown",
			Title:     req.Title,
		}
		if err = m.db.Create(&ref).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"err": err.Error()})
			return
		}

		// Create submitter as proponent
		_ = m.db.Create(&types.RefProponent{
			RefID:   ref.ID,
			Address: userAddr,
			Role:    "submitter",
			Active:  1,
		}).Error
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"err": err.Error()})
		return
	} else {
		// Referendum exists - check authorization
		var auth types.RefProponent
		if err := m.db.First(&auth, "ref_id = ? AND address = ? AND active = ?", ref.ID, userAddr, 1).Error; err != nil {
			// Not a proponent - check if DAO member
			var daoMember types.DaoMember
			if err := m.db.First(&daoMember, "address = ?", userAddr).Error; err != nil {
				c.JSON(http.StatusForbidden, gin.H{"err": "not authorized for this proposal"})
				return
			}

			// DAO member but not a proponent - add them as a dao_member proponent
			_ = m.db.Create(&types.RefProponent{
				RefID:   ref.ID,
				Address: userAddr,
				Role:    "dao_member",
				Active:  1,
			}).Error
		}
	}

	// Store message
	msg := types.RefMessage{
		RefID:     ref.ID,
		Author:    userAddr,
		Body:      req.Body,
		CreatedAt: time.Now(),
		Internal:  false, // Messages from API are external
	}
	if err := m.db.Create(&msg).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"err": err.Error()})
		return
	}

	// Store email subscriptions
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
			frontendURL = "http://localhost:3000" // development default
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

	// Check if user is authorized for this referendum
	userAddr := c.GetString("addr")
	var auth types.RefProponent
	if err := m.db.First(&auth, "ref_id = ? AND address = ?", ref.ID, userAddr).Error; err != nil {
		// Check if user is a DAO member
		var daoMember types.DaoMember
		if err := m.db.First(&daoMember, "address = ?", userAddr).Error; err != nil {
			c.JSON(http.StatusForbidden, gin.H{"err": "not authorized to view this proposal"})
			return
		}
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
