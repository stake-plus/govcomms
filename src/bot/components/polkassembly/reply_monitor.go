package polkassembly

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/stake-plus/govcomms/src/bot/types"
	"gorm.io/gorm"
)

type ReplyMonitor struct {
	db      *gorm.DB
	clients map[string]*Client
	logger  *log.Logger
}

func NewReplyMonitor(db *gorm.DB, clients map[string]*Client, logger *log.Logger) *ReplyMonitor {
	return &ReplyMonitor{
		db:      db,
		clients: clients,
		logger:  logger,
	}
}

func (rm *ReplyMonitor) Start(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			rm.logger.Println("Reply monitor stopping")
			return
		case <-ticker.C:
			rm.checkForReplies()
		}
	}
}

func (rm *ReplyMonitor) checkForReplies() {
	// Get all unfinalized referenda with Polkassembly comments
	var refs []types.Ref
	err := rm.db.Where("finalized = ? AND polkassembly_comment_id IS NOT NULL AND polkassembly_comment_id > 0", false).
		Where("last_reply_check IS NULL OR last_reply_check < ?", time.Now().Add(-5*time.Minute)).
		Find(&refs).Error
	if err != nil {
		rm.logger.Printf("Failed to get refs for reply check: %v", err)
		return
	}

	for _, ref := range refs {
		rm.checkReferendumReplies(ref)

		// Update last check time
		rm.db.Model(&ref).Update("last_reply_check", time.Now())

		// Rate limit
		time.Sleep(2 * time.Second)
	}
}

func (rm *ReplyMonitor) checkReferendumReplies(ref types.Ref) {
	// Get network
	var network types.Network
	if err := rm.db.First(&network, ref.NetworkID).Error; err != nil {
		rm.logger.Printf("Failed to get network for ref %d: %v", ref.RefID, err)
		return
	}

	networkName := "polkadot"
	if network.ID == 2 {
		networkName = "kusama"
	}

	// Get replies from Polkassembly
	replies, err := rm.getReplies(networkName, int(ref.RefID), int(ref.PolkassemblyCommentID))
	if err != nil {
		rm.logger.Printf("Failed to get replies for %s ref %d: %v", networkName, ref.RefID, err)
		return
	}

	// Check for new replies
	var existingReplies []types.RefMessage
	rm.db.Where("ref_id = ? AND polkassembly_user_id IS NOT NULL", ref.ID).Find(&existingReplies)

	existingMap := make(map[int]bool)
	for _, msg := range existingReplies {
		if msg.PolkassemblyUserID != nil {
			existingMap[int(*msg.PolkassemblyUserID)] = true
		}
	}

	// Store new replies
	for _, reply := range replies {
		if !existingMap[reply.UserID] {
			userID := uint32(reply.UserID)
			msg := types.RefMessage{
				RefID:                ref.ID,
				Author:               reply.Username,
				Body:                 reply.Content,
				Internal:             false,
				PolkassemblyUserID:   &userID,
				PolkassemblyUsername: reply.Username,
				CreatedAt:            reply.CreatedAt,
			}

			if err := rm.db.Create(&msg).Error; err != nil {
				rm.logger.Printf("Failed to save reply: %v", err)
			} else {
				rm.logger.Printf("New reply from %s on %s ref %d", reply.Username, networkName, ref.RefID)
			}
		}
	}
}

type PolkassemblyReply struct {
	ID        int       `json:"id"`
	UserID    int       `json:"user_id"`
	Username  string    `json:"username"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

func (rm *ReplyMonitor) getReplies(network string, refID int, commentID int) ([]PolkassemblyReply, error) {
	// Try the v1 API endpoint which might work better
	url := fmt.Sprintf("https://%s.polkassembly.io/api/v1/posts/on-chain-post/%d/comments", network, refID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	// Add headers to look like a proper API request
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// Check if response is HTML (error page)
	bodyStr := string(body)
	if strings.HasPrefix(strings.TrimSpace(bodyStr), "<") {
		// Log first 200 chars of HTML for debugging
		preview := bodyStr
		if len(preview) > 200 {
			preview = preview[:200]
		}
		rm.logger.Printf("Got HTML response instead of JSON from %s: %s...", url, preview)

		// Return empty array - no replies found
		return []PolkassemblyReply{}, nil
	}

	// Try to parse as JSON
	var response struct {
		Comments []struct {
			ID        int       `json:"id"`
			UserID    int       `json:"user_id"`
			Username  string    `json:"username"`
			Content   string    `json:"content"`
			CreatedAt time.Time `json:"created_at"`
			Replies   []struct {
				ID        int       `json:"id"`
				UserID    int       `json:"user_id"`
				Username  string    `json:"username"`
				Content   string    `json:"content"`
				CreatedAt time.Time `json:"created_at"`
			} `json:"replies"`
		} `json:"comments"`
	}

	if err := json.Unmarshal(body, &response); err != nil {
		// Try alternative response format
		var altResponse []struct {
			ID        int       `json:"id"`
			UserID    int       `json:"user_id"`
			Username  string    `json:"username"`
			Content   string    `json:"content"`
			CreatedAt time.Time `json:"created_at"`
		}

		if err2 := json.Unmarshal(body, &altResponse); err2 != nil {
			rm.logger.Printf("Failed to parse JSON response: %v", err)
			rm.logger.Printf("Response preview: %s", string(body)[:min(len(body), 500)])
			return []PolkassemblyReply{}, nil
		}

		// Convert alt response to replies
		var replies []PolkassemblyReply
		for _, comment := range altResponse {
			if comment.ID == commentID {
				continue // Skip our own comment
			}
			replies = append(replies, PolkassemblyReply{
				ID:        comment.ID,
				UserID:    comment.UserID,
				Username:  comment.Username,
				Content:   comment.Content,
				CreatedAt: comment.CreatedAt,
			})
		}
		return replies, nil
	}

	// Find our comment and get replies to it
	var replies []PolkassemblyReply
	for _, comment := range response.Comments {
		if comment.ID == commentID {
			// Get direct replies to our comment
			for _, reply := range comment.Replies {
				replies = append(replies, PolkassemblyReply{
					ID:        reply.ID,
					UserID:    reply.UserID,
					Username:  reply.Username,
					Content:   reply.Content,
					CreatedAt: reply.CreatedAt,
				})
			}
		}
	}

	return replies, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
