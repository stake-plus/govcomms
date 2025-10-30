package gov

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ThreadInfo contains referendum thread information
type ThreadInfo struct {
	ThreadID  string
	RefID     uint64
	RefDBID   uint64
	NetworkID uint8
}

// Manager manages referendum thread lookups
type ReferendumManager struct {
	db *gorm.DB
}

// NewReferendumManager creates a new referendum manager
func NewReferendumManager(db *gorm.DB) *ReferendumManager {
	return &ReferendumManager{db: db}
}

// FindThread finds thread info by Discord thread ID
func (m *ReferendumManager) FindThread(threadID string) (*ThreadInfo, error) {
	var refThread RefThread
	err := m.db.Where("thread_id = ?", threadID).First(&refThread).Error
	if err != nil {
		return nil, err
	}

	return &ThreadInfo{
		ThreadID:  refThread.ThreadID,
		RefID:     refThread.RefID,
		RefDBID:   refThread.RefDBID,
		NetworkID: refThread.NetworkID,
	}, nil
}

// GetThreadInfo gets thread info by network ID and ref ID
func (m *ReferendumManager) GetThreadInfo(networkID uint8, refID uint32) (*ThreadInfo, error) {
	var refThread RefThread
	err := m.db.Where("network_id = ? AND ref_id = ?", networkID, refID).First(&refThread).Error
	if err != nil {
		return nil, err
	}

	return &ThreadInfo{
		ThreadID:  refThread.ThreadID,
		RefID:     refThread.RefID,
		RefDBID:   refThread.RefDBID,
		NetworkID: refThread.NetworkID,
	}, nil
}

// ParseRefIDFromTitle extracts referendum number from a thread title
func ParseRefIDFromTitle(title string) (uint32, error) {
	// Extract referendum number from title using regex to handle special characters
	// Look for a number at the beginning, possibly with quotes or other characters
	re := regexp.MustCompile(`^\s*["']?(\d+)\s*["']?\s*:`)
	matches := re.FindStringSubmatch(title)

	if len(matches) < 2 {
		// Fallback: try to find any number followed by colon
		re = regexp.MustCompile(`(\d+)\s*:`)
		matches = re.FindStringSubmatch(title)

		if len(matches) < 2 {
			// Last resort: find first number in the title
			re = regexp.MustCompile(`(\d+)`)
			matches = re.FindStringSubmatch(title)

			if len(matches) < 2 {
				return 0, fmt.Errorf("no referendum number found")
			}
		}
	}

	refNumStr := matches[1]
	refNum, err := strconv.ParseUint(refNumStr, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid referendum number: %s", refNumStr)
	}

	return uint32(refNum), nil
}

// UpsertThreadMapping links a Discord thread to a referendum record.
func (m *ReferendumManager) UpsertThreadMapping(networkID uint8, refID uint32, threadID string) error {
	if threadID == "" {
		return fmt.Errorf("threadID cannot be empty")
	}

	var ref Ref
	if err := m.db.Where("network_id = ? AND ref_id = ?", networkID, refID).First(&ref).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			ref = Ref{
				NetworkID: networkID,
				RefID:     uint64(refID),
			}
			if createErr := m.db.Create(&ref).Error; createErr != nil {
				return createErr
			}
		} else {
			return err
		}
	}

	thread := RefThread{
		ThreadID:  threadID,
		RefDBID:   ref.ID,
		NetworkID: networkID,
		RefID:     ref.RefID,
		UpdatedAt: time.Now(),
	}

	return m.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "thread_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"ref_db_id", "network_id", "ref_id", "updated_at"}),
	}).Create(&thread).Error
}
