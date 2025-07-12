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
	db.Model(&types.Proposal{}).Where("network_id = ?", 1).Count(&dbCount)
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
		var proposal types.Proposal
		dbErr := db.Where("network_id = ? AND ref_id = ?", 1, refID).First(&proposal).Error

		if err != nil {
			// Only log errors for refs we expect to work
			if !strings.Contains(err.Error(), "does not exist") {
				log.Printf("indexer polkadot: failed to get info for ref %d: %v", refID, err)
			}
			if dbErr == gorm.ErrRecordNotFound {
				// Create with minimal info for cleared refs
				proposal = types.Proposal{
					NetworkID: 1,
					RefID:     uint64(refID),
					Status:    "Cleared",
					Submitter: "Unknown",
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				}
				if err := db.Create(&proposal).Error; err != nil {
					log.Printf("indexer polkadot: failed to create proposal %d: %v", refID, err)
					errors++
				} else {
					created++
				}
			}
			continue
		}

		// For finished referenda, always try to fetch historical data
		if info.Status != "Ongoing" && (info.Submission.Who == "Unknown" || info.Origin == "") {
			log.Printf("indexer polkadot: ref %d is %s but missing data, fetching historical", refID, info.Status)
			historicalInfo := fetchHistoricalReferendumInfo(client, refID, info)
			if historicalInfo != nil {
				info = historicalInfo
			}
		}

		// We have referendum info
		if dbErr == gorm.ErrRecordNotFound {
			// Create new
			proposal = types.Proposal{
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
				proposal.Submitter = info.Submission.Who
				proposal.SubmissionDepositWho = info.Submission.Who
				proposal.SubmissionDepositAmount = info.Submission.Amount
			} else {
				proposal.Submitter = "Unknown"
			}

			// Set decision deposit if available
			if info.DecisionDeposit != nil {
				proposal.DecisionDepositWho = info.DecisionDeposit.Who
				proposal.DecisionDepositAmount = info.DecisionDeposit.Amount
			}

			// Set tally info for ongoing referenda
			if info.Status == "Ongoing" && info.Tally.Ayes != "" {
				proposal.TallyAyes = info.Tally.Ayes
				proposal.TallyNays = info.Tally.Nays
			}

			// Set decision timing if available
			if info.Decision != nil {
				proposal.DecisionStart = uint64(info.Decision.Since)
				if info.Decision.Confirming != nil {
					proposal.ConfirmStart = uint64(*info.Decision.Confirming)
				}
			}

			// Set end times for finished referenda
			if info.ApprovedAt > 0 {
				proposal.DecisionEnd = uint64(info.ApprovedAt)
			} else if info.RejectedAt > 0 {
				proposal.DecisionEnd = uint64(info.RejectedAt)
			} else if info.CancelledAt > 0 {
				proposal.DecisionEnd = uint64(info.CancelledAt)
			} else if info.TimedOutAt > 0 {
				proposal.DecisionEnd = uint64(info.TimedOutAt)
			} else if info.KilledAt > 0 {
				proposal.DecisionEnd = uint64(info.KilledAt)
			}

			if err := db.Create(&proposal).Error; err != nil {
				log.Printf("indexer polkadot: failed to create proposal %d: %v", refID, err)
				errors++
			} else {
				created++
				log.Printf("indexer polkadot: created ref %d with status %s on track %d origin %s", refID, info.Status, info.Track, info.Origin)

				// Create proposal participants
				participants := []types.ProposalParticipant{}

				// Add submitter
				if info.Submission.Who != "" && info.Submission.Who != "Unknown" {
					participants = append(participants, types.ProposalParticipant{
						ProposalID: proposal.ID,
						Address:    info.Submission.Who,
						Role:       "submitter",
					})
				}

				// Add decision deposit provider if different
				if info.DecisionDeposit != nil && info.DecisionDeposit.Who != info.Submission.Who {
					participants = append(participants, types.ProposalParticipant{
						ProposalID: proposal.ID,
						Address:    info.DecisionDeposit.Who,
						Role:       "decision_deposit",
					})
				}

				// Create participants
				for _, p := range participants {
					if err := db.Create(&p).Error; err != nil {
						log.Printf("indexer polkadot: failed to create participant: %v", err)
					}
				}

				// Store preimage info if available
				if info.Proposal != "" && info.ProposalLen > 0 {
					preimage := types.Preimage{
						Hash:      info.Proposal,
						Length:    info.ProposalLen,
						CreatedAt: time.Now(),
					}
					if err := db.FirstOrCreate(&preimage, types.Preimage{Hash: info.Proposal}).Error; err != nil {
						log.Printf("indexer polkadot: failed to store preimage: %v", err)
					}
				}
			}
		} else if dbErr == nil {
			// Update existing if changed
			changed := false

			if proposal.Status != info.Status && info.Status != "" {
				log.Printf("indexer polkadot: updating ref %d status from %s to %s", refID, proposal.Status, info.Status)
				proposal.Status = info.Status
				changed = true
			}

			if info.Track > 0 && proposal.TrackID != info.Track {
				proposal.TrackID = info.Track
				changed = true
			}

			if info.Origin != "" && proposal.Origin != info.Origin {
				proposal.Origin = info.Origin
				changed = true
			}

			if info.Enactment != "" && proposal.Enactment != info.Enactment {
				proposal.Enactment = info.Enactment
				changed = true
			}

			if info.Status == "Approved" && !proposal.Approved {
				proposal.Approved = true
				changed = true
			}

			// Update tally info for ongoing referenda
			if info.Status == "Ongoing" && info.Tally.Ayes != "" {
				if proposal.TallyAyes != info.Tally.Ayes {
					proposal.TallyAyes = info.Tally.Ayes
					changed = true
				}
				if proposal.TallyNays != info.Tally.Nays {
					proposal.TallyNays = info.Tally.Nays
					changed = true
				}
			}

			// Update submitter if we now have it
			if info.Submission.Who != "" && info.Submission.Who != "Unknown" && (proposal.Submitter == "Unknown" || proposal.Submitter == "") {
				proposal.Submitter = info.Submission.Who
				proposal.SubmissionDepositWho = info.Submission.Who
				proposal.SubmissionDepositAmount = info.Submission.Amount
				changed = true
			}

			// Update decision deposit if we now have it
			if info.DecisionDeposit != nil && proposal.DecisionDepositWho == "" {
				proposal.DecisionDepositWho = info.DecisionDeposit.Who
				proposal.DecisionDepositAmount = info.DecisionDeposit.Amount
				changed = true
			}

			// Update preimage info
			if info.Proposal != "" && proposal.PreimageHash == "" {
				proposal.PreimageHash = info.Proposal
				proposal.PreimageLen = info.ProposalLen
				changed = true
			}

			if info.Submitted > 0 && proposal.Submitted == 0 {
				proposal.Submitted = uint64(info.Submitted)
				changed = true
			}

			// Update decision timing
			if info.Decision != nil && proposal.DecisionStart == 0 {
				proposal.DecisionStart = uint64(info.Decision.Since)
				if info.Decision.Confirming != nil {
					proposal.ConfirmStart = uint64(*info.Decision.Confirming)
				}
				changed = true
			}

			// Update end times
			if info.ApprovedAt > 0 && proposal.DecisionEnd == 0 {
				proposal.DecisionEnd = uint64(info.ApprovedAt)
				changed = true
			} else if info.RejectedAt > 0 && proposal.DecisionEnd == 0 {
				proposal.DecisionEnd = uint64(info.RejectedAt)
				changed = true
			} else if info.CancelledAt > 0 && proposal.DecisionEnd == 0 {
				proposal.DecisionEnd = uint64(info.CancelledAt)
				changed = true
			} else if info.TimedOutAt > 0 && proposal.DecisionEnd == 0 {
				proposal.DecisionEnd = uint64(info.TimedOutAt)
				changed = true
			} else if info.KilledAt > 0 && proposal.DecisionEnd == 0 {
				proposal.DecisionEnd = uint64(info.KilledAt)
				changed = true
			}

			if changed {
				proposal.UpdatedAt = time.Now()
				if err := db.Save(&proposal).Error; err != nil {
					log.Printf("indexer polkadot: failed to update proposal %d: %v", refID, err)
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

		// Small delay to not hammer the node
		time.Sleep(50 * time.Millisecond)
	}

	// Index track information
	indexTracks(db, client)

	log.Printf("indexer polkadot: sync complete - processed %d, created %d, updated %d, errors %d proposals",
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

// indexTracks indexes track configuration
func indexTracks(db *gorm.DB, client *polkadot.Client) {
	// Common tracks for Polkadot
	tracks := []types.Track{
		{ID: 0, NetworkID: 1, Name: "Root"},
		{ID: 1, NetworkID: 1, Name: "WhitelistedCaller"},
		{ID: 10, NetworkID: 1, Name: "StakingAdmin"},
		{ID: 11, NetworkID: 1, Name: "Treasurer"},
		{ID: 12, NetworkID: 1, Name: "LeaseAdmin"},
		{ID: 13, NetworkID: 1, Name: "FellowshipAdmin"},
		{ID: 14, NetworkID: 1, Name: "GeneralAdmin"},
		{ID: 15, NetworkID: 1, Name: "AuctionAdmin"},
		{ID: 20, NetworkID: 1, Name: "ReferendumCanceller"},
		{ID: 21, NetworkID: 1, Name: "ReferendumKiller"},
		{ID: 30, NetworkID: 1, Name: "SmallTipper"},
		{ID: 31, NetworkID: 1, Name: "BigTipper"},
		{ID: 32, NetworkID: 1, Name: "SmallSpender"},
		{ID: 33, NetworkID: 1, Name: "MediumSpender"},
		{ID: 34, NetworkID: 1, Name: "BigSpender"},
		{ID: 1000, NetworkID: 1, Name: "WishForChange"},
	}

	for _, track := range tracks {
		// Try to get detailed track info from chain
		if info, err := client.GetTrackInfo(track.ID); err == nil {
			track.MaxDeciding = info.MaxDeciding
			track.DecisionDeposit = info.DecisionDeposit
			track.PreparePeriod = info.PreparePeriod
			track.DecisionPeriod = info.DecisionPeriod
			track.ConfirmPeriod = info.ConfirmPeriod
			track.MinEnactmentPeriod = info.MinEnactmentPeriod
			track.MinApproval = info.MinApproval
			track.MinSupport = info.MinSupport
		}

		if err := db.FirstOrCreate(&track, types.Track{ID: track.ID, NetworkID: track.NetworkID}).Error; err != nil {
			log.Printf("indexer polkadot: failed to create track %d: %v", track.ID, err)
		}
	}
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
