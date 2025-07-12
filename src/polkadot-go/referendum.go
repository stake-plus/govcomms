package polkadot

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/big"

	"github.com/centrifuge/go-substrate-rpc-client/v4/scale"
	"github.com/centrifuge/go-substrate-rpc-client/v4/types"
	"github.com/centrifuge/go-substrate-rpc-client/v4/types/codec"
	"github.com/mr-tron/base58"
)

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

	// Try to decode the referendum data
	info, err := decodeReferendumInfo(raw, refID)
	if err == nil {
		return info, nil
	}

	// If decode failed, check if it's a cleared referendum
	// For cleared refs, the data is just the variant byte + block number
	if len(raw) >= 5 {
		decoder := scale.NewDecoder(bytes.NewReader(raw))

		// Read variant
		variant, err := decoder.ReadOneByte()
		if err == nil && variant >= 1 && variant <= 5 {
			// This is a cleared referendum, get the block number
			var blockNum uint32
			if err := decoder.Decode(&blockNum); err == nil {
				// Now fetch historical data
				targetBlock := uint64(blockNum) - 1
				blockHash, err := c.GetBlockHash(&targetBlock)
				if err != nil {
					return nil, fmt.Errorf("get block hash for %d: %w", targetBlock, err)
				}

				// Query at historical block
				var histRaw types.StorageDataRaw
				var hash types.Hash
				err = codec.DecodeFromHex(blockHash, &hash)
				if err != nil {
					return nil, fmt.Errorf("decode block hash: %w", err)
				}

				ok, err := c.api.RPC.State.GetStorage(storageKey, &histRaw, hash)
				if err != nil {
					return nil, fmt.Errorf("get storage at block %d: %w", targetBlock, err)
				}

				if !ok || len(histRaw) == 0 {
					return nil, fmt.Errorf("referendum %d not found at block %d", refID, targetBlock)
				}

				// Decode the historical data
				histInfo, err := decodeReferendumInfo(histRaw, refID)
				if err != nil {
					return nil, fmt.Errorf("decode historical data: %w", err)
				}

				// Update the status based on the variant we got
				switch variant {
				case 1:
					histInfo.Status = "Approved"
					histInfo.ApprovedAt = blockNum
				case 2:
					histInfo.Status = "Rejected"
					histInfo.RejectedAt = blockNum
				case 3:
					histInfo.Status = "Cancelled"
					histInfo.CancelledAt = blockNum
				case 4:
					histInfo.Status = "TimedOut"
					histInfo.TimedOutAt = blockNum
				case 5:
					histInfo.Status = "Killed"
					histInfo.KilledAt = blockNum
				}

				return histInfo, nil
			}
		}
	}

	// Return original error
	return nil, fmt.Errorf("decode referendum %d: %w", refID, err)
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

// accountIDToSS58 converts an AccountID to SS58 format for Polkadot (prefix 0)
func accountIDToSS58(accountID types.AccountID) string {
	// SS58 encoding with network prefix 0 for Polkadot
	prefix := byte(0)

	// Create the payload: prefix + accountID + checksum
	payload := make([]byte, 0, 35)
	payload = append(payload, prefix)
	payload = append(payload, accountID[:]...)

	// Calculate SS58 checksum
	checksumInput := []byte("SS58PRE")
	checksumInput = append(checksumInput, prefix)
	checksumInput = append(checksumInput, accountID[:]...)

	checksum := Blake2_256(checksumInput)

	// Append first 2 bytes of checksum
	payload = append(payload, checksum[0:2]...)

	// Base58 encode
	return base58.Encode(payload)
}

