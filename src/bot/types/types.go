package types

import "time"

// Networks
type Network struct {
	ID               uint8  `gorm:"primaryKey"`
	Name             string `gorm:"size:32;unique;not null"`
	Symbol           string `gorm:"size:8;not null"`
	URL              string `gorm:"size:256;not null"`
	DiscordChannelID string `gorm:"size:64"`
}

// Network RPC endpoints
type NetworkRPC struct {
	ID        uint32 `gorm:"primaryKey"`
	NetworkID uint8
	URL       string  `gorm:"size:256;not null"`
	Active    bool    `gorm:"default:true"`
	Network   Network `gorm:"foreignKey:NetworkID;references:ID"`
}

// Settings
type Setting struct {
	ID     uint8  `gorm:"primaryKey"`
	Name   string `gorm:"size:32;not null"`
	Value  string `gorm:"size:256;not null"`
	Active uint8  `gorm:"not null"`
}

// DAO members
type DaoMember struct {
	Address string `gorm:"primaryKey;size:128"`
	Discord string `gorm:"size:64"`
	IsAdmin bool   `gorm:"default:false"`
}

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
	SubmissionDepositWho    *string
	SubmissionDepositAmount *string
	LastReplyCheck          *time.Time
	Finalized               bool
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

type RefThread struct {
	ID        uint64 `gorm:"primaryKey;autoIncrement"`
	ThreadID  string `gorm:"uniqueIndex"`
	RefDBID   uint64 `gorm:"index"`
	NetworkID uint8  `gorm:"index"`
	RefID     uint64 `gorm:"index"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

type RefMessage struct {
	ID                    uint64 `gorm:"primaryKey;autoIncrement"`
	RefID                 uint64 `gorm:"index"`
	Author                string
	Body                  string `gorm:"type:text"`
	CreatedAt             time.Time
	Internal              bool `gorm:"default:false"`
	PolkassemblyUserID    *uint32
	PolkassemblyUsername  string
	PolkassemblyCommentID *string `gorm:"type:varchar(64)"`
}

// Proposal participants
type RefProponent struct {
	RefID   uint64 `gorm:"primaryKey"`
	Address string `gorm:"primaryKey;size:128"`
	Role    string `gorm:"size:32"`
	Active  int8   `gorm:"default:1"`
}

// DAO votes
type DaoVote struct {
	ID          uint64 `gorm:"primaryKey"`
	RefID       uint64 `gorm:"index;not null"`
	DaoMemberID string `gorm:"size:128;not null"`
	Choice      int16  `gorm:"not null"`
	CreatedAt   time.Time
}
