package types

import "time"

// Networks
type Network struct {
	ID                 uint8  `gorm:"primaryKey"`
	Name               string `gorm:"size:32;unique;not null"`
	Symbol             string `gorm:"size:8;not null"`
	URL                string `gorm:"size:256;not null"`
	DiscordChannelID   string `gorm:"size:64"`
	PolkassemblyURL    string `gorm:"size:256"`
	SubsquareURL       string `gorm:"size:256"`
	PolkassemblyPrefix string `gorm:"size:32"` // e.g., "polkadot", "kusama" for API calls
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
	ID    uint8  `gorm:"primaryKey"`
	Name  string `gorm:"size:32;not null"`
	Value string `gorm:"size:256;not null"`
}

// DAO members
type DaoMember struct {
	Address string `gorm:"primaryKey;size:128"`
	Discord string `gorm:"size:64"`
	IsAdmin bool   `gorm:"default:false"` // Add this field
}

// Proposals/Referenda
type Ref struct {
	ID                      uint64 `gorm:"primaryKey"`
	NetworkID               uint8  `gorm:"index;not null"`
	RefID                   uint64 `gorm:"not null"`
	Submitter               string `gorm:"size:128;not null"`
	Title                   string `gorm:"size:255"`
	Status                  string `gorm:"size:32"`
	TrackID                 uint16 `gorm:"index"`
	Origin                  string `gorm:"size:64"`
	Enactment               string `gorm:"size:32"`
	Submitted               uint64 `gorm:"default:0"`
	SubmittedAt             *time.Time
	DecisionStart           uint64 `gorm:"default:0"`
	DecisionEnd             uint64 `gorm:"default:0"`
	ConfirmStart            uint64 `gorm:"default:0"`
	ConfirmEnd              uint64 `gorm:"default:0"`
	Approved                bool   `gorm:"default:false"`
	Support                 string `gorm:"size:64"`
	Approval                string `gorm:"size:64"`
	Ayes                    string `gorm:"size:64"`
	Nays                    string `gorm:"size:64"`
	Turnout                 string `gorm:"size:64"`
	Electorate              string `gorm:"size:64"`
	PreimageHash            string `gorm:"size:128"`
	PreimageLen             uint32
	DecisionDepositWho      string `gorm:"size:128"`
	DecisionDepositAmount   string `gorm:"size:64"`
	SubmissionDepositWho    string `gorm:"size:128"`
	SubmissionDepositAmount string `gorm:"size:64"`
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

// Messages between DAO and proponents
type RefMessage struct {
	ID        uint64 `gorm:"primaryKey"`
	RefID     uint64 `gorm:"index;not null"`
	Author    string `gorm:"size:128;not null"`
	Body      string `gorm:"type:text;not null"`
	Internal  bool   `gorm:"default:false"`
	CreatedAt time.Time
}

// Proposal participants
type RefProponent struct {
	RefID   uint64 `gorm:"primaryKey"`
	Address string `gorm:"primaryKey;size:128"`
	Role    string `gorm:"size:32"` // submitter, voter, delegator, etc
	Active  int8   `gorm:"default:1"`
}

// Email subscriptions
type RefSub struct {
	ID        uint64 `gorm:"primaryKey"`
	MessageID uint64 `gorm:"index;not null"`
	Email     string `gorm:"size:256;not null"`
	SentAt    *time.Time
}

// DAO votes (internal voting, not on-chain)
type DaoVote struct {
	ID          uint64 `gorm:"primaryKey"`
	RefID       uint64 `gorm:"index;not null"`
	DaoMemberID string `gorm:"size:128;not null"`
	Choice      int16  `gorm:"not null"`
	CreatedAt   time.Time
}

// For backward compatibility with existing code
type Proposal = Ref
type ProposalParticipant = RefProponent
type Message = RefMessage
type EmailSubscription = RefSub
type Vote = DaoVote
type RPC = NetworkRPC

// Legacy structures no longer used
type Track struct{}
type Preimage struct{}
type DiscordChannel struct{}
