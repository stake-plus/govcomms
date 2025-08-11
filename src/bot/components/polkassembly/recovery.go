package polkassembly

import (
	"fmt"
	"log"

	"github.com/stake-plus/govcomms/src/bot/types"
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

	if ref.PolkassemblyCommentID != nil && *ref.PolkassemblyCommentID != "" {
		s.logger.Printf("Referendum already has comment ID: %s", *ref.PolkassemblyCommentID)
		return nil
	}

	commentID, err := s.findOurComment(network, refID)
	if err != nil {
		return fmt.Errorf("failed to find comment: %w", err)
	}

	if commentID == "" {
		return fmt.Errorf("no comment found for referendum")
	}

	err = s.db.Model(&ref).Update("polkassembly_comment_id", commentID).Error
	if err != nil {
		return fmt.Errorf("failed to update comment ID: %w", err)
	}

	s.logger.Printf("Recovered comment ID %s for %s referendum #%d", commentID, network, refID)
	return nil
}

func (s *Service) RecoverAllCommentIDs() {
	var refs []types.Ref
	s.db.Where("polkassembly_comment_id IS NULL OR polkassembly_comment_id = ''").Find(&refs)

	for _, ref := range refs {
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
