package gov

import "time"

// NetworkRPC represents RPC endpoints for networks
type NetworkRPC struct {
	ID        uint32 `gorm:"primaryKey"`
	NetworkID uint8
	URL       string `gorm:"size:256;not null"`
	Active    bool   `gorm:"default:true"`
}

// DaoMember represents a DAO member
type DaoMember struct {
	Address string `gorm:"primaryKey;size:128"`
	Discord string `gorm:"size:64"`
	IsAdmin bool   `gorm:"default:false"`
}

// Ref represents a referendum/proposal
type Ref struct {
	ID                      uint64 `gorm:"primaryKey;autoIncrement"`
	NetworkID               uint8  `gorm:"index:idx_proposal_network_ref,unique"`
	RefID                   uint64 `gorm:"index:idx_proposal_network_ref,unique"`
	Submitter               string
	Title                   *string
	Status                  *string
	TrackID                 *uint16
	Origin                  *string
	Enactment               *string
	Submitted               uint64
	SubmittedAt             *time.Time
	DecisionStart           uint64
	DecisionEnd             uint64
	ConfirmStart            uint64
	ConfirmEnd              uint64
	Approved                bool
	Support                 *string
	Approval                *string
	Ayes                    *string
	Nays                    *string
	Turnout                 *string
	Electorate              *string
	PreimageHash            *string
	PreimageLen             *uint32
	DecisionDepositWho      *string
	DecisionDepositAmount   *string
	SubmissionDepositWho     *string
	SubmissionDepositAmount *string
	LastReplyCheck          *time.Time
	Finalized               bool
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

// RefMessage represents a message in a referendum thread
type RefMessage struct {
	ID                   uint64  `gorm:"primaryKey;autoIncrement"`
	RefID                uint64  `gorm:"index"`
	Author               string
	Body                 string  `gorm:"type:text"`
	CreatedAt            time.Time
	Internal             bool    `gorm:"default:false"`
	PolkassemblyUserID    *uint32
	PolkassemblyUsername string
	PolkassemblyCommentID *string `gorm:"type:varchar(64)"`
}

// RefProponent represents a proposal participant
type RefProponent struct {
	RefID   uint64 `gorm:"primaryKey"`
	Address string `gorm:"primaryKey;size:128"`
	Role    string `gorm:"size:32"`
	Active  int8   `gorm:"default:1"`
}

// DaoVote represents a DAO vote on a referendum
type DaoVote struct {
	ID          uint64 `gorm:"primaryKey"`
	RefID       uint64 `gorm:"index;not null"`
	DaoMemberID string `gorm:"size:128;not null"`
	Choice      int16  `gorm:"not null"`
	CreatedAt   time.Time
}

