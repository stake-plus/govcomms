package data

import (
	"context"
	"encoding/binary"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/stake-plus/polkadot-gov-comms/src/api/types"
	polkadot "github.com/stake-plus/polkadot-gov-comms/src/polkadot-go"
	"gorm.io/gorm"
)

// RunPolkadotIndexer indexes Polkadot referendum data
func RunPolkadotIndexer(ctx context.Context, db *gorm.DB, rpcURL string) {
	// Create client
	client, err := polkadot.NewClient(rpcURL)
	if err != nil {
		log.Printf("indexer polkadot: failed to create client: %v", err)
		return
	}
	defer client.Close()

	// Get current block
	header, err := client.GetHeader(nil)
	if err != nil {
		log.Printf("indexer polkadot: failed to get header: %v", err)
		return
	}
	currentBlock := header.Number
	log.Printf("indexer polkadot: current block %s", currentBlock)

	// Get all referendum keys that actually exist
	prefix := "0x" + polkadot.HexEncode(polkadot.Twox128([]byte("Referenda"))) + polkadot.HexEncode(polkadot.Twox128([]byte("ReferendumInfoFor")))
	keys, err := client.GetKeys(prefix, nil)
	if err != nil {
		log.Printf("indexer polkadot: failed to get keys: %v", err)
		return
	}
	log.Printf("indexer polkadot: found %d referendum keys", len(keys))

	// Extract referendum IDs from keys
	existingRefs := make(map[uint32]bool)
	prefixLen := len(prefix) - 2 // Remove "0x"
	for _, key := range keys {
		// Remove 0x prefix
		keyHex := strings.TrimPrefix(key, "0x")
		// The key format is: prefix + blake2_128(refID) + refID
		// We need to extract the refID from the end
		if len(keyHex) > prefixLen {
			// Get the part after the prefix
			remainder := keyHex[prefixLen:]
			// The remainder should be: blake2_128_hash(16 bytes = 32 hex chars) + refID(4 bytes = 8 hex chars)
			if len(remainder) >= 40 { // 32 + 8
				// Extract the last 8 hex characters (4 bytes) which is the refID
				refIDHex := remainder[len(remainder)-8:]
				// Convert hex to bytes
				refIDBytes, err := polkadot.DecodeHex(refIDHex)
				if err != nil || len(refIDBytes) != 4 {
					continue
				}
				// Convert to uint32 (little endian)
				refID := binary.LittleEndian.Uint32(refIDBytes)
				existingRefs[refID] = true
			}
		}
	}
	log.Printf("indexer polkadot: found %d existing referenda", len(existingRefs))

	// Convert map to sorted slice for ordered processing
	var refIDs []uint32
	for refID := range existingRefs {
		if refID <= 10000 { // Skip invalid IDs
			refIDs = append(refIDs, refID)
		}
	}
	sort.Slice(refIDs, func(i, j int) bool {
		return refIDs[i] < refIDs[j]
	})

	// Check what we have in database
	var dbCount int64
	db.Model(&types.Ref{}).Where("network_id = ?", 1).Count(&dbCount)
	log.Printf("indexer polkadot: database has %d proposals", dbCount)

	created := 0
	updated := 0
	errors := 0
	processed := 0

	// Process each existing referendum in order
	for _, refID := range refIDs {
		select {
		case <-ctx.Done():
			return
		default:
		}

		processed++

		// Get referendum info - the client handles historical lookups
		info, err := client.GetReferendumInfo(refID)
		var ref types.Ref
		dbErr := db.Where("network_id = ? AND ref_id = ?", 1, refID).First(&ref).Error

		if err != nil {
			// Only log errors for refs we expect to work
			if !strings.Contains(err.Error(), "does not exist") {
				log.Printf("indexer polkadot: failed to get info for ref %d: %v", refID, err)
			}
			if dbErr == gorm.ErrRecordNotFound {
				// Create with minimal info for cleared refs
				ref = types.Ref{
					RefID:     uint64(refID),
					NetworkID: 1,
					Status:    "Cleared",
					Submitter: "Unknown",
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				}
				if err := db.Create(&ref).Error; err != nil {
					log.Printf("indexer polkadot: failed to create ref %d: %v", refID, err)
					errors++
				} else {
					created++
				}
			}
			continue
		}

		// For finished referenda, always try to fetch historical data
		if info.Status != "Ongoing" && (info.Submission.Who == "Unknown" || info.Origin == "") {
			historicalInfo := fetchHistoricalReferendumInfo(client, refID, info)
			if historicalInfo != nil {
				info = historicalInfo
			}
		}

		// We have referendum info
		if dbErr == gorm.ErrRecordNotFound {
			// Create new
			ref = types.Ref{
				NetworkID:    1,
				RefID:        uint64(refID),
				Status:       info.Status,
				TrackID:      info.Track,
				Origin:       info.Origin,
				Enactment:    info.Enactment,
				Submitted:    uint64(info.Submitted),
				Approved:     info.Status == "Approved",
				CreatedAt:    time.Now(),
				UpdatedAt:    time.Now(),
				PreimageHash: info.Proposal,
				PreimageLen:  info.ProposalLen,
			}

			// Set submitter info
			if info.Submission.Who != "" {
				ref.Submitter = info.Submission.Who
				ref.SubmissionDepositWho = info.Submission.Who
				ref.SubmissionDepositAmount = info.Submission.Amount
			} else {
				ref.Submitter = "Unknown"
			}

			// Set decision deposit if available
			if info.DecisionDeposit != nil {
				ref.DecisionDepositWho = info.DecisionDeposit.Who
				ref.DecisionDepositAmount = info.DecisionDeposit.Amount
			}

			// Set tally info for ongoing referenda
			if info.Status == "Ongoing" && info.Tally.Ayes != "" {
				ref.Ayes = info.Tally.Ayes
				ref.Nays = info.Tally.Nays
				ref.Support = info.Tally.Support
			}

			// Set decision timing if available
			if info.Decision != nil {
				ref.DecisionStart = uint64(info.Decision.Since)
				if info.Decision.Confirming != nil {
					ref.ConfirmStart = uint64(*info.Decision.Confirming)
				}
			}

			// Set end times for finished referenda
			if info.ApprovedAt > 0 {
				ref.DecisionEnd = uint64(info.ApprovedAt)
			} else if info.RejectedAt > 0 {
				ref.DecisionEnd = uint64(info.RejectedAt)
			} else if info.CancelledAt > 0 {
				ref.DecisionEnd = uint64(info.CancelledAt)
			} else if info.TimedOutAt > 0 {
				ref.DecisionEnd = uint64(info.TimedOutAt)
			} else if info.KilledAt > 0 {
				ref.DecisionEnd = uint64(info.KilledAt)
			}

			if err := db.Create(&ref).Error; err != nil {
				log.Printf("indexer polkadot: failed to create ref %d: %v", refID, err)
				errors++
			} else {
				created++
				// Create proponents
				proponents := []types.RefProponent{}

				// Add submitter
				if info.Submission.Who != "" && info.Submission.Who != "Unknown" {
					proponents = append(proponents, types.RefProponent{
						RefID:   ref.ID,
						Address: info.Submission.Who,
						Role:    "submitter",
						Active:  1,
					})
				}

				// Add decision deposit provider if different
				if info.DecisionDeposit != nil && info.DecisionDeposit.Who != info.Submission.Who {
					proponents = append(proponents, types.RefProponent{
						RefID:   ref.ID,
						Address: info.DecisionDeposit.Who,
						Role:    "decision_deposit",
						Active:  1,
					})
				}

				// Create proponents
				for _, p := range proponents {
					if err := db.Create(&p).Error; err != nil {
						log.Printf("indexer polkadot: failed to create proponent: %v", err)
					}
				}

				// Add this after creating the proposal in the database
				if info.Proposal != "" && info.ProposalLen > 0 && created > 0 {
					// Decode preimage to extract participants
					preimageDecoder := polkadot.NewPreimageDecoder(client)
					addresses, err := preimageDecoder.FetchAndDecodePreimage(
						info.Proposal,
						info.ProposalLen,
						uint32(ref.Submitted),
					)
					if err != nil {
						log.Printf("Failed to decode preimage for ref %d: %v", refID, err)
					} else {
						// Add all addresses as participants
						for _, addr := range addresses {
							if addr != "" && addr != ref.Submitter {
								proponent := types.RefProponent{
									RefID:   ref.ID,
									Address: addr,
									Role:    "recipient",
									Active:  1,
								}
								if err := db.Create(&proponent).Error; err != nil {
									log.Printf("Failed to create recipient proponent: %v", err)
								}
							}
						}
						log.Printf("Added %d recipients from preimage for ref %d", len(addresses), refID)
					}
				}
			}
		} else if dbErr == nil {
			// Update existing if changed
			changed := false
			if ref.Status != info.Status && info.Status != "" {
				log.Printf("indexer polkadot: updating ref %d status from %s to %s", refID, ref.Status, info.Status)
				ref.Status = info.Status
				changed = true
			}
			if info.Track > 0 && ref.TrackID != info.Track {
				ref.TrackID = info.Track
				changed = true
			}
			if info.Origin != "" && ref.Origin != info.Origin {
				ref.Origin = info.Origin
				changed = true
			}
			if info.Enactment != "" && ref.Enactment != info.Enactment {
				ref.Enactment = info.Enactment
				changed = true
			}
			if info.Status == "Approved" && !ref.Approved {
				ref.Approved = true
				changed = true
			}

			// Update tally info for ongoing referenda
			if info.Status == "Ongoing" && info.Tally.Ayes != "" {
				if ref.Ayes != info.Tally.Ayes {
					ref.Ayes = info.Tally.Ayes
					changed = true
				}
				if ref.Nays != info.Tally.Nays {
					ref.Nays = info.Tally.Nays
					changed = true
				}
				if ref.Support != info.Tally.Support {
					ref.Support = info.Tally.Support
					changed = true
				}
			}

			// Update submitter if we now have it
			if info.Submission.Who != "" && info.Submission.Who != "Unknown" && (ref.Submitter == "Unknown" || ref.Submitter == "") {
				ref.Submitter = info.Submission.Who
				ref.SubmissionDepositWho = info.Submission.Who
				ref.SubmissionDepositAmount = info.Submission.Amount
				changed = true
			}

			// Update decision deposit if we now have it
			if info.DecisionDeposit != nil && ref.DecisionDepositWho == "" {
				ref.DecisionDepositWho = info.DecisionDeposit.Who
				ref.DecisionDepositAmount = info.DecisionDeposit.Amount
				changed = true
			}

			// Update preimage info
			if info.Proposal != "" && ref.PreimageHash == "" {
				ref.PreimageHash = info.Proposal
				ref.PreimageLen = info.ProposalLen
				changed = true
			}

			if info.Submitted > 0 && ref.Submitted == 0 {
				ref.Submitted = uint64(info.Submitted)
				changed = true
			}

			// Update decision timing
			if info.Decision != nil && ref.DecisionStart == 0 {
				ref.DecisionStart = uint64(info.Decision.Since)
				if info.Decision.Confirming != nil {
					ref.ConfirmStart = uint64(*info.Decision.Confirming)
				}
				changed = true
			}

			// Update end times
			if info.ApprovedAt > 0 && ref.DecisionEnd == 0 {
				ref.DecisionEnd = uint64(info.ApprovedAt)
				changed = true
			} else if info.RejectedAt > 0 && ref.DecisionEnd == 0 {
				ref.DecisionEnd = uint64(info.RejectedAt)
				changed = true
			} else if info.CancelledAt > 0 && ref.DecisionEnd == 0 {
				ref.DecisionEnd = uint64(info.CancelledAt)
				changed = true
			} else if info.TimedOutAt > 0 && ref.DecisionEnd == 0 {
				ref.DecisionEnd = uint64(info.TimedOutAt)
				changed = true
			} else if info.KilledAt > 0 && ref.DecisionEnd == 0 {
				ref.DecisionEnd = uint64(info.KilledAt)
				changed = true
			}

			if changed {
				ref.UpdatedAt = time.Now()
				if err := db.Save(&ref).Error; err != nil {
					log.Printf("indexer polkadot: failed to update ref %d: %v", refID, err)
					errors++
				} else {
					updated++
				}
			}
		}

		// Progress logging
		if processed%100 == 0 {
			log.Printf("indexer polkadot: processed %d referenda (created: %d, updated: %d, errors: %d)",
				processed, created, updated, errors)
		}
	}

	log.Printf("indexer polkadot: sync complete - processed %d, created %d, updated %d, errors %d refs",
		processed, created, updated, errors)
}