// decodeReferendumInfo decodes referendum data based on the structure from the documentation
func decodeReferendumInfo(data []byte, refID uint32) (*ReferendumInfo, error) {
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

		// Track (u16)
		var track uint16
		if err := decoder.Decode(&track); err != nil {
			return nil, fmt.Errorf("decode track: %w", err)
		}
		info.Track = track

		// Origin - complex enum
		// First byte is the origin type
		originType, err := decoder.ReadOneByte()
		if err != nil {
			return nil, fmt.Errorf("read origin type: %w", err)
		}

		if originType == 0 { // system
			info.Origin = "system"
		} else if originType == 1 { // Origins pallet
			// Read the origin variant
			originVariant, err := decoder.ReadOneByte()
			if err != nil {
				return nil, fmt.Errorf("read origin variant: %w", err)
			}
			info.Origin = getOriginName(originVariant)
		} else {
			info.Origin = fmt.Sprintf("Unknown(%d)", originType)
		}

		// Proposal - Bounded<CallOf<T, I>, T::Preimages>
		// First byte indicates Lookup(0), Legacy(1), or Inline(2)
		proposalType, err := decoder.ReadOneByte()
		if err != nil {
			return nil, fmt.Errorf("read proposal type: %w", err)
		}

		switch proposalType {
		case 0: // Lookup
			var hash types.Hash
			if err := decoder.Decode(&hash); err != nil {
				return nil, fmt.Errorf("decode lookup hash: %w", err)
			}
			var length types.U32
			if err := decoder.Decode(&length); err != nil {
				return nil, fmt.Errorf("decode lookup length: %w", err)
			}
			info.Proposal = hash.Hex()
			info.ProposalLen = uint32(length)

		case 1: // Legacy
			var hash types.Hash
			if err := decoder.Decode(&hash); err != nil {
				return nil, fmt.Errorf("decode legacy hash: %w", err)
			}
			info.Proposal = hash.Hex()
			info.ProposalLen = 0

		case 2: // Inline
			// Read bounded vec
			var length types.UCompact
			if err := decoder.Decode(&length); err != nil {
				return nil, fmt.Errorf("decode inline length: %w", err)
			}
			callLen := uint32(length.Int64())
			callData := make([]byte, callLen)
			if err := decoder.Decode(&callData); err != nil {
				return nil, fmt.Errorf("decode inline call: %w", err)
			}
			// Compute hash
			hash := Blake2_256(callData)
			info.Proposal = codec.HexEncodeToString(hash)
			info.ProposalLen = callLen

		default:
			return nil, fmt.Errorf("unknown proposal type: %d", proposalType)
		}

		// Enactment - DispatchTime<BlockNumberFor<T, I>>
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

		// Submitted - BlockNumberFor<T, I>
		var submitted uint32
		if err := decoder.Decode(&submitted); err != nil {
			return nil, fmt.Errorf("decode submitted: %w", err)
		}
		info.Submitted = submitted

		// SubmissionDeposit - Deposit<T::AccountId, BalanceOf<T, I>>
		var submitter types.AccountID
		if err := decoder.Decode(&submitter); err != nil {
			return nil, fmt.Errorf("decode submitter: %w", err)
		}
		info.Submission.Who = accountIDToSS58(submitter)

		var amount types.U128
		if err := decoder.Decode(&amount); err != nil {
			return nil, fmt.Errorf("decode submission amount: %w", err)
		}
		info.Submission.Amount = amount.String()

		// DecisionDeposit - Option<Deposit<T::AccountId, BalanceOf<T, I>>>
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
			info.DecisionDeposit.Who = accountIDToSS58(decisionWho)

			var decisionAmount types.U128
			if err := decoder.Decode(&decisionAmount); err != nil {
				return nil, fmt.Errorf("decode decision deposit amount: %w", err)
			}
			info.DecisionDeposit.Amount = decisionAmount.String()
		}

		// Deciding - Option<DecidingStatus<BlockNumberFor<T, I>>>
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

			// Confirming - Option<BlockNumberFor<T, I>>
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

		// Tally - T::Tally
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

		// Calculate approval percentage
		totalVotes := new(big.Int).Add(ayes.Int, nays.Int)
		if totalVotes.Cmp(big.NewInt(0)) > 0 {
			approval := new(big.Int).Mul(ayes.Int, big.NewInt(10000))
			approval.Div(approval, totalVotes)
			info.Tally.Approval = fmt.Sprintf("%d.%02d%%", approval.Int64()/100, approval.Int64()%100)
		}

		// InQueue - bool
		var inQueue bool
		if err := decoder.Decode(&inQueue); err != nil {
			return nil, fmt.Errorf("decode inQueue: %w", err)
		}
		info.InQueue = inQueue

		// Alarm - Option<(BlockNumberFor<T, I>, ScheduleAddressOf<T, I>)>
		hasAlarm, err := decoder.ReadOneByte()
		if err == nil && hasAlarm == 1 {
			// Skip alarm data (block number + schedule address)
			skipBytes(decoder, 40) // 4 bytes block + 32 bytes address + 4 bytes index
		}

		return info, nil

	case 1: // Approved
		info.Status = "Approved"

		// Since - BlockNumberFor<T, I>
		var since uint32
		if err := decoder.Decode(&since); err != nil {
			return nil, fmt.Errorf("decode approved since: %w", err)
		}
		info.ApprovedAt = since

		// Deposit - Option<Deposit<T::AccountId, BalanceOf<T, I>>>
		hasDeposit, err := decoder.ReadOneByte()
		if err == nil && hasDeposit == 1 {
			var who types.AccountID
			if err := decoder.Decode(&who); err == nil {
				info.Submission.Who = accountIDToSS58(who)
			}
			var amount types.U128
			if err := decoder.Decode(&amount); err == nil {
				info.Submission.Amount = amount.String()
			}
		} else {
			info.Submission.Who = "Unknown"
		}

		// Approved/Rejected/Cancelled/TimedOut don't store track directly
		// but we can infer Root track (0) for these old referendums
		info.Track = 0
		return info, nil

	case 2: // Rejected
		info.Status = "Rejected"

		// Since - BlockNumberFor<T, I>
		var since uint32
		if err := decoder.Decode(&since); err != nil {
			return nil, fmt.Errorf("decode rejected since: %w", err)
		}
		info.RejectedAt = since

		// Deposit - Option<Deposit<T::AccountId, BalanceOf<T, I>>>
		hasDeposit, err := decoder.ReadOneByte()
		if err == nil && hasDeposit == 1 {
			var who types.AccountID
			if err := decoder.Decode(&who); err == nil {
				info.Submission.Who = accountIDToSS58(who)
			}
			var amount types.U128
			if err := decoder.Decode(&amount); err == nil {
				info.Submission.Amount = amount.String()
			}
		} else {
			info.Submission.Who = "Unknown"
		}
		info.Track = 0
		return info, nil

	case 3: // Cancelled
		info.Status = "Cancelled"

		// Since - BlockNumberFor<T, I>
		var since uint32
		if err := decoder.Decode(&since); err != nil {
			return nil, fmt.Errorf("decode cancelled since: %w", err)
		}
		info.CancelledAt = since

		// Deposit - Option<Deposit<T::AccountId, BalanceOf<T, I>>>
		hasDeposit, err := decoder.ReadOneByte()
		if err == nil && hasDeposit == 1 {
			var who types.AccountID
			if err := decoder.Decode(&who); err == nil {
				info.Submission.Who = accountIDToSS58(who)
			}
			var amount types.U128
			if err := decoder.Decode(&amount); err == nil {
				info.Submission.Amount = amount.String()
			}
		} else {
			info.Submission.Who = "Unknown"
		}
		info.Track = 0
		return info, nil

	case 4: // TimedOut
		info.Status = "TimedOut"

		// Since - BlockNumberFor<T, I>
		var since uint32
		if err := decoder.Decode(&since); err != nil {
			return nil, fmt.Errorf("decode timedout since: %w", err)
		}
		info.TimedOutAt = since

		// Deposit - Option<Deposit<T::AccountId, BalanceOf<T, I>>>
		hasDeposit, err := decoder.ReadOneByte()
		if err == nil && hasDeposit == 1 {
			var who types.AccountID
			if err := decoder.Decode(&who); err == nil {
				info.Submission.Who = accountIDToSS58(who)
			}
			var amount types.U128
			if err := decoder.Decode(&amount); err == nil {
				info.Submission.Amount = amount.String()
			}
		} else {
			info.Submission.Who = "Unknown"
		}
		info.Track = 0
		return info, nil

	case 5: // Killed
		info.Status = "Killed"

		// Since - BlockNumberFor<T, I>
		var since uint32
		if err := decoder.Decode(&since); err != nil {
			return nil, fmt.Errorf("decode killed since: %w", err)
		}
		info.KilledAt = since
		info.Track = 0
		return info, nil

	default:
		return nil, fmt.Errorf("unknown referendum variant: %d", variant)
	}
}

