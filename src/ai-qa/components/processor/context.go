package processor

import (
	"fmt"
	"strings"
	"time"

	"github.com/stake-plus/govcomms/src/ai-qa/types"
	"gorm.io/gorm"
)

type ContextManager struct {
	db *gorm.DB
}

func NewContextManager(db *gorm.DB) *ContextManager {
	return &ContextManager{db: db}
}

func (cm *ContextManager) SaveQA(networkID uint8, refID uint32, threadID string, userID string, question string, answer string) error {
	qa := types.QAHistory{
		NetworkID: networkID,
		RefID:     refID,
		ThreadID:  threadID,
		UserID:    userID,
		Question:  question,
		Answer:    answer,
		CreatedAt: time.Now(),
	}

	return cm.db.Create(&qa).Error
}

func (cm *ContextManager) GetRecentQAs(networkID uint8, refID uint32, limit int) ([]types.QAHistory, error) {
	var qas []types.QAHistory

	err := cm.db.Where("network_id = ? AND ref_id = ?", networkID, refID).
		Order("created_at DESC").
		Limit(limit).
		Find(&qas).Error

	if err != nil {
		return nil, err
	}

	// Reverse to get chronological order
	for i, j := 0, len(qas)-1; i < j; i, j = i+1, j-1 {
		qas[i], qas[j] = qas[j], qas[i]
	}

	return qas, nil
}

func (cm *ContextManager) BuildContext(networkID uint8, refID uint32) (string, error) {
	qas, err := cm.GetRecentQAs(networkID, refID, 10)
	if err != nil {
		return "", err
	}

	if len(qas) == 0 {
		return "", nil
	}

	var context strings.Builder
	context.WriteString("\n\n## Previous Q&A in this thread:\n")

	for i, qa := range qas {
		context.WriteString(fmt.Sprintf("\nQ%d: %s\nA%d: %s\n", i+1, qa.Question, i+1, qa.Answer))
		if len(context.String()) > 2000 {
			context.WriteString("\n[Earlier context truncated]")
			break
		}
	}

	return context.String(), nil
}
