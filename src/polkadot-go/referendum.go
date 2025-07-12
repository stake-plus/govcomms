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
	if len(raw) >= 5 {
		decoder := scale.NewDecoder(bytes.NewReader(raw))
		variant, err := decoder.ReadOneByte()
		if err == nil && variant >= 1 && variant <= 5 {
			// This is a cleared referendum, get the block number
			var blockNum uint32
			if err := decoder.Decode(&blockNum); err == nil {
				// Fetch historical data
				return c.fetchHistoricalReferendum(refID, variant, blockNum, storageKey)
			}
		}
	}

	return nil, fmt.Errorf("decode referendum %d: %w", refID, err)
}

// fetchHistoricalReferendum retrieves referendum data from a historical block
func (c *Client) fetchHistoricalReferendum(refID uint32, variant uint8, blockNum uint32, storageKey types.StorageKey) (*ReferendumInfo, error) {
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

	// Update the status based on the variant
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

	info := &ReferendumInfo{
		Track:  0, // Legacy refs were all on root track
		Origin: "system",
	}

	// Handle legacy "Ongoing" format
	if variant == 0 {
		info.Status = "Ongoing"

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
			info.Submission.Amount = "0"
		}
		return info, nil
	}

	// For finished variants (1-5), they typically have:
	// - uint32: block number when finished
	// - Option<Deposit>: optional deposit info

	// Set status based on variant
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

	// Try to decode common fields for finished states
	if variant >= 1 && variant <= 5 {
		// Decode block number (when the status changed)
		var blockNum uint32
		if err := decoder.Decode(&blockNum); err == nil {
			// Set the appropriate field based on status
			switch variant {
			case 1:
				info.ApprovedAt = blockNum
			case 2:
				info.RejectedAt = blockNum
			case 3:
				info.CancelledAt = blockNum
			case 4:
				info.TimedOutAt = blockNum
			case 5:
				info.KilledAt = blockNum
			}
		}

		// Try to decode Option<Deposit> if variant != 5 (Killed doesn't have deposit)
		if variant != 5 {
			hasDeposit, err := decoder.ReadOneByte()
			if err == nil && hasDeposit == 1 {
				// Decode deposit directly into Submission
				var who types.AccountID
				if err := decoder.Decode(&who); err == nil {
					info.Submission.Who = accountIDToSS58(who)

					var amount types.U128
					if err := decoder.Decode(&amount); err == nil {
						info.Submission.Amount = amount.String()
					} else {
						info.Submission.Amount = "0"
					}
				}
			}
		}
	}

	// Set defaults if we couldn't decode
	if info.Submission.Who == "" {
		info.Submission.Who = "Unknown"
		info.Submission.Amount = "0"
	}

	return info, nil
}

