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

// accountIDToSS58 converts an AccountID to SS58 format using chain's prefix
func (c *Client) accountIDToSS58(accountID types.AccountID) string {
	prefix := c.GetCachedSS58Prefix()

	// Create the payload: prefix + accountID + checksum
	payload := make([]byte, 0, 35)

	// For prefix > 63, we need to encode it differently
	if prefix < 64 {
		payload = append(payload, byte(prefix))
	} else {
		// For larger prefixes, use the extended format
		payload = append(payload, 0x40|((byte(prefix>>8))&0x3f))
		payload = append(payload, byte(prefix&0xff))
	}
	payload = append(payload, accountID[:]...)

	// Calculate SS58 checksum
	checksumInput := []byte("SS58PRE")
	// Add the correct prefix bytes for checksum
	if prefix < 64 {
		checksumInput = append(checksumInput, byte(prefix))
	} else {
		checksumInput = append(checksumInput, 0x40|((byte(prefix>>8))&0x3f))
		checksumInput = append(checksumInput, byte(prefix&0xff))
	}
	checksumInput = append(checksumInput, accountID[:]...)

	checksum := Blake2_256(checksumInput)

	// Append first 2 bytes of checksum
	payload = append(payload, checksum[0:2]...)

	// Base58 encode
	return base58.Encode(payload)
}

// Helper function for backwards compatibility
func accountIDToSS58(accountID types.AccountID) string {
	// Use generic substrate prefix for backwards compatibility
	return accountIDToSS58WithPrefix(accountID, 42)
}

