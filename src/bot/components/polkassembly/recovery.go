package polkassembly

import (
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
