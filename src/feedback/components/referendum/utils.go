package referendum

import (
	sharedgov "github.com/stake-plus/govcomms/src/shared/gov"
	"gorm.io/gorm"
)

// GetThreadByRef retrieves thread info for a specific referendum
func GetThreadByRef(db *gorm.DB, networkID uint8, refID uint32) (*sharedgov.ThreadInfo, error) {
	manager := sharedgov.NewReferendumManager(db)
	return manager.GetThreadInfo(networkID, refID)
}
