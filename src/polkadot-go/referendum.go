package polkadot

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/big"

	"github.com/centrifuge/go-substrate-rpc-client/v4/scale"
	"github.com/centrifuge/go-substrate-rpc-client/v4/types"
	"github.com/centrifuge/go-substrate-rpc-client/v4/types/codec"
)

// ClearedReferendumResponse represents the JSON response for a cleared referendum
type ClearedReferendumResponse struct {
	Approved  []interface{} `json:"Approved"`
	Rejected  []interface{} `json:"Rejected"`
	Cancelled []interface{} `json:"Cancelled"`
	TimedOut  []interface{} `json:"TimedOut"`
	Killed    []interface{} `json:"Killed"`
}

// GetReferendumInfo fetches and decodes referendum info
func (c *Client) GetReferendumInfo(refID uint32) (*ReferendumInfo, error) {
	// Create storage key for ReferendumInfoFor
	key := createReferendumStorageKey(refID)

	// Query storage
	var raw types.StorageDataRaw
	storageKey := types.NewStorageKey(key)
	ok, err := c.api.RPC.State.GetStorageLatest(storageKey, &raw)
	if err != nil {
		return nil, fmt.Errorf("get storage: %w", err)
	}
	if !ok || len(raw) == 0 {
		return nil, fmt.Errorf("referendum %d does not exist", refID)
	}

	// Try to decode as ongoing referendum
	info, err := decodeReferendumInfo(raw)
	if err != nil {
		// If decode fails, try as cleared referendum
		return decodeFinishedReferendum(raw, refID)
	}

	return info, nil
}

// createReferendumStorageKey creates the storage key for a referendum
func createReferendumStorageKey(refID uint32) []byte {
	// Referenda.ReferendumInfoFor storage key
	palletHash := Twox128([]byte("Referenda"))
	storageHash := Twox128([]byte("ReferendumInfoFor"))

	// Encode the referendum ID
	refIDBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(refIDBytes, refID)

	// Use Blake2_128_Concat hasher for the key
	hashedKey := append(Blake2_128(refIDBytes), refIDBytes...)

	// Combine all parts
	key := append(palletHash, storageHash...)
	key = append(key, hashedKey...)

	return key
}

