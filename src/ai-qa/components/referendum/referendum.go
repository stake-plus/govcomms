package referendum

import (
	"fmt"
	"regexp"
	"strconv"

	"github.com/stake-plus/govcomms/src/ai-qa/types"
	"gorm.io/gorm"
)

type ThreadInfo struct {
	ThreadID  string
	RefID     uint64
	RefDBID   uint64
	NetworkID uint8
}

type Manager struct {
	db *gorm.DB
}

func NewManager(db *gorm.DB) *Manager {
	return &Manager{db: db}
}

func (m *Manager) FindThread(threadID string) (*ThreadInfo, error) {
	var refThread types.RefThread
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

func (m *Manager) GetThreadInfo(networkID uint8, refID uint32) (*ThreadInfo, error) {
	var refThread types.RefThread
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
