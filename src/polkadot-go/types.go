package polkadot

import "encoding/json"

// Header represents a block header
type Header struct {
	ParentHash     string     `json:"parentHash"`
	Number         string     `json:"number"`
	StateRoot      string     `json:"stateRoot"`
	ExtrinsicsRoot string     `json:"extrinsicsRoot"`
	Digest         DigestItem `json:"digest"`
}

// DigestItem represents the digest in a block header
type DigestItem struct {
	Logs []json.RawMessage `json:"logs"`
}

// RuntimeVersion represents the runtime version
type RuntimeVersion struct {
	SpecName           string          `json:"specName"`
	ImplName           string          `json:"implName"`
	AuthoringVersion   uint32          `json:"authoringVersion"`
	SpecVersion        uint32          `json:"specVersion"`
	ImplVersion        uint32          `json:"implVersion"`
	Apis               [][]interface{} `json:"apis"`
	TransactionVersion uint32          `json:"transactionVersion"`
	StateVersion       uint32          `json:"stateVersion"`
}

// Block represents a full block
type Block struct {
	Header     Header   `json:"header"`
	Extrinsics []string `json:"extrinsics"`
}

// StorageChangeSet represents changes to storage
type StorageChangeSet struct {
	Block   string     `json:"block"`
	Changes [][]string `json:"changes"`
}

// ReferendumInfo represents the info for a referendum
type ReferendumInfo struct {
	Status          string
	Track           uint16
	Origin          string
	Proposal        string // Preimage hash
	ProposalLen     uint32 // Preimage length
	Enactment       string
	Submitted       uint32
	Submission      Submission
	DecisionDeposit *Deposit
	Decision        *DecisionStatus
	Tally           Tally
	InQueue         bool
	// For finished referenda
	ApprovedAt  uint32
	RejectedAt  uint32
	CancelledAt uint32
	TimedOutAt  uint32
	KilledAt    uint32
}

// Submission info
type Submission struct {
	Who    string
	Amount string
}

// Deposit info
type Deposit struct {
	Who    string
	Amount string
}

// DecisionStatus for ongoing referenda
type DecisionStatus struct {
	Since      uint32
	Confirming *uint32
}

// Tally represents vote counts
type Tally struct {
	Ayes     string
	Nays     string
	Support  string
	Approval string // Calculated percentage
}

// BoundedCall represents a proposal
type BoundedCall struct {
	Hash string
	Len  uint32
	Data []byte
}

// TrackInfo contains track configuration
type TrackInfo struct {
	Name               string
	MaxDeciding        uint32
	DecisionDeposit    string
	PreparePeriod      uint32
	DecisionPeriod     uint32
	ConfirmPeriod      uint32
	MinEnactmentPeriod uint32
	MinApproval        string
	MinSupport         string
}