// decodeReferendumInfo decodes ongoing referendum data
func decodeReferendumInfo(data []byte) (*ReferendumInfo, error) {
	decoder := scale.NewDecoder(bytes.NewReader(data))

	// Read variant
	variant, err := decoder.ReadOneByte()
	if err != nil {
		return nil, fmt.Errorf("read variant: %w", err)
	}

	info := &ReferendumInfo{}

	switch variant {
	case 0: // Ongoing
		info.Status = "Ongoing"

		// Track
		var track uint16
		if err := decoder.Decode(&track); err != nil {
			return nil, fmt.Errorf("decode track: %w", err)
		}
		info.Track = track

		// Origin - complex enum
		originVariant, err := decoder.ReadOneByte()
		if err != nil {
			return nil, fmt.Errorf("read origin variant: %w", err)
		}
		info.Origin = getOriginName(originVariant)

		// Skip origin data based on variant
		if err := skipOriginData(decoder, originVariant); err != nil {
			return nil, fmt.Errorf("skip origin data: %w", err)
		}

		// Proposal (bounded call)
		proposal, err := decodeBoundedCall(decoder)
		if err != nil {
			return nil, fmt.Errorf("decode proposal: %w", err)
		}
		info.Proposal = proposal.Hash
		info.ProposalLen = proposal.Len

		// Enactment
		enactmentVariant, err := decoder.ReadOneByte()
		if err != nil {
			return nil, fmt.Errorf("read enactment variant: %w", err)
		}

		var enactmentValue uint32
		if err := decoder.Decode(&enactmentValue); err != nil {
			return nil, fmt.Errorf("decode enactment value: %w", err)
		}

		if enactmentVariant == 0 {
			info.Enactment = fmt.Sprintf("At(%d)", enactmentValue)
		} else {
			info.Enactment = fmt.Sprintf("After(%d)", enactmentValue)
		}

		// Submitted
		var submitted uint32
		if err := decoder.Decode(&submitted); err != nil {
			return nil, fmt.Errorf("decode submitted: %w", err)
		}
		info.Submitted = submitted

		// Submission deposit
		var submitter types.AccountID
		if err := decoder.Decode(&submitter); err != nil {
			return nil, fmt.Errorf("decode submitter: %w", err)
		}
		info.Submission.Who = submitter.ToHexString()

		var amount types.U128
		if err := decoder.Decode(&amount); err != nil {
			return nil, fmt.Errorf("decode amount: %w", err)
		}
		info.Submission.Amount = amount.String()

		// Decision deposit (Option)
		hasDecisionDeposit, err := decoder.ReadOneByte()
		if err != nil {
			return nil, fmt.Errorf("read decision deposit option: %w", err)
		}

		if hasDecisionDeposit == 1 {
			info.DecisionDeposit = &Deposit{}
			var decisionWho types.AccountID
			if err := decoder.Decode(&decisionWho); err != nil {
				return nil, fmt.Errorf("decode decision deposit who: %w", err)
			}
			info.DecisionDeposit.Who = decisionWho.ToHexString()

			var decisionAmount types.U128
			if err := decoder.Decode(&decisionAmount); err != nil {
				return nil, fmt.Errorf("decode decision deposit amount: %w", err)
			}
			info.DecisionDeposit.Amount = decisionAmount.String()
		}

		// Deciding (Option)
		hasDeciding, err := decoder.ReadOneByte()
		if err != nil {
			return nil, fmt.Errorf("read deciding option: %w", err)
		}

		if hasDeciding == 1 {
			info.Decision = &DecisionStatus{}
			var since uint32
			if err := decoder.Decode(&since); err != nil {
				return nil, fmt.Errorf("decode deciding since: %w", err)
			}
			info.Decision.Since = since

			// Confirming (Option)
			hasConfirming, err := decoder.ReadOneByte()
			if err != nil {
				return nil, fmt.Errorf("read confirming option: %w", err)
			}

			if hasConfirming == 1 {
				var confirming uint32
				if err := decoder.Decode(&confirming); err != nil {
					return nil, fmt.Errorf("decode confirming: %w", err)
				}
				info.Decision.Confirming = &confirming
			}
		}

		// Tally
		var ayes, nays, support types.U128
		if err := decoder.Decode(&ayes); err != nil {
			return nil, fmt.Errorf("decode ayes: %w", err)
		}
		if err := decoder.Decode(&nays); err != nil {
			return nil, fmt.Errorf("decode nays: %w", err)
		}
		if err := decoder.Decode(&support); err != nil {
			return nil, fmt.Errorf("decode support: %w", err)
		}

		info.Tally.Ayes = ayes.String()
		info.Tally.Nays = nays.String()
		info.Tally.Support = support.String()

		// Calculate approval percentage if we have total votes
		totalVotes := new(big.Int).Add(ayes.Int, nays.Int)
		if totalVotes.Cmp(big.NewInt(0)) > 0 {
			approval := new(big.Int).Mul(ayes.Int, big.NewInt(10000))
			approval.Div(approval, totalVotes)
			info.Tally.Approval = fmt.Sprintf("%d.%02d%%", approval.Int64()/100, approval.Int64()%100)
		}

		// In queue
		var inQueue bool
		if err := decoder.Decode(&inQueue); err != nil {
			return nil, fmt.Errorf("decode inQueue: %w", err)
		}
		info.InQueue = inQueue

		// Alarm (Option) - skip for now
		hasAlarm, err := decoder.ReadOneByte()
		if err != nil {
			return nil, fmt.Errorf("read alarm option: %w", err)
		}
		if hasAlarm == 1 {
			// Skip alarm data (block number + scheduler data)
			skipBytes(decoder, 4) // block number
			skipBytes(decoder, 4) // scheduler index
		}

		return info, nil

	default:
		return nil, fmt.Errorf("not an ongoing referendum, variant: %d", variant)
	}
}

