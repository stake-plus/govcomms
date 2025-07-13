package webserver

import (
	"context"
	"fmt"
	"html"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/microcosm-cc/bluemonday"
	"github.com/redis/go-redis/v9"
	"github.com/stake-plus/govcomms/src/GCApi/data"
	"github.com/stake-plus/govcomms/src/GCApi/polkassembly"
	"github.com/stake-plus/govcomms/src/GCApi/types"
	"gorm.io/gorm"
)

type Messages struct {
	db        *gorm.DB
	rdb       *redis.Client
	pa        *polkassembly.Client
	sanitizer *bluemonday.Policy
}

func NewMessages(db *gorm.DB, rdb *redis.Client) Messages {
	var paClient *polkassembly.Client
	if apiKey := os.Getenv("POLKASSEMBLY_API_KEY"); apiKey != "" {
		baseURL := data.GetSetting("polkassembly_api")
		paClient = polkassembly.NewClient(apiKey, baseURL)
	}

	// Create a strict sanitizer for markdown content
	sanitizer := bluemonday.StrictPolicy()
	// Allow basic markdown formatting
	sanitizer.AllowElements("p", "br", "strong", "em", "code", "pre", "blockquote")
	sanitizer.AllowElements("ul", "ol", "li")
	sanitizer.AllowElements("h1", "h2", "h3", "h4", "h5", "h6")
	sanitizer.AllowAttrs("href").OnElements("a")
	sanitizer.RequireParseableURLs(true)
	sanitizer.AddTargetBlankToFullyQualifiedLinks(true)
	sanitizer.RequireNoFollowOnLinks(true)

	return Messages{db: db, rdb: rdb, pa: paClient, sanitizer: sanitizer}
}

func (m Messages) Create(c *gin.Context) {
	var req struct {
		Proposal string   `json:"proposalRef" binding:"required"`
		Body     string   `json:"body" binding:"required,min=1,max=10000"`
		Emails   []string `json:"emails" binding:"max=10"`
		Title    string   `json:"title" binding:"max=255"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"err": err.Error()})
		return
	}

	// Validate and sanitize input
	if !isValidProposalRef(req.Proposal) {
		c.JSON(http.StatusBadRequest, gin.H{"err": "invalid proposal reference format"})
		return
	}

	// Sanitize HTML/markdown content
	req.Body = m.sanitizer.Sanitize(req.Body)
	req.Title = html.EscapeString(req.Title)

	// Validate UTF-8
	if !utf8.ValidString(req.Body) || !utf8.ValidString(req.Title) {
		c.JSON(http.StatusBadRequest, gin.H{"err": "invalid characters in input"})
		return
	}

	// Validate body length after sanitization
	if len(req.Body) < 1 || len(req.Body) > 10000 {
		c.JSON(http.StatusBadRequest, gin.H{"err": "body must be between 1 and 10000 characters"})
		return
	}

	// Validate emails
	for _, email := range req.Emails {
		if !isValidEmail(email) {
			c.JSON(http.StatusBadRequest, gin.H{"err": "invalid email format: " + email})
			return
		}
	}

	parts := strings.Split(req.Proposal, "/")
	if len(parts) != 2 {
		c.JSON(http.StatusBadRequest, gin.H{"err": "bad proposalRef"})
		return
	}

	// Validate network
	network := strings.ToLower(parts[0])
	if network != "polkadot" && network != "kusama" {
		c.JSON(http.StatusBadRequest, gin.H{"err": "invalid network"})
		return
	}

	// Validate referendum ID
	refID, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil || refID == 0 || refID > 1000000 { // reasonable upper limit
		c.JSON(http.StatusBadRequest, gin.H{"err": "invalid referendum ID"})
		return
	}

	netID := uint8(1)
	if network == "kusama" {
		netID = 2
	}

	// Get user address from JWT
	userAddr := c.GetString("addr")

	// Check if referendum exists
	var ref types.Ref
	err = m.db.First(&ref, "network_id = ? AND ref_id = ?", netID, refID).Error
	if err == gorm.ErrRecordNotFound {
		// Check if user is allowed to create referendum
		// Only allow if they are a DAO member or have a valid reason
		var daoMember types.DaoMember
		if err := m.db.First(&daoMember, "address = ?", userAddr).Error; err != nil {
			c.JSON(http.StatusForbidden, gin.H{"err": "only DAO members can create new referendum entries"})
			return
		}

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

	// Validate network parameter
	if net != "polkadot" && net != "kusama" {
		c.JSON(http.StatusBadRequest, gin.H{"err": "invalid network"})
		return
	}

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

func isValidEmail(email string) bool {
	// More robust email validation
	emailRegex := regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)
	return emailRegex.MatchString(email) && len(email) <= 255
}

func isValidProposalRef(ref string) bool {
	// Validate proposal reference format
	proposalRegex := regexp.MustCompile(`^(polkadot|kusama)/\d+$`)
	return proposalRegex.MatchString(strings.ToLower(ref))
}
