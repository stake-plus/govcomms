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

// ReferendumInfo represents the info for a referendum
type ReferendumInfo struct {
	Status     string
	Track      uint16
	Origin     string
	Proposal   string
	Enactment  string
	Submitted  uint32
	Submission Submission
	Decision   *DecisionStatus
	Tally      Tally
}

// Submission info
type Submission struct {
	Who    string
	Track  uint16
	Origin string
}

// DecisionStatus for ongoing referenda
type DecisionStatus struct {
	Since      uint32
	Confirming *uint32
}

// Tally represents vote counts
type Tally struct {
	Ayes    string
	Nays    string
	Support string
}
