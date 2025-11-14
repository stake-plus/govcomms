package data

import (
	"fmt"
	"time"

	sharedgov "github.com/stake-plus/govcomms/src/polkadot-go/governance"
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

// GetPolkassemblyMessages returns all messages that have a Polkassembly comment ID.
func GetPolkassemblyMessages(db *gorm.DB, refDBID uint64) ([]sharedgov.RefMessage, error) {
	var messages []sharedgov.RefMessage
	if err := db.Where("ref_id = ? AND polkassembly_comment_id IS NOT NULL AND polkassembly_comment_id <> ''", refDBID).
		Find(&messages).Error; err != nil {
		return nil, err
	}
	return messages, nil
}

// SaveExternalPolkassemblyReply persists a reply that originated on Polkassembly.
func SaveExternalPolkassemblyReply(db *gorm.DB, refDBID uint64, author, body string, userID *int, username string, commentID string, createdAt time.Time) (*sharedgov.RefMessage, error) {
	if commentID == "" {
		return nil, fmt.Errorf("comment ID cannot be empty")
	}

	msg := sharedgov.RefMessage{
		RefID:                refDBID,
		Author:               author,
		Body:                 body,
		Internal:             true,
		PolkassemblyUsername: username,
		CreatedAt:            createdAt,
	}

	if createdAt.IsZero() {
		msg.CreatedAt = time.Now()
	}

	if userID != nil {
		val := uint32(*userID)
		msg.PolkassemblyUserID = &val
	}

	msgID := commentID
	msg.PolkassemblyCommentID = &msgID

	if err := db.Create(&msg).Error; err != nil {
		return nil, err
	}

	return &msg, nil
}

// GetFirstFeedbackMessage returns the earliest feedback message for a referendum.
func GetFirstFeedbackMessage(db *gorm.DB, refDBID uint64) (*sharedgov.RefMessage, error) {
	var msg sharedgov.RefMessage
	if err := db.Where("ref_id = ?", refDBID).Order("created_at ASC").First(&msg).Error; err != nil {
		return nil, err
	}
	return &msg, nil
}

