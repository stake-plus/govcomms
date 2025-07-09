package types

import "time"

//
// ─── NETWORKS / RPC ENDPOINTS ────────────────────────────────────────────────
//

type Network struct {
	ID     uint8  `gorm:"primaryKey"`
	Name   string `gorm:"size:32;unique;not null"`
	Symbol string `gorm:"size:8;not null"`
	URL    string `gorm:"size:256;not null"`
	RPCs   []RPC  `gorm:"foreignKey:NetworkID"`
}

type RPC struct {
	ID        uint32 `gorm:"primaryKey"`
	NetworkID uint8
	URL       string `gorm:"size:256;not null"`
	Active    bool   `gorm:"default:true"`
}

//
// ─── GOVERNANCE ──────────────────────────────────────────────────────────────
//

type Proposal struct {
	ID        uint64 `gorm:"primaryKey"`
	NetworkID uint8  `gorm:"index;not null"`
	RefID     uint64 `gorm:"not null"`
	Submitter string `gorm:"size:64;not null"`
	Title     string `gorm:"size:255"`
	Status    string `gorm:"size:32"`
	EndBlock  uint64
	CreatedAt time.Time
}

type ProposalParticipant struct {
	ProposalID uint64 `gorm:"primaryKey"`
	Address    string `gorm:"primaryKey;size:64"`
}

type Message struct {
	ID         uint64 `gorm:"primaryKey"`
	ProposalID uint64 `gorm:"index;not null"`
	Author     string `gorm:"size:64;not null"`
	Body       string `gorm:"type:text;not null"`
	Internal   bool
	CreatedAt  time.Time
	Emails     []EmailSubscription `gorm:"foreignKey:MessageID"`
}

type DaoMember struct {
	Address string `gorm:"primaryKey;size:64"`
	Discord string `gorm:"size:64"`
}

type Vote struct {
	ID         uint64 `gorm:"primaryKey"`
	ProposalID uint64 `gorm:"index;not null"`
	VoterAddr  string `gorm:"size:64;not null"`
	Choice     string `gorm:"size:8;not null"` // aye|nay|abstain
	Conviction int16
}

type EmailSubscription struct {
	ID        uint64 `gorm:"primaryKey"`
	MessageID uint64
	Email     string `gorm:"size:256;not null"`
	SentAt    *time.Time
}
