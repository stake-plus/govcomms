package referendum

import (
	"fmt"
	"strconv"
	"strings"

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
	parts := strings.SplitN(title, ":", 2)
	if len(parts) == 0 {
		return 0, fmt.Errorf("no referendum number found")
	}

	refNumStr := strings.TrimSpace(parts[0])
	refNum, err := strconv.ParseUint(refNumStr, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid referendum number: %s", refNumStr)
	}

	return uint32(refNum), nil
}
