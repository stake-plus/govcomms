package data

import (
	"fmt"
	"time"

	sharedgov "github.com/stake-plus/govcomms/src/shared/gov"
	"gorm.io/gorm"
)

// SaveFeedbackMessage persists a feedback message for a referendum.
func SaveFeedbackMessage(db *gorm.DB, ref *sharedgov.Ref, author, body string) (*sharedgov.RefMessage, error) {
	if ref == nil {
		return nil, fmt.Errorf("nil referendum provided")
	}

	message := sharedgov.RefMessage{
		RefID:     ref.ID,
		Author:    author,
		Body:      body,
		CreatedAt: time.Now(),
	}

	if err := db.Create(&message).Error; err != nil {
		return nil, err
	}

	return &message, nil
}

// UpdateFeedbackMessagePolkassembly updates a feedback message with Polkassembly information
func UpdateFeedbackMessagePolkassembly(db *gorm.DB, messageID uint64, commentID string, userID *uint32, username string) error {
	updates := map[string]interface{}{
		"polkassembly_comment_id": commentID,
	}
	if userID != nil {
		updates["polkassembly_user_id"] = *userID
	}
	if username != "" {
		updates["polkassembly_username"] = username
	}

	return db.Model(&sharedgov.RefMessage{}).
		Where("id = ?", messageID).
		Updates(updates).Error
}

// CountFeedbackMessages returns how many messages exist for a referendum.
func CountFeedbackMessages(db *gorm.DB, refDBID uint64) (int64, error) {
	var count int64
	if err := db.Model(&sharedgov.RefMessage{}).Where("ref_id = ?", refDBID).Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}
