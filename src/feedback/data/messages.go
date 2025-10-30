package data

import (
	"fmt"
	"time"

	sharedgov "github.com/stake-plus/govcomms/src/shared/gov"
	"gorm.io/gorm"
)

// SaveFeedbackMessage persists a feedback message for a referendum.
func SaveFeedbackMessage(db *gorm.DB, ref *sharedgov.Ref, author, body string) error {
	if ref == nil {
		return fmt.Errorf("nil referendum provided")
	}

	message := sharedgov.RefMessage{
		RefID:     ref.ID,
		Author:    author,
		Body:      body,
		CreatedAt: time.Now(),
	}

	return db.Create(&message).Error
}
