package types

import "time"

//
// ──── NETWORKS / RPC ENDPOINTS ──────────────────────────────────────────────
//

type Network struct {
	ID               uint8  `gorm:"primaryKey"`
	Name             string `gorm:"size:32;unique;not null"`
	Symbol           string `gorm:"size:8;not null"`
	URL              string `gorm:"size:256;not null"`
	DiscordChannelID string `gorm:"size:64"`
	RPCs             []RPC  `gorm:"foreignKey:NetworkID"`
}

type RPC struct {
	ID        uint32 `gorm:"primaryKey"`
	NetworkID uint8
	URL       string `gorm:"size:256;not null"`
	Active    bool   `gorm:"default:true"`
}

//
// ──── GOVERNANCE ─────────────────────────────────────────────────────────────
//

type Proposal struct {
	ID                      uint64    `gorm:"primaryKey"`
	NetworkID               uint8     `gorm:"index;not null"`
	RefID                   uint64    `gorm:"not null"`
	Submitter               string    `gorm:"size:128;not null"`
	Title                   string    `gorm:"size:255"`
	Status                  string    `gorm:"size:32"`
	TrackID                 uint16    `gorm:"index"`
	Origin                  string    `gorm:"size:64"`
	Enactment               string    `gorm:"size:32"`
	Submitted               uint64    // Block number when submitted
	SubmittedAt             time.Time // Timestamp when submitted
	DecisionStart           uint64    // Block number when decision started
	DecisionEnd             uint64    // Block number when decision ends
	ConfirmStart            uint64    // Block number when confirm started
	ConfirmEnd              uint64    // Block number when confirm ends
	Approved                bool
	TallyAyes               string `gorm:"size:64"` // On-chain tally
	TallyNays               string `gorm:"size:64"` // On-chain tally
	PreimageHash            string `gorm:"size:128"`
	PreimageLen             uint32
	DecisionDepositWho      string `gorm:"size:128"`
	DecisionDepositAmount   string `gorm:"size:64"`
	SubmissionDepositWho    string `gorm:"size:128"`
	SubmissionDepositAmount string `gorm:"size:64"`
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

type ProposalParticipant struct {
	ProposalID uint64 `gorm:"primaryKey"`
	Address    string `gorm:"primaryKey;size:128"`
	Role       string `gorm:"size:32"` // submitter, dao_member, feedback_provider
}

type Message struct {
	ID         uint64 `gorm:"primaryKey"`
	ProposalID uint64 `gorm:"index;not null"`
	Author     string `gorm:"size:128;not null"`
	Body       string `gorm:"type:text;not null"`
	Internal   bool
	CreatedAt  time.Time
	Emails     []EmailSubscription `gorm:"foreignKey:MessageID"`
}

type DaoMember struct {
	Address string `gorm:"primaryKey;size:128"`
	Discord string `gorm:"size:64"`
}

// Internal DAO vote tracking (not on-chain votes)
type Vote struct {
	ID         uint64 `gorm:"primaryKey"`
	ProposalID uint64 `gorm:"index;not null"`
	VoterAddr  string `gorm:"size:128;not null"`
	Choice     string `gorm:"size:8;not null"` // aye|nay|abstain
	Conviction int16
	Balance    string `gorm:"size:64"` // Vote weight/balance
	CreatedAt  time.Time
}

type EmailSubscription struct {
	ID        uint64 `gorm:"primaryKey"`
	MessageID uint64
	Email     string `gorm:"size:256;not null"`
	SentAt    *time.Time
}

// Track information for different referendum tracks
type Track struct {
	ID                 uint16 `gorm:"primaryKey"`
	NetworkID          uint8  `gorm:"index;not null"`
	Name               string `gorm:"size:64;not null"`
	MaxDeciding        uint32
	DecisionDeposit    string `gorm:"size:64"`
	PreparePeriod      uint32
	DecisionPeriod     uint32
	ConfirmPeriod      uint32
	MinEnactmentPeriod uint32
	MinApproval        string `gorm:"size:32"`
	MinSupport         string `gorm:"size:32"`
}

// Preimage stores proposal content
type Preimage struct {
	Hash      string `gorm:"primaryKey;size:128"`
	Data      string `gorm:"type:longtext"`
	Length    uint32
	Provider  string `gorm:"size:128"`
	Deposit   string `gorm:"size:64"`
	CreatedAt time.Time
}

type DiscordChannel struct {
	ID          uint64 `gorm:"primaryKey"`
	GuildID     string `gorm:"size:64;not null"`
	ChannelID   string `gorm:"size:64;not null"`
	NetworkID   uint8  `gorm:"not null"`
	ChannelType string `gorm:"size:32;not null"` // "referenda", "feedback"
	CreatedAt   time.Time
}
