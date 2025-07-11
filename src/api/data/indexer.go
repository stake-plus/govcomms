package data

import (
	"context"
	"log"
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

	currentBlock, err := polkadot.DecodeBlockNumber(header.Number)
	if err != nil {
		log.Printf("indexer polkadot: failed to decode block number: %v", err)
		return
	}

	log.Printf("indexer polkadot: current block %d", currentBlock)

	// Get referendum count
	refCountKey := polkadot.StorageKey("Referenda", "ReferendumCount")
	countHex, err := client.GetStorage(refCountKey, nil)
	if err != nil {
		log.Printf("indexer polkadot: failed to get referendum count: %v", err)
		return
	}

	count, err := polkadot.DecodeU32(countHex)
	if err != nil {
		log.Printf("indexer polkadot: failed to decode referendum count: %v", err)
		return
	}

	log.Printf("indexer polkadot: chain reports %d referenda", count)

	// Check what we have in database
	var dbCount int64
	db.Model(&types.Proposal{}).Where("network_id = ?", 1).Count(&dbCount)
	log.Printf("indexer polkadot: database has %d proposals", dbCount)

	// Get the highest ref_id we have in database to continue from there
	var lastRefID uint64
	db.Model(&types.Proposal{}).Where("network_id = ?", 1).Select("COALESCE(MAX(ref_id), 0)").Scan(&lastRefID)

	startFrom := uint32(0)
	if lastRefID > 0 {
		startFrom = uint32(lastRefID + 1)
	}

	created := 0
	updated := 0
	errors := 0

	// Process each referendum
	for i := startFrom; i < count; i++ {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Get referendum info
		refKey := polkadot.StorageKeyUint32("Referenda", "ReferendumInfoFor", i)

		// First check current storage
		refHex, err := client.GetStorage(refKey, nil)
		if err != nil {
			log.Printf("indexer polkadot: error fetching ref %d: %v", i, err)
			errors++
			continue
		}

		// Check if data exists
		hasData := refHex != "" && refHex != "0x" && refHex != "null"

		// Try to get storage size to verify it exists
		if !hasData {
			size, _ := client.GetStorageSize(refKey, nil)
			hasData = size > 0
		}

		var proposal types.Proposal
		dbErr := db.Where("network_id = ? AND ref_id = ?", 1, i).First(&proposal).Error

		if !hasData {
			// No data in current storage
			if dbErr == gorm.ErrRecordNotFound {
				// Create with minimal info
				proposal = types.Proposal{
					NetworkID: 1,
					RefID:     uint64(i),
					Status:    "Cleared",
					Submitter: "Unknown",
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				}
				if err := db.Create(&proposal).Error; err != nil {
					log.Printf("indexer polkadot: failed to create proposal %d: %v", i, err)
					errors++
				} else {
					created++
				}
			}
			continue
		}

		// We have data, try to decode it
		data, err := polkadot.DecodeHex(refHex)
		if err != nil || len(data) == 0 {
			log.Printf("indexer polkadot: failed to decode ref %d: %v", i, err)
			errors++
			continue
		}

		// Basic parsing - just get status from first byte
		status := "Unknown"
		track := uint16(0)

		if len(data) > 0 {
			variant := data[0]
			switch variant {
			case 0:
				status = "Ongoing"
				// Try to get track (next 2 bytes)
				if len(data) > 2 {
					track = uint16(data[1]) | uint16(data[2])<<8
				}
			case 1:
				status = "Approved"
			case 2:
				status = "Rejected"
			case 3:
				status = "Cancelled"
			case 4:
				status = "TimedOut"
			case 5:
				status = "Killed"
			default:
				log.Printf("indexer polkadot: unknown status variant %d for ref %d", variant, i)
			}
		}

		if dbErr == gorm.ErrRecordNotFound {
			// Create new
			proposal = types.Proposal{
				NetworkID: 1,
				RefID:     uint64(i),
				Status:    status,
				TrackID:   track,
				Submitter: "Unknown",
				Approved:  status == "Approved",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			}

			if err := db.Create(&proposal).Error; err != nil {
				log.Printf("indexer polkadot: failed to create proposal %d: %v", i, err)
				errors++
			} else {
				created++
				if status == "Ongoing" {
					log.Printf("indexer polkadot: created ongoing ref %d on track %d", i, track)
				}
			}
		} else if dbErr == nil {
			// Update existing if changed
			changed := false

			if proposal.Status != status && status != "" && status != "Unknown" {
				log.Printf("indexer polkadot: updating ref %d status from %s to %s", i, proposal.Status, status)
				proposal.Status = status
				changed = true
			}

			if track > 0 && proposal.TrackID != track {
				proposal.TrackID = track
				changed = true
			}

			if status == "Approved" && !proposal.Approved {
				proposal.Approved = true
				changed = true
			}

			if changed {
				proposal.UpdatedAt = time.Now()
				if err := db.Save(&proposal).Error; err != nil {
					log.Printf("indexer polkadot: failed to update proposal %d: %v", i, err)
					errors++
				} else {
					updated++
				}
			}
		}

		// Progress logging
		if i%100 == 0 && i > 0 {
			log.Printf("indexer polkadot: processed %d/%d referenda (created: %d, updated: %d, errors: %d)",
				i, count, created, updated, errors)
		}

		// Small delay to not hammer the node
		time.Sleep(5 * time.Millisecond)
	}

	log.Printf("indexer polkadot: sync complete - created %d, updated %d, errors %d proposals",
		created, updated, errors)
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