// getOriginName maps the origin variant to a name
func getOriginName(variant uint8) string {
	// These map to the Polkadot runtime Origins enum
	origins := map[uint8]string{
		0:  "StakingAdmin",
		1:  "Treasurer",
		2:  "FellowshipAdmin",
		3:  "GeneralAdmin",
		4:  "AuctionAdmin",
		5:  "LeaseAdmin",
		6:  "ReferendumCanceller",
		7:  "ReferendumKiller",
		8:  "SmallTipper",
		9:  "BigTipper",
		10: "SmallSpender",
		11: "MediumSpender",
		12: "BigSpender",
		13: "WhitelistedCaller",
		14: "WishForChange",
	}

	if name, ok := origins[variant]; ok {
		return name
	}
	return fmt.Sprintf("Custom(%d)", variant)
}

// Helper function to skip bytes
func skipBytes(decoder *scale.Decoder, n int) error {
	buf := make([]byte, n)
	return decoder.Decode(&buf)
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

	// Return simplified track info
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
		0:    "Root",
		1:    "WhitelistedCaller",
		10:   "StakingAdmin",
		11:   "Treasurer",
		12:   "LeaseAdmin",
		13:   "FellowshipAdmin",
		14:   "GeneralAdmin",
		15:   "AuctionAdmin",
		20:   "ReferendumCanceller",
		21:   "ReferendumKiller",
		30:   "SmallTipper",
		31:   "BigTipper",
		32:   "SmallSpender",
		33:   "MediumSpender",
		34:   "BigSpender",
		1000: "WishForChange",
	}

	if name, ok := trackNames[trackID]; ok {
		return name
	}
	return fmt.Sprintf("Track%d", trackID)
}