// decodeFinishedReferendum handles all finished referendum states
func decodeFinishedReferendum(decoder *scale.Decoder, status string) (*ReferendumInfo, error) {
	info := &ReferendumInfo{
		Status: status,
		Track:  0, // Finished refs don't store track, assume root
	}

	// Since - BlockNumberFor<T, I>
	var since uint32
	if err := decoder.Decode(&since); err != nil {
		return nil, fmt.Errorf("decode %s since: %w", status, err)
	}

	// Set the appropriate field based on status
	switch status {
	case "Approved":
		info.ApprovedAt = since
	case "Rejected":
		info.RejectedAt = since
	case "Cancelled":
		info.CancelledAt = since
	case "TimedOut":
		info.TimedOutAt = since
	}

	// For Approved and Rejected, there might be additional tally data before the deposit
	// This is why some refs decode properly and others don't
	if status == "Approved" || status == "Rejected" {
		// Some versions have tally data here
		// Try to peek ahead to see if we have tally data (3 u128 values)
		// If the next byte is 0 or 1 (Option indicator), it's the deposit
		// Otherwise, it might be tally data

		peekByte, err := decoder.ReadOneByte()
		if err != nil {
			// No more data, set defaults
			info.Submission.Who = "Unknown"
			info.Submission.Amount = "0"
			return info, nil
		}

		// Put the byte back by creating a new decoder with the remaining data
		remainingData := make([]byte, 1024) // Assuming enough buffer
		n := 0
		buf := make([]byte, 1)
		for {
			if err := decoder.Decode(&buf); err != nil {
				break
			}
			remainingData[n] = buf[0]
			n++
			if n >= len(remainingData)-1 {
				break
			}
		}
		newData := append([]byte{peekByte}, remainingData[:n]...)
		decoder = scale.NewDecoder(bytes.NewReader(newData))

		// If it's not 0 or 1, assume we have tally data
		if peekByte > 1 {
			// Decode tally (3 x U128)
			var ayes, nays, support types.U128
			// Put the byte back and decode as U128
			decoder = scale.NewDecoder(bytes.NewReader(newData))
			if err := decoder.Decode(&ayes); err == nil {
				decoder.Decode(&nays)
				decoder.Decode(&support)
				// We decoded tally, continue to deposit
			} else {
				// Failed to decode as tally, reset decoder
				decoder = scale.NewDecoder(bytes.NewReader(newData))
			}
		}
	}

	// Deposit - Option<Deposit<T::AccountId, BalanceOf<T, I>>>
	hasDeposit, err := decoder.ReadOneByte()
	if err != nil {
		// Some referenda might not have deposit info at all
		info.Submission.Who = "Unknown"
		info.Submission.Amount = "0"
		return info, nil
	}

	if hasDeposit == 1 {
		var who types.AccountID
		if err := decoder.Decode(&who); err != nil {
			return nil, fmt.Errorf("decode %s deposit who: %w", status, err)
		}
		info.Submission.Who = accountIDToSS58(who)

		var amount types.U128
		if err := decoder.Decode(&amount); err != nil {
			return nil, fmt.Errorf("decode %s deposit amount: %w", status, err)
		}
		info.Submission.Amount = amount.String()
	} else {
		info.Submission.Who = "Unknown"
		info.Submission.Amount = "0"
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

	switch variant {
	case 0: // Ongoing
		return decodeOngoingReferendum(decoder, refID)

	case 1: // Approved
		return decodeFinishedReferendum(decoder, "Approved")

	case 2: // Rejected
		return decodeFinishedReferendum(decoder, "Rejected")

	case 3: // Cancelled
		return decodeFinishedReferendum(decoder, "Cancelled")

	case 4: // TimedOut
		return decodeFinishedReferendum(decoder, "TimedOut")

	case 5: // Killed
		info := &ReferendumInfo{
			Status: "Killed",
			Track:  0,
		}
		// Since - BlockNumberFor<T, I>
		var since uint32
		if err := decoder.Decode(&since); err != nil {
			return nil, fmt.Errorf("decode killed since: %w", err)
		}
		info.KilledAt = since
		// Killed status doesn't have deposit info
		info.Submission.Who = "Unknown"
		info.Submission.Amount = "0"
		return info, nil

	default:
		return nil, fmt.Errorf("unknown referendum variant: %d", variant)
	}
}

// decodeOngoingReferendum decodes the complex Ongoing referendum state
func decodeOngoingReferendum(decoder *scale.Decoder, refID uint32) (*ReferendumInfo, error) {
	info := &ReferendumInfo{Status: "Ongoing"}

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
	if err := decodeProposal(decoder, info, refID); err != nil {
		return nil, fmt.Errorf("decode proposal: %w", err)
	}

	// Enactment - DispatchTime<BlockNumberFor<T, I>>
	if err := decodeEnactment(decoder, info); err != nil {
		return nil, fmt.Errorf("decode enactment: %w", err)
	}

	// Submitted - BlockNumberFor<T, I>
	var submitted uint32
	if err := decoder.Decode(&submitted); err != nil {
		return nil, fmt.Errorf("decode submitted: %w", err)
	}
	info.Submitted = submitted

	// SubmissionDeposit - Deposit<T::AccountId, BalanceOf<T, I>>
	// Decode directly into Submission since they have the same fields
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
	if err := decodeOptionDeposit(decoder, &info.DecisionDeposit); err != nil {
		return nil, fmt.Errorf("decode decision deposit: %w", err)
	}

	// Deciding - Option<DecidingStatus<BlockNumberFor<T, I>>>
	if err := decodeDeciding(decoder, &info.Decision); err != nil {
		return nil, fmt.Errorf("decode deciding: %w", err)
	}

	// Tally - T::Tally
	if err := decodeTally(decoder, &info.Tally); err != nil {
		return nil, fmt.Errorf("decode tally: %w", err)
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
}

// decodeProposal decodes the proposal field
func decodeProposal(decoder *scale.Decoder, info *ReferendumInfo, refID uint32) error {
	proposalType, err := decoder.ReadOneByte()
	if err != nil {
		return fmt.Errorf("read proposal type: %w", err)
	}

	switch proposalType {
	case 0: // Lookup
		var hash types.Hash
		if err := decoder.Decode(&hash); err != nil {
			return fmt.Errorf("decode lookup hash: %w", err)
		}
		var length types.U32
		if err := decoder.Decode(&length); err != nil {
			return fmt.Errorf("decode lookup length: %w", err)
		}
		info.Proposal = hash.Hex()
		info.ProposalLen = uint32(length)

	case 1: // Legacy
		var hash types.Hash
		if err := decoder.Decode(&hash); err != nil {
			return fmt.Errorf("decode legacy hash: %w", err)
		}
		info.Proposal = hash.Hex()
		info.ProposalLen = 0

	case 2: // Inline
		var length types.UCompact
		if err := decoder.Decode(&length); err != nil {
			return fmt.Errorf("decode inline length: %w", err)
		}
		callLen := uint32(length.Int64())
		callData := make([]byte, callLen)
		if err := decoder.Decode(&callData); err != nil {
			return fmt.Errorf("decode inline call: %w", err)
		}
		// Compute hash
		hash := Blake2_256(callData)
		info.Proposal = codec.HexEncodeToString(hash)
		info.ProposalLen = callLen

	default:
		// Handle other legacy proposal types
		log.Printf("Unknown proposal type %d for ref %d, attempting to skip", proposalType, refID)
		// Try to skip based on common patterns
		if proposalType >= 3 && proposalType <= 10 {
			skipBytes(decoder, 32) // Most legacy types have a hash
			if proposalType >= 11 && proposalType <= 14 {
				skipBytes(decoder, 4) // Some also have length
			}
		} else {
			skipBytes(decoder, 32) // Default skip
		}
		info.Proposal = ""
		info.ProposalLen = 0
	}

	return nil
}

// decodeEnactment decodes the enactment field
func decodeEnactment(decoder *scale.Decoder, info *ReferendumInfo) error {
	enactmentVariant, err := decoder.ReadOneByte()
	if err != nil {
		return fmt.Errorf("read enactment variant: %w", err)
	}

	var enactmentValue uint32
	if err := decoder.Decode(&enactmentValue); err != nil {
		return fmt.Errorf("decode enactment value: %w", err)
	}

	if enactmentVariant == 0 {
		info.Enactment = fmt.Sprintf("At(%d)", enactmentValue)
	} else {
		info.Enactment = fmt.Sprintf("After(%d)", enactmentValue)
	}

	return nil
}

// decodeDeposit decodes a deposit structure
func decodeDeposit(decoder *scale.Decoder, deposit *Deposit) error {
	var who types.AccountID
	if err := decoder.Decode(&who); err != nil {
		return fmt.Errorf("decode deposit who: %w", err)
	}
	deposit.Who = accountIDToSS58(who)

	var amount types.U128
	if err := decoder.Decode(&amount); err != nil {
		return fmt.Errorf("decode deposit amount: %w", err)
	}
	deposit.Amount = amount.String()

	return nil
}

// decodeOptionDeposit decodes an optional deposit
func decodeOptionDeposit(decoder *scale.Decoder, deposit **Deposit) error {
	hasDeposit, err := decoder.ReadOneByte()
	if err != nil {
		return fmt.Errorf("read deposit option: %w", err)
	}

	if hasDeposit == 1 {
		*deposit = &Deposit{}
		return decodeDeposit(decoder, *deposit)
	}

	return nil
}

// decodeDeciding decodes the deciding status
func decodeDeciding(decoder *scale.Decoder, decision **DecisionStatus) error {
	hasDeciding, err := decoder.ReadOneByte()
	if err != nil {
		return fmt.Errorf("read deciding option: %w", err)
	}

	if hasDeciding == 1 {
		*decision = &DecisionStatus{}
		var since uint32
		if err := decoder.Decode(&since); err != nil {
			return fmt.Errorf("decode deciding since: %w", err)
		}
		(*decision).Since = since

		// Confirming - Option<BlockNumberFor<T, I>>
		hasConfirming, err := decoder.ReadOneByte()
		if err != nil {
			return fmt.Errorf("read confirming option: %w", err)
		}

		if hasConfirming == 1 {
			var confirming uint32
			if err := decoder.Decode(&confirming); err != nil {
				return fmt.Errorf("decode confirming: %w", err)
			}
			(*decision).Confirming = &confirming
		}
	}

	return nil
}

// decodeTally decodes the tally information
func decodeTally(decoder *scale.Decoder, tally *Tally) error {
	var ayes, nays, support types.U128
	if err := decoder.Decode(&ayes); err != nil {
		return fmt.Errorf("decode ayes: %w", err)
	}
	if err := decoder.Decode(&nays); err != nil {
		return fmt.Errorf("decode nays: %w", err)
	}
	if err := decoder.Decode(&support); err != nil {
		return fmt.Errorf("decode support: %w", err)
	}

	tally.Ayes = ayes.String()
	tally.Nays = nays.String()
	tally.Support = support.String()

	// Calculate approval percentage
	totalVotes := new(big.Int).Add(ayes.Int, nays.Int)
	if totalVotes.Cmp(big.NewInt(0)) > 0 {
		approval := new(big.Int).Mul(ayes.Int, big.NewInt(10000))
		approval.Div(approval, totalVotes)
		tally.Approval = fmt.Sprintf("%d.%02d%%", approval.Int64()/100, approval.Int64()%100)
	}

	return nil
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
