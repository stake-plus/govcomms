package cache

import (
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// QAHistory stores question/answer context for referendums.
type QAHistory struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement"`
	NetworkID uint8     `gorm:"index:idx_qa_ref"`
	RefID     uint32    `gorm:"index:idx_qa_ref"`
	ThreadID  string    `gorm:"index"`
	UserID    string    `gorm:"size:64"`
	Question  string    `gorm:"type:text"`
	Answer    string    `gorm:"type:text"`
	CreatedAt time.Time `gorm:"index"`
}

// TableName implements gorm's tabler interface.
func (QAHistory) TableName() string {
	return "qa_history"
}

// ContextStore provides persistence helpers for referendum Q&A content.
type ContextStore struct {
	db *gorm.DB
}

// NewContextStore returns a new context store instance.
func NewContextStore(db *gorm.DB) *ContextStore {
	return &ContextStore{db: db}
}

// SaveQA persists a new Q&A record.
func (cs *ContextStore) SaveQA(networkID uint8, refID uint32, threadID, userID, question, answer string) error {
	if cs == nil || cs.db == nil {
		return fmt.Errorf("context store not initialized")
	}

	qa := QAHistory{
		NetworkID: networkID,
		RefID:     refID,
		ThreadID:  threadID,
		UserID:    userID,
		Question:  question,
		Answer:    answer,
		CreatedAt: time.Now(),
	}

	return cs.db.Create(&qa).Error
}

// GetRecentQAs returns the most recent QAs up to limit, ordered oldest->newest.
func (cs *ContextStore) GetRecentQAs(networkID uint8, refID uint32, limit int) ([]QAHistory, error) {
	if cs == nil || cs.db == nil {
		return nil, fmt.Errorf("context store not initialized")
	}

	var qas []QAHistory

	err := cs.db.Where("network_id = ? AND ref_id = ?", networkID, refID).
		Order("created_at DESC").
		Limit(limit).
		Find(&qas).Error
	if err != nil {
		return nil, err
	}

	for i, j := 0, len(qas)-1; i < j; i, j = i+1, j-1 {
		qas[i], qas[j] = qas[j], qas[i]
	}

	return qas, nil
}

// BuildContext builds a formatted context block from recent QAs.
func (cs *ContextStore) BuildContext(networkID uint8, refID uint32) (string, error) {
	qas, err := cs.GetRecentQAs(networkID, refID, 10)
	if err != nil {
		return "", err
	}

	if len(qas) == 0 {
		return "", nil
	}

	var builder strings.Builder
	builder.WriteString("\n\n## Previous Q&A in this thread:\n")

	for i, qa := range qas {
		builder.WriteString(fmt.Sprintf("\nQ%d: %s\nA%d: %s\n", i+1, qa.Question, i+1, qa.Answer))
		if builder.Len() > 2000 {
			builder.WriteString("\n[Earlier context truncated]")
			break
		}
	}

	return builder.String(), nil
}
