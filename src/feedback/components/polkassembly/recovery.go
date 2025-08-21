package polkassembly

import (
	"fmt"
	"log"

	"github.com/stake-plus/govcomms/src/feedback/types"
	"gorm.io/gorm"
)

type RecoveryService struct {
	db      *gorm.DB
	service *Service
}

func NewRecoveryService(db *gorm.DB, service *Service) *RecoveryService {
	return &RecoveryService{
		db:      db,
		service: service,
	}
}

func (s *Service) RecoverCommentID(network string, refID int) error {
	var ref types.Ref
	err := s.db.Where("network_id = (SELECT id FROM networks WHERE name = ?) AND ref_id = ?", network, refID).First(&ref).Error
	if err != nil {
		return fmt.Errorf("failed to find referendum: %w", err)
	}

	// Check if we already have a comment ID in ref_messages
	var msg types.RefMessage
	err = s.db.Where("ref_id = ? AND internal = ? AND polkassembly_comment_id IS NOT NULL", ref.ID, true).First(&msg).Error
	if err == nil && msg.PolkassemblyCommentID != nil && *msg.PolkassemblyCommentID != "" {
		s.logger.Printf("Referendum already has comment ID in messages: %s", *msg.PolkassemblyCommentID)
		return nil
	}

	commentID, err := s.findOurComment(network, refID)
	if err != nil {
		return fmt.Errorf("failed to find comment: %w", err)
	}

	if commentID == "" {
		return fmt.Errorf("no comment found for referendum")
	}

	// Store comment ID in the first internal message for this ref
	err = s.db.Model(&types.RefMessage{}).
		Where("ref_id = ? AND internal = ?", ref.ID, true).
		Order("created_at ASC").
		Limit(1).
		Update("polkassembly_comment_id", commentID).Error
	if err != nil {
		return fmt.Errorf("failed to update comment ID: %w", err)
	}

	s.logger.Printf("Recovered comment ID %s for %s referendum #%d", commentID, network, refID)
	return nil
}

func (s *Service) RecoverAllCommentIDs() {
	// Find all refs where we have internal messages but no polkassembly comment ID
	var messages []types.RefMessage
	s.db.Where("internal = ? AND (polkassembly_comment_id IS NULL OR polkassembly_comment_id = '')", true).
		Group("ref_id").
		Find(&messages)

	for _, msg := range messages {
		var ref types.Ref
		if err := s.db.First(&ref, msg.RefID).Error; err != nil {
			log.Printf("Failed to get ref for message %d: %v", msg.ID, err)
			continue
		}

		var network types.Network
		if err := s.db.First(&network, ref.NetworkID).Error; err != nil {
			log.Printf("Failed to get network for ref %d: %v", ref.RefID, err)
			continue
		}

		if err := s.RecoverCommentID(network.Name, int(ref.RefID)); err != nil {
			log.Printf("Failed to recover comment ID for %s ref %d: %v", network.Name, ref.RefID, err)
		}
	}
}
