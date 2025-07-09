package types

import "time"

type Network struct {
	ID   uint8  `gorm:"primaryKey"`
	Name string `gorm:"unique;size:32"`
	RPCs []RPC
}

type RPC struct {
	ID        uint32 `gorm:"primaryKey"`
	NetworkID uint8
	URL       string `gorm:"size:256"`
	Active    bool   `gorm:"default:true"`
}

type Proposal struct {
	ID        uint64 `gorm:"primaryKey"`
	NetworkID uint8  `gorm:"index"`
	RefID     uint64 `gorm:"index"`
	Submitter string `gorm:"size:64"`
	Title     string `gorm:"size:255"`
	Status    string `gorm:"size:32"`
	CreatedAt time.Time
}

type Message struct {
	ID         uint64 `gorm:"primaryKey"`
	ProposalID uint64 `gorm:"index"`
	Author     string `gorm:"size:64"`
	Body       string `gorm:"type:text"`
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
	ProposalID uint64 `gorm:"index"`
	VoterAddr  string `gorm:"size:64"`
	Choice     string `gorm:"size:8"`
	Conviction int16
}

type EmailSubscription struct {
	ID        uint64 `gorm:"primaryKey"`
	MessageID uint64 `gorm:"index"`
	Email     string `gorm:"size:256"`
	SentAt    *time.Time
}
