package referendum

import (
	"github.com/stake-plus/govcomms/src/feedback/types"
	"gorm.io/gorm"
)

// GetThreadByRef retrieves thread info for a specific referendum
func GetThreadByRef(db *gorm.DB, networkID uint8, refID uint32) (*ThreadInfo, error) {
	var thread types.RefThread
	err := db.Where("network_id = ? AND ref_id = ?", networkID, refID).First(&thread).Error
	if err != nil {
		return nil, err
	}

	return &ThreadInfo{
		ThreadID:  thread.ThreadID,
		RefID:     uint64(refID),
		RefDBID:   thread.RefDBID,
		NetworkID: networkID,
	}, nil
}
