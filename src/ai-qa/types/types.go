package types

import "time"

// QAHistory stores Q&A conversation history for referendums
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

func (QAHistory) TableName() string {
	return "qa_history"
}
