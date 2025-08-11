package polkassembly

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/stake-plus/govcomms/src/bot/types"
	"gorm.io/gorm"
)

// RecoverMissingCommentIDs tries to find comment IDs for refs that posted but timed out
func (s *Service) RecoverMissingCommentIDs(db *gorm.DB) {
	// Find refs with messages but no comment ID
	var refs []types.Ref
	err := db.Where("polkassembly_comment_id IS NULL OR polkassembly_comment_id = 0").
		Where("EXISTS (SELECT 1 FROM ref_messages WHERE ref_id = refs.id AND internal = 1)").
		Find(&refs).Error

	if err != nil {
		s.logger.Printf("Failed to find refs needing recovery: %v", err)
		return
	}

	for _, ref := range refs {
		// Get network
		var network types.Network
		if err := db.First(&network, ref.NetworkID).Error; err != nil {
			continue
		}

		networkName := "polkadot"
		if network.ID == 2 {
			networkName = "kusama"
		}

		// Try to find our comment
		commentID := s.findOurComment(networkName, int(ref.RefID))
		if commentID > 0 {
			// Update the database
			if err := db.Model(&ref).Update("polkassembly_comment_id", commentID).Error; err != nil {
				s.logger.Printf("Failed to update comment ID for ref %d: %v", ref.RefID, err)
			} else {
				s.logger.Printf("Recovered comment ID %d for %s ref %d", commentID, networkName, ref.RefID)
			}
		}

		time.Sleep(2 * time.Second) // Rate limit
	}
}

func (s *Service) findOurComment(network string, refID int) int {
	url := fmt.Sprintf("https://%s.polkassembly.io/api/v1/posts/on-chain-post/%d/comments", network, refID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0
	}

	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0
	}

	// Check if it's JSON
	if strings.HasPrefix(strings.TrimSpace(string(body)), "<") {
		return 0 // HTML response
	}

	var comments []struct {
		ID       int    `json:"id"`
		Content  string `json:"content"`
		Username string `json:"username"`
	}

	if err := json.Unmarshal(body, &comments); err != nil {
		return 0
	}

	// Look for our comment (contains our intro text)
	for _, comment := range comments {
		if strings.Contains(comment.Content, "REEEEEEEEEE DAO") {
			return comment.ID
		}
	}

	return 0
}
