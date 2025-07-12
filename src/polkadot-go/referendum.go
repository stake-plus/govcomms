package polkadot

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log"
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
					// Try legacy format for very old referenda
					histInfo, err = decodeLegacyReferendumInfo(histRaw, refID)
					if err != nil {
						return nil, fmt.Errorf("decode historical data: %w", err)
					}
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

// accountIDToSS58 converts an AccountID to generic Substrate SS58 format (prefix 42)
func accountIDToSS58(accountID types.AccountID) string {
	// SS58 encoding with network prefix 42 for generic Substrate addresses
	prefix := byte(42)

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

// decodeLegacyReferendumInfo decodes very old referendum formats
func decodeLegacyReferendumInfo(data []byte, refID uint32) (*ReferendumInfo, error) {
	decoder := scale.NewDecoder(bytes.NewReader(data))

	// Read variant
	variant, err := decoder.ReadOneByte()
	if err != nil {
		return nil, fmt.Errorf("read variant: %w", err)
	}

	info := &ReferendumInfo{}

	// Handle legacy "Ongoing" format
	if variant == 0 {
		info.Status = "Ongoing"
		info.Track = 0 // Legacy refs were all on root track
		info.Origin = "system"

		// Legacy format had different structure
		// Try to decode what we can

		// Skip some bytes that might be proposal data
		// The exact format varies by runtime version
		if len(data) > 100 {
			// Skip to where we might find an AccountID
			decoder = scale.NewDecoder(bytes.NewReader(data[40:]))
		}

		// Try to find an AccountID pattern (32 bytes that look like an account)
		for i := 0; i < len(data)-32; i++ {
			testAccount := data[i : i+32]
			// Simple heuristic: valid accounts usually have some non-zero bytes
			nonZero := 0
			for _, b := range testAccount {
				if b != 0 {
					nonZero++
				}
			}

			if nonZero > 10 && nonZero < 30 {
				// This might be an account
				var accountID types.AccountID
				copy(accountID[:], testAccount)
				info.Submission.Who = accountIDToSS58(accountID)
				info.Submission.Amount = "10000000000" // Default deposit
				break
			}
		}

		if info.Submission.Who == "" {
			info.Submission.Who = "Unknown"
		}

		return info, nil
	}

	// For other variants, return minimal info
	info.Track = 0
	info.Origin = "system"
	info.Submission.Who = "Unknown"
	info.Submission.Amount = "0"

	switch variant {
	case 1:
		info.Status = "Approved"
	case 2:
		info.Status = "Rejected"
	case 3:
		info.Status = "Cancelled"
	case 4:
		info.Status = "TimedOut"
	case 5:
		info.Status = "Killed"
	default:
		info.Status = "Unknown"
	}

	return info, nil
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

		// Origin
		origin, err := decodeOrigin(decoder)
		if err != nil {
			return nil, fmt.Errorf("decode origin: %w", err)
		}
		info.Origin = origin

		// Proposal - Bounded<CallOf<T, I>, T::Preimages>
		// First byte indicates the proposal type
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
			// Handle other legacy proposal types
			// These might be from different runtime versions
			log.Printf("Unknown proposal type %d for ref %d, attempting to skip", proposalType, refID)

			// Based on the proposal type, we need to skip different amounts of data
			switch proposalType {
			case 3, 4, 5:
				// These might be other legacy hash types
				var hash types.Hash
				if err := decoder.Decode(&hash); err == nil {
					info.Proposal = hash.Hex()
					info.ProposalLen = 0
				}
			case 6, 7, 8, 9, 10:
				// Skip 32 bytes (hash)
				skipBytes(decoder, 32)
			case 11, 12, 13, 14:
				// Skip 32 bytes (hash) + 4 bytes (length)
				skipBytes(decoder, 36)
			default:
				// Unknown format, try to continue
				skipBytes(decoder, 32)
			}

			info.Proposal = ""
			info.ProposalLen = 0
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
		if err != nil {
			return nil, fmt.Errorf("read approved deposit option: %w", err)
		}

		if hasDeposit == 1 {
			var who types.AccountID
			if err := decoder.Decode(&who); err != nil {
				return nil, fmt.Errorf("decode approved deposit who: %w", err)
			}
			info.Submission.Who = accountIDToSS58(who)

			var amount types.U128
			if err := decoder.Decode(&amount); err != nil {
				return nil, fmt.Errorf("decode approved deposit amount: %w", err)
			}
			info.Submission.Amount = amount.String()
		} else {
			info.Submission.Who = "Unknown"
			info.Submission.Amount = "0"
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
		if err != nil {
			return nil, fmt.Errorf("read rejected deposit option: %w", err)
		}

		if hasDeposit == 1 {
			var who types.AccountID
			if err := decoder.Decode(&who); err != nil {
				return nil, fmt.Errorf("decode rejected deposit who: %w", err)
			}
			info.Submission.Who = accountIDToSS58(who)

			var amount types.U128
			if err := decoder.Decode(&amount); err != nil {
				return nil, fmt.Errorf("decode rejected deposit amount: %w", err)
			}
			info.Submission.Amount = amount.String()
		} else {
			info.Submission.Who = "Unknown"
			info.Submission.Amount = "0"
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
		if err != nil {
			return nil, fmt.Errorf("read cancelled deposit option: %w", err)
		}

		if hasDeposit == 1 {
			var who types.AccountID
			if err := decoder.Decode(&who); err != nil {
				return nil, fmt.Errorf("decode cancelled deposit who: %w", err)
			}
			info.Submission.Who = accountIDToSS58(who)

			var amount types.U128
			if err := decoder.Decode(&amount); err != nil {
				return nil, fmt.Errorf("decode cancelled deposit amount: %w", err)
			}
			info.Submission.Amount = amount.String()
		} else {
			info.Submission.Who = "Unknown"
			info.Submission.Amount = "0"
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
		if err != nil {
			return nil, fmt.Errorf("read timedout deposit option: %w", err)
		}

		if hasDeposit == 1 {
			var who types.AccountID
			if err := decoder.Decode(&who); err != nil {
				return nil, fmt.Errorf("decode timedout deposit who: %w", err)
			}
			info.Submission.Who = accountIDToSS58(who)

			var amount types.U128
			if err := decoder.Decode(&amount); err != nil {
				return nil, fmt.Errorf("decode timedout deposit amount: %w", err)
			}
			info.Submission.Amount = amount.String()
		} else {
			info.Submission.Who = "Unknown"
			info.Submission.Amount = "0"
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
