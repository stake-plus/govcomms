// internal/models/models.go
package models

import "time"

type Proposal struct {
	ID        uint64 `gorm:"primaryKey"`
	Network   string `gorm:"size:16;not null"` // polkadot|kusama
	RefID     uint64 `gorm:"not null"`         // on‑chain referendum number
	Submitter string `gorm:"size:64;not null"`
	Title     string `gorm:"size:255"`
	Status    string `gorm:"size:32"`
	CreatedAt time.Time
}

type Message struct {
	ID         uint64 `gorm:"primaryKey"`
	ProposalID uint64 `gorm:"index;not null"`
	Author     string `gorm:"size:64;not null"`
	Body       string `gorm:"type:text;not null"`
	Internal   bool
	CreatedAt  time.Time
}