// decodeFinishedReferendum handles approved/rejected/cancelled/timedout/killed referenda
func decodeFinishedReferendum(data []byte, refID uint32) (*ReferendumInfo, error) {
	decoder := scale.NewDecoder(bytes.NewReader(data))

	// Read variant
	variant, err := decoder.ReadOneByte()
	if err != nil {
		return nil, fmt.Errorf("read variant: %w", err)
	}

	info := &ReferendumInfo{}

	switch variant {
	case 1: // Approved
		info.Status = "Approved"
		// Approved has: (since, Option<Deposit>, Option<DecidingStatus>)
		var since uint32
		if err := decoder.Decode(&since); err != nil {
			return nil, fmt.Errorf("decode approved since: %w", err)
		}
		info.ApprovedAt = since

		// Option<Deposit>
		hasDeposit, _ := decoder.ReadOneByte()
		if hasDeposit == 1 {
			info.Submission = Submission{}
			var who types.AccountID
			decoder.Decode(&who)
			info.Submission.Who = who.ToHexString()
			var amount types.U128
			decoder.Decode(&amount)
			info.Submission.Amount = amount.String()
		}

	case 2: // Rejected
		info.Status = "Rejected"
		// Rejected has: (since, Option<Deposit>, Option<DecidingStatus>)
		var since uint32
		if err := decoder.Decode(&since); err != nil {
			return nil, fmt.Errorf("decode rejected since: %w", err)
		}
		info.RejectedAt = since

		// Option<Deposit>
		hasDeposit, _ := decoder.ReadOneByte()
		if hasDeposit == 1 {
			info.Submission = Submission{}
			var who types.AccountID
			decoder.Decode(&who)
			info.Submission.Who = who.ToHexString()
			var amount types.U128
			decoder.Decode(&amount)
			info.Submission.Amount = amount.String()
		}

	case 3: // Cancelled
		info.Status = "Cancelled"
		// Cancelled has: (since, Option<Deposit>, Option<DecidingStatus>)
		var since uint32
		if err := decoder.Decode(&since); err != nil {
			return nil, fmt.Errorf("decode cancelled since: %w", err)
		}
		info.CancelledAt = since

		// Option<Deposit>
		hasDeposit, _ := decoder.ReadOneByte()
		if hasDeposit == 1 {
			info.Submission = Submission{}
			var who types.AccountID
			decoder.Decode(&who)
			info.Submission.Who = who.ToHexString()
			var amount types.U128
			decoder.Decode(&amount)
			info.Submission.Amount = amount.String()
		}

	case 4: // TimedOut
		info.Status = "TimedOut"
		// TimedOut has: (since, Option<Deposit>, Option<DecidingStatus>)
		var since uint32
		if err := decoder.Decode(&since); err != nil {
			return nil, fmt.Errorf("decode timedout since: %w", err)
		}
		info.TimedOutAt = since

		// Option<Deposit>
		hasDeposit, _ := decoder.ReadOneByte()
		if hasDeposit == 1 {
			info.Submission = Submission{}
			var who types.AccountID
			decoder.Decode(&who)
			info.Submission.Who = who.ToHexString()
			var amount types.U128
			decoder.Decode(&amount)
			info.Submission.Amount = amount.String()
		}

	case 5: // Killed
		info.Status = "Killed"
		// Killed has: (since)
		var since uint32
		if err := decoder.Decode(&since); err != nil {
			return nil, fmt.Errorf("decode killed since: %w", err)
		}
		info.KilledAt = since

	default:
		return nil, fmt.Errorf("unknown referendum variant: %d", variant)
	}

	return info, nil
}

// decodeBoundedCall decodes a bounded call (proposal)
func decodeBoundedCall(decoder *scale.Decoder) (*BoundedCall, error) {
	// Read compact length
	var length types.UCompact
	if err := decoder.Decode(&length); err != nil {
		return nil, err
	}

	callLen := uint32(length.Int64())

	// For now, we'll just skip the actual call data and compute hash
	// In a full implementation, you'd decode the call to extract recipients
	callData := make([]byte, callLen)
	if err := decoder.Decode(&callData); err != nil {
		return nil, err
	}

	// Compute blake2-256 hash of the call
	hash := Blake2_256(callData)

	return &BoundedCall{
		Hash: codec.HexEncodeToString(hash),
		Len:  callLen,
		Data: callData,
	}, nil
}

// Helper functions
func getOriginName(variant byte) string {
	origins := map[byte]string{
		0:  "Root",
		1:  "WhitelistedCaller",
		2:  "GeneralAdmin",
		3:  "ReferendumCanceller",
		4:  "ReferendumKiller",
		10: "StakingAdmin",
		11: "Treasurer",
		12: "LeaseAdmin",
		13: "FellowshipAdmin",
		14: "SmallTipper",
		15: "BigTipper",
		16: "SmallSpender",
		17: "MediumSpender",
		18: "BigSpender",
		19: "WhitelistedCaller",
	}

	if name, ok := origins[variant]; ok {
		return name
	}
	return fmt.Sprintf("Unknown(%d)", variant)
}

func skipOriginData(decoder *scale.Decoder, variant byte) error {
	switch variant {
	case 1: // Signed
		skipBytes(decoder, 32) // AccountID
		// Most origins have no additional data
	}
	return nil
}

func skipBytes(decoder *scale.Decoder, n int) error {
	// Skip n bytes by reading into a small buffer
	const bufSize = 256
	buf := make([]byte, bufSize)

	for n > 0 {
		toRead := n
		if toRead > bufSize {
			toRead = bufSize
		}

		tmpBuf := buf[:toRead]
		if err := decoder.Decode(&tmpBuf); err != nil {
			return err
		}

		n -= toRead
	}

	return nil
}