// accountIDToSS58WithPrefix converts with specific prefix
func accountIDToSS58WithPrefix(accountID types.AccountID, prefix uint16) string {
	// Create the payload: prefix + accountID + checksum
	payload := make([]byte, 0, 35)

	// For prefix > 63, we need to encode it differently
	if prefix < 64 {
		payload = append(payload, byte(prefix))
	} else {
		// For larger prefixes, use the extended format
		payload = append(payload, 0x40|((byte(prefix>>8))&0x3f))
		payload = append(payload, byte(prefix&0xff))
	}
	payload = append(payload, accountID[:]...)

	// Calculate SS58 checksum
	checksumInput := []byte("SS58PRE")
	// Add the correct prefix bytes for checksum
	if prefix < 64 {
		checksumInput = append(checksumInput, byte(prefix))
	} else {
		checksumInput = append(checksumInput, 0x40|((byte(prefix>>8))&0x3f))
		checksumInput = append(checksumInput, byte(prefix&0xff))
	}
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

	// Deposit - Option<Deposit<T::AccountId, BalanceOf<T, I>>>
	hasDeposit, err := decoder.ReadOneByte()
	if err != nil {
		// No more data, set defaults
		info.Submission.Who = "Unknown"
		info.Submission.Amount = "0"
		return info, nil
	}

	if hasDeposit == 1 {
		var who types.AccountID
		if err := decoder.Decode(&who); err != nil {
			// If we can't decode the account, set defaults
			info.Submission.Who = "Unknown"
			info.Submission.Amount = "0"
			return info, nil
		}
		info.Submission.Who = accountIDToSS58(who)

		var amount types.U128
		if err := decoder.Decode(&amount); err != nil {
			// If we can't decode the amount, at least we have the account
			info.Submission.Amount = "0"
		} else {
			info.Submission.Amount = amount.String()
		}
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
		// Skip alarm data - read and discard the bytes
		// Block number (4 bytes)
		blockNumBytes := make([]byte, 4)
		if err := decoder.Read(blockNumBytes); err != nil {
			// If we can't read, just continue
			return info, nil
		}

		// Schedule address is complex, try to skip a reasonable amount
		// Usually contains task name (32 bytes) + maybe_id (Option<Vec<u8>>)
		skipData := make([]byte, 32)
		decoder.Read(skipData) // Ignore error, we're just trying to skip

		// Try to read option byte for maybe_id
		optionByte, err := decoder.ReadOneByte()
		if err == nil && optionByte == 1 {
			// Has maybe_id, try to decode compact length
			compactBytes := make([]byte, 5) // Max compact encoding
			decoder.Read(compactBytes)      // Ignore error
			// Would need to decode compact and then skip that many bytes
			// but for safety, just continue
		}
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

		// UCompact stores the value as bytes, we need to extract it
		// This is a workaround - ideally we'd have a method to get the uint64 value
		callLen := uint32(0)
		// Read the actual call data length from the stream
		// Since we can't easily convert UCompact, let's read it as a U32
		if err := decoder.Decode(&callLen); err != nil {
			// If that fails, try a default length
			callLen = 1024
		}

		callData := make([]byte, callLen)
		if err := decoder.Read(callData); err != nil {
			return fmt.Errorf("decode inline call: %w", err)
		}

		// Compute hash
		hash := Blake2_256(callData)
		info.Proposal = codec.HexEncodeToString(hash)
		info.ProposalLen = callLen

	case 3, 4, 5, 6, 7: // Various legacy formats with just hash
		var hash types.Hash
		if err := decoder.Decode(&hash); err != nil {
			return fmt.Errorf("decode type %d hash: %w", proposalType, err)
		}
		info.Proposal = hash.Hex()
		info.ProposalLen = 0

	case 8, 9, 10: // Legacy format with hash only (Democracy era)
		var hash types.Hash
		if err := decoder.Decode(&hash); err != nil {
			return fmt.Errorf("decode democracy hash: %w", err)
		}
		info.Proposal = hash.Hex()
		info.ProposalLen = 0

	case 11, 12: // Democracy era with hash and maybe length
		var hash types.Hash
		if err := decoder.Decode(&hash); err != nil {
			return fmt.Errorf("decode democracy v2 hash: %w", err)
		}
		info.Proposal = hash.Hex()

		// Some versions include length
		if proposalType == 12 {
			var length types.U32
			if err := decoder.Decode(&length); err == nil {
				info.ProposalLen = uint32(length)
			} else {
				info.ProposalLen = 0
			}
		} else {
			info.ProposalLen = 0
		}

	case 13, 14: // Transition era formats
		var hash types.Hash
		if err := decoder.Decode(&hash); err != nil {
			return fmt.Errorf("decode transition hash: %w", err)
		}
		info.Proposal = hash.Hex()

		// Try to decode length, but don't fail if it's not there
		var length types.U32
		if err := decoder.Decode(&length); err == nil {
			info.ProposalLen = uint32(length)
		} else {
			info.ProposalLen = 0
		}

	default:
		// For completely unknown types, try common patterns
		log.Printf("Attempting to decode unknown proposal type %d for ref %d", proposalType, refID)

		// Most proposal types have a hash as the first field
		var hash types.Hash
		if err := decoder.Decode(&hash); err != nil {
			// If we can't decode as hash, skip 32 bytes
			skipData := make([]byte, 32)
			decoder.Read(skipData)
			info.Proposal = ""
			info.ProposalLen = 0
		} else {
			info.Proposal = hash.Hex()

			// Try to decode an optional length
			var length types.U32
			if err := decoder.Decode(&length); err == nil && uint32(length) < 1000000 {
				info.ProposalLen = uint32(length)
			} else {
				info.ProposalLen = 0
			}
		}
	}

	// Validate that we got something
	if info.Proposal == "" {
		log.Printf("Warning: No proposal hash extracted for ref %d (type %d)", refID, proposalType)
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
	// Try to decode ayes
	var ayes types.U128
	if err := decoder.Decode(&ayes); err != nil {
		// If we can't decode, set defaults and return
		log.Printf("Warning: Failed to decode tally ayes: %v", err)
		tally.Ayes = "0"
		tally.Nays = "0"
		tally.Support = "0"
		tally.Approval = "0%"
		return nil
	}
	tally.Ayes = ayes.String()

	// Try to decode nays
	var nays types.U128
	if err := decoder.Decode(&nays); err != nil {
		// Partial decode - set remaining to defaults
		log.Printf("Warning: Failed to decode tally nays: %v", err)
		tally.Nays = "0"
		tally.Support = "0"
		tally.Approval = "0%"
		return nil
	}
	tally.Nays = nays.String()

	// Try to decode support - this might be missing in some referenda
	var support types.U128
	if err := decoder.Decode(&support); err != nil {
		tally.Support = "0"

		// Still calculate approval if we have ayes and nays
		totalVotes := new(big.Int).Add(ayes.Int, nays.Int)
		if totalVotes.Cmp(big.NewInt(0)) > 0 {
			approval := new(big.Int).Mul(ayes.Int, big.NewInt(10000))
			approval.Div(approval, totalVotes)
			tally.Approval = fmt.Sprintf("%d.%02d%%", approval.Int64()/100, approval.Int64()%100)
		} else {
			tally.Approval = "0%"
		}
		return nil
	}
	tally.Support = support.String()

	// Calculate approval percentage
	totalVotes := new(big.Int).Add(ayes.Int, nays.Int)
	if totalVotes.Cmp(big.NewInt(0)) > 0 {
		approval := new(big.Int).Mul(ayes.Int, big.NewInt(10000))
		approval.Div(approval, totalVotes)
		tally.Approval = fmt.Sprintf("%d.%02d%%", approval.Int64()/100, approval.Int64()%100)
	} else {
		tally.Approval = "0%"
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