// fetchHistoricalReferendumInfo tries to get referendum info from when it was ongoing
func fetchHistoricalReferendumInfo(client *polkadot.Client, refID uint32, currentInfo *polkadot.ReferendumInfo) *polkadot.ReferendumInfo {
	// For all finished states, we want to look back to when they were Ongoing
	var searchBlocks []uint64

	// Determine which blocks to search based on the current status
	var endBlock uint32
	switch currentInfo.Status {
	case "Approved":
		endBlock = currentInfo.ApprovedAt
	case "Rejected":
		endBlock = currentInfo.RejectedAt
	case "Cancelled":
		endBlock = currentInfo.CancelledAt
	case "TimedOut":
		endBlock = currentInfo.TimedOutAt
	case "Killed":
		endBlock = currentInfo.KilledAt
	default:
		return nil
	}

	if endBlock == 0 {
		return nil
	}

	// Search backwards from the end block
	// Try different intervals to find when it was Ongoing
	intervals := []uint64{1, 10, 100, 1000, 10000}
	for _, interval := range intervals {
		if uint64(endBlock) > interval {
			searchBlocks = append(searchBlocks, uint64(endBlock)-interval)
		}
	}

	// Try each block
	for _, targetBlock := range searchBlocks {
		// Get block hash for the target block
		blockHash, err := client.GetBlockHash(&targetBlock)
		if err != nil {
			continue
		}

		// Get referendum info at that block
		historicalInfo, err := client.GetReferendumInfoAt(refID, blockHash)
		if err != nil {
			continue
		}

		// If we found good data (Ongoing status with submitter info), use it
		if historicalInfo != nil && historicalInfo.Status == "Ongoing" &&
			historicalInfo.Submission.Who != "Unknown" && historicalInfo.Submission.Who != "" {
			// Copy over the important fields but keep the current status
			currentInfo.Track = historicalInfo.Track
			currentInfo.Origin = historicalInfo.Origin
			currentInfo.Proposal = historicalInfo.Proposal
			currentInfo.ProposalLen = historicalInfo.ProposalLen
			currentInfo.Enactment = historicalInfo.Enactment
			currentInfo.Submitted = historicalInfo.Submitted
			currentInfo.Submission = historicalInfo.Submission
			currentInfo.DecisionDeposit = historicalInfo.DecisionDeposit
			return currentInfo
		}
	}

	return nil
}

// IndexerService runs the indexer periodically
func IndexerService(ctx context.Context, db *gorm.DB, rpcURL string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Run once immediately
	RunPolkadotIndexer(ctx, db, rpcURL)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			RunPolkadotIndexer(ctx, db, rpcURL)
		}
	}
}