// GetReferendumCount gets the total number of referenda
func (c *Client) GetReferendumCount() (uint32, error) {
	// Create storage key for Referenda.ReferendumCount
	palletHash := Twox128([]byte("Referenda"))
	storageHash := Twox128([]byte("ReferendumCount"))
	key := append(palletHash, storageHash...)

	storageKey := types.NewStorageKey(key)
	var count types.U32
	ok, err := c.api.RPC.State.GetStorageLatest(storageKey, &count)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, nil
	}

	return uint32(count), nil
}

// GetTrackInfo gets information about a specific track
func (c *Client) GetTrackInfo(trackID uint16) (*TrackInfo, error) {
	// Create storage key for Referenda.Tracks
	palletHash := Twox128([]byte("Referenda"))
	storageHash := Twox128([]byte("Tracks"))
	key := append(palletHash, storageHash...)

	storageKey := types.NewStorageKey(key)
	var raw types.StorageDataRaw
	ok, err := c.api.RPC.State.GetStorageLatest(storageKey, &raw)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("tracks not found")
	}

	// Decode tracks array and find the one we want
	// This is simplified - you'd need to properly decode the Vec<TrackInfo>
	return &TrackInfo{
		Name:               getTrackName(trackID),
		MaxDeciding:        1,
		DecisionPeriod:     403200, // 28 days default
		ConfirmPeriod:      14400,  // 1 day default
		MinEnactmentPeriod: 14400,
		MinApproval:        "50%",
		MinSupport:         "0.01%",
	}, nil
}

func getTrackName(trackID uint16) string {
	trackNames := map[uint16]string{
		0:  "Root",
		1:  "WhitelistedCaller",
		10: "StakingAdmin",
		11: "Treasurer",
		12: "LeaseAdmin",
		13: "FellowshipAdmin",
		14: "GeneralAdmin",
		15: "AuctionAdmin",
		20: "ReferendumCanceller",
		21: "ReferendumKiller",
		30: "SmallTipper",
		31: "BigTipper",
		32: "SmallSpender",
		33: "MediumSpender",
		34: "BigSpender",
	}

	if name, ok := trackNames[trackID]; ok {
		return name
	}
	return fmt.Sprintf("Track%d", trackID)
}

// GetAccountVotes gets all votes by an account for a referendum
func (c *Client) GetAccountVotes(account string, refID uint32) (*AccountVote, error) {
	// ConvictionVoting.VotingFor storage key
	palletHash := Twox128([]byte("ConvictionVoting"))
	storageHash := Twox128([]byte("VotingFor"))

	// Decode account
	accountBytes, err := DecodeHex(account)
	if err != nil {
		return nil, err
	}

	// First key is account (Blake2_128_Concat)
	accountKey := append(Blake2_128(accountBytes), accountBytes...)

	// Second key is track class - we need to find which track the referendum is on
	// For now, assume track 0 (this would need to be looked up)
	trackBytes := make([]byte, 2)
	binary.LittleEndian.PutUint16(trackBytes, 0)
	trackKey := append(Blake2_128(trackBytes), trackBytes...)

	key := append(palletHash, storageHash...)
	key = append(key, accountKey...)
	key = append(key, trackKey...)

	storageKey := types.NewStorageKey(key)
	var raw types.StorageDataRaw
	ok, err := c.api.RPC.State.GetStorageLatest(storageKey, &raw)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil // No votes
	}

	// Decode VotingInfo
	return decodeVotingInfo(raw, refID)
}

func decodeVotingInfo(data []byte, refID uint32) (*AccountVote, error) {
	decoder := scale.NewDecoder(bytes.NewReader(data))

	// Read variant (Casting, Delegating)
	variant, err := decoder.ReadOneByte()
	if err != nil {
		return nil, err
	}

	switch variant {
	case 0: // Casting
		// Decode Casting struct
		var casting CastingInfo
		if err := decoder.Decode(&casting); err != nil {
			return nil, err
		}

		// Look for our referendum in the votes
		for _, vote := range casting.Votes {
			if vote.RefID == refID {
				return &AccountVote{
					VoteType: "Casting",
					Vote:     &vote,
				}, nil
			}
		}

	case 1: // Delegating
		// Decode delegating info
		var target types.AccountID
		if err := decoder.Decode(&target); err != nil {
			return nil, err
		}

		var conviction uint8
		if err := decoder.Decode(&conviction); err != nil {
			return nil, err
		}

		var balance types.U128
		if err := decoder.Decode(&balance); err != nil {
			return nil, err
		}

		var prior PriorLock
		if err := decoder.Decode(&prior); err != nil {
			return nil, err
		}

		return &AccountVote{
			VoteType: "Delegating",
			Delegating: &DelegatingInfo{
				Target:     target.ToHexString(),
				Conviction: conviction,
				Balance:    balance.String(),
				Prior:      prior,
			},
		}, nil
	}

	return nil, fmt.Errorf("unknown voting variant: %d", variant)
}
