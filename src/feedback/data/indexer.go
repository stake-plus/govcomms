package data

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	polkadot "github.com/stake-plus/govcomms/src/polkadot-go"
	sharedgov "github.com/stake-plus/govcomms/src/shared/gov"
	"gorm.io/gorm"
)

type NetworkIndexer struct {
	networkID    uint8
	networkName  string
	db           *gorm.DB
	rpcURL       string
	client       *polkadot.Client
	mu           sync.Mutex
	running      bool
	workers      int
	currentBlock uint32
}

type MultiNetworkIndexer struct {
	indexers map[uint8]*NetworkIndexer
	db       *gorm.DB
	workers  int
}

func NewNetworkIndexer(networkID uint8, networkName string, db *gorm.DB, workers int) (*NetworkIndexer, error) {
	var rpc sharedgov.NetworkRPC
	err := db.Where("network_id = ? AND active = ?", networkID, true).First(&rpc).Error
	if err != nil {
		return nil, fmt.Errorf("no active RPC for network %d: %w", networkID, err)
	}

	client, err := polkadot.NewClient(rpc.URL)
	if err != nil {
		return nil, fmt.Errorf("failed to create polkadot client for %s: %w", networkName, err)
	}

	return &NetworkIndexer{
		networkID:   networkID,
		networkName: networkName,
		db:          db,
		rpcURL:      rpc.URL,
		client:      client,
		workers:     workers,
	}, nil
}

func NewMultiNetworkIndexer(db *gorm.DB, workers int) *MultiNetworkIndexer {
	return &MultiNetworkIndexer{
		indexers: make(map[uint8]*NetworkIndexer),
		db:       db,
		workers:  workers,
	}
}

func (mni *MultiNetworkIndexer) StartAll(ctx context.Context, interval time.Duration, workers int) error {
	var networks []sharedgov.Network
	if err := mni.db.Find(&networks).Error; err != nil {
		return fmt.Errorf("failed to load networks: %w", err)
	}

	for _, network := range networks {
		indexer, err := NewNetworkIndexer(network.ID, network.Name, mni.db, workers)
		if err != nil {
			log.Printf("Failed to create indexer for %s: %v", network.Name, err)
			continue
		}

		mni.indexers[network.ID] = indexer

		go func(idx *NetworkIndexer, netName string) {
			log.Printf("Starting indexer for %s with RPC: %s", netName, idx.rpcURL)
			idx.Run(ctx, interval)
			log.Printf("Indexer for %s stopped", netName)
		}(indexer, network.Name)
	}

	return nil
}

func (ni *NetworkIndexer) Run(ctx context.Context, interval time.Duration) {
	ni.mu.Lock()
	if ni.running {
		ni.mu.Unlock()
		return
	}
	ni.running = true
	ni.mu.Unlock()

	defer func() {
		ni.mu.Lock()
		ni.running = false
		ni.mu.Unlock()
		if ni.client != nil {
			ni.client.Close()
		}
	}()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	ni.indexOnce(ctx)

	for {
		select {
		case <-ctx.Done():
			log.Printf("Stopping indexer for %s", ni.networkName)
			return
		case <-ticker.C:
			ni.indexOnce(ctx)
		}
	}
}

func (ni *NetworkIndexer) indexOnce(ctx context.Context) {
	log.Printf("%s indexer: Starting index run", ni.networkName)

	// Get current block number
	header, err := ni.client.GetHeader(nil)
	if err != nil {
		log.Printf("%s indexer: Failed to get current block: %v", ni.networkName, err)
		return
	}

	currentBlock, err := polkadot.DecodeU32(header.Number)
	if err != nil {
		log.Printf("%s indexer: Failed to parse block number: %v", ni.networkName, err)
		return
	}

	ni.currentBlock = currentBlock
	log.Printf("%s indexer: Current block height: %d", ni.networkName, currentBlock)

	refCount, err := ni.client.GetReferendumCount()
	if err != nil {
		log.Printf("%s indexer: Failed to get referendum count: %v", ni.networkName, err)
		return
	}

	log.Printf("%s indexer: Chain has %d total referenda", ni.networkName, refCount)

	var ongoingRefs []sharedgov.Ref
	ni.db.Where("network_id = ? AND finalized = ?", ni.networkID, false).Find(&ongoingRefs)
	log.Printf("%s indexer: Found %d ongoing referenda in database", ni.networkName, len(ongoingRefs))

	for _, ref := range ongoingRefs {
		select {
		case <-ctx.Done():
			return
		default:
			ni.processReferendum(ref.RefID)
			time.Sleep(100 * time.Millisecond)
		}
	}

	start := uint64(0)
	if refCount > 100 {
		start = uint64(refCount - 100)
	}

	for i := start; i < uint64(refCount); i++ {
		select {
		case <-ctx.Done():
			return
		default:
			ni.processReferendum(i)
			time.Sleep(100 * time.Millisecond)
		}
	}

	log.Printf("%s indexer: Completed index run", ni.networkName)
}

func (ni *NetworkIndexer) processReferendum(refID uint64) {
	refInfo, err := ni.client.GetReferendumInfo(uint32(refID))

	// Check if referendum exists in database
	var ref sharedgov.Ref
	dbErr := ni.db.Where("network_id = ? AND ref_id = ?", ni.networkID, refID).First(&ref).Error

	if err != nil {
		if strings.Contains(err.Error(), "does not exist") || strings.Contains(err.Error(), "not found") {
			// Referendum doesn't exist on chain
			if dbErr == nil && !ref.Finalized {
				log.Printf("%s ref #%d no longer exists on chain, marking as cleared", ni.networkName, refID)
				clearedStatus := "Cleared"
				updates := map[string]interface{}{
					"status":     &clearedStatus,
					"finalized":  true,
					"updated_at": time.Now(),
				}
				ni.db.Model(&ref).Updates(updates)
			}
			return
		}

		// Decoding error - try to create minimal record
		log.Printf("Failed to fully decode %s ref #%d: %v", ni.networkName, refID, err)

		// If we don't have it in DB, create a minimal record
		if dbErr == gorm.ErrRecordNotFound {
			unknownStatus := "Unknown"
			ref = sharedgov.Ref{
				NetworkID: ni.networkID,
				RefID:     refID,
				Submitter: "Unknown",
				Status:    &unknownStatus,
				Submitted: 0,
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			}

			if err := ni.db.Create(&ref).Error; err != nil {
				if !strings.Contains(err.Error(), "Duplicate entry") && !strings.Contains(err.Error(), "duplicate key") {
					log.Printf("Failed to create minimal %s ref #%d: %v", ni.networkName, refID, err)
				}
			} else {
				log.Printf("Created minimal record for %s ref #%d (decode error: %v)", ni.networkName, refID, err)
			}
		}
		return
	}

	// Successfully decoded referendum
	if dbErr == gorm.ErrRecordNotFound {
		// Create new referendum
		ref = sharedgov.Ref{
			NetworkID: ni.networkID,
			RefID:     refID,
			Submitter: refInfo.Submission.Who,
			Status:    &refInfo.Status,
			TrackID:   &refInfo.Track,
			Origin:    &refInfo.Origin,
			Enactment: &refInfo.Enactment,
			Submitted: uint64(refInfo.Submitted),
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}

		// Calculate submitted time - submitted block number * 6 seconds per block
		if refInfo.Submitted > 0 && ni.currentBlock > 0 {
			// Calculate blocks ago
			blocksAgo := int64(ni.currentBlock) - int64(refInfo.Submitted)
			if blocksAgo > 0 {
				secondsAgo := blocksAgo * 6
				submittedTime := time.Now().Add(-time.Duration(secondsAgo) * time.Second)
				// Only set if the date is reasonable (after 1970)
				if submittedTime.Year() >= 1970 {
					ref.SubmittedAt = &submittedTime
				}
			}
		}

		if refInfo.Proposal != "" {
			ref.PreimageHash = &refInfo.Proposal
			if refInfo.ProposalLen > 0 {
				ref.PreimageLen = &refInfo.ProposalLen
			}
		}

		if refInfo.DecisionDeposit != nil {
			ref.DecisionDepositWho = &refInfo.DecisionDeposit.Who
			ref.DecisionDepositAmount = &refInfo.DecisionDeposit.Amount
		}

		if refInfo.Submission.Who != "" {
			ref.SubmissionDepositWho = &refInfo.Submission.Who
			ref.SubmissionDepositAmount = &refInfo.Submission.Amount
		}

		if refInfo.Tally.Ayes != "" {
			ref.Ayes = &refInfo.Tally.Ayes
		}
		if refInfo.Tally.Nays != "" {
			ref.Nays = &refInfo.Tally.Nays
		}
		if refInfo.Tally.Support != "" {
			ref.Support = &refInfo.Tally.Support
		}
		if refInfo.Tally.Approval != "" {
			ref.Approval = &refInfo.Tally.Approval
		}

		if refInfo.Decision != nil {
			ref.DecisionStart = uint64(refInfo.Decision.Since)
			if refInfo.Decision.Confirming != nil {
				ref.ConfirmStart = uint64(*refInfo.Decision.Confirming)
			}
		}

		if refInfo.Status != "Ongoing" {
			ref.Finalized = true
			ref.Approved = refInfo.Status == "Approved"
			now := uint64(time.Now().Unix())
			switch refInfo.Status {
			case "Approved":
				if refInfo.ApprovedAt > 0 {
					now = uint64(refInfo.ApprovedAt)
				}
			case "Rejected":
				if refInfo.RejectedAt > 0 {
					now = uint64(refInfo.RejectedAt)
				}
			case "Cancelled":
				if refInfo.CancelledAt > 0 {
					now = uint64(refInfo.CancelledAt)
				}
			case "TimedOut":
				if refInfo.TimedOutAt > 0 {
					now = uint64(refInfo.TimedOutAt)
				}
			case "Killed":
				if refInfo.KilledAt > 0 {
					now = uint64(refInfo.KilledAt)
				}
			}
			ref.ConfirmEnd = now
		}

		if err := ni.db.Create(&ref).Error; err != nil {
			if !strings.Contains(err.Error(), "Duplicate entry") && !strings.Contains(err.Error(), "duplicate key") {
				log.Printf("Failed to create %s ref #%d: %v", ni.networkName, refID, err)
			}
		} else {
			log.Printf("Created %s ref #%d - Status: %s, Track: %d, Submitter: %s",
				ni.networkName, refID, refInfo.Status, refInfo.Track, refInfo.Submission.Who)
		}
	} else if dbErr == nil {
		// Update existing referendum
		updates := map[string]interface{}{
			"updated_at": time.Now(),
		}

		updates["status"] = &refInfo.Status

		if refInfo.Tally.Ayes != "" {
			updates["ayes"] = &refInfo.Tally.Ayes
		}
		if refInfo.Tally.Nays != "" {
			updates["nays"] = &refInfo.Tally.Nays
		}
		if refInfo.Tally.Support != "" {
			updates["support"] = &refInfo.Tally.Support
		}
		if refInfo.Tally.Approval != "" {
			updates["approval"] = &refInfo.Tally.Approval
		}

		if refInfo.Decision != nil {
			updates["decision_start"] = uint64(refInfo.Decision.Since)
			if refInfo.Decision.Confirming != nil {
				updates["confirm_start"] = uint64(*refInfo.Decision.Confirming)
			}
		}

		if refInfo.Status != "Ongoing" && !ref.Finalized {
			updates["finalized"] = true
			updates["approved"] = refInfo.Status == "Approved"
			now := uint64(time.Now().Unix())
			switch refInfo.Status {
			case "Approved":
				if refInfo.ApprovedAt > 0 {
					now = uint64(refInfo.ApprovedAt)
				}
			case "Rejected":
				if refInfo.RejectedAt > 0 {
					now = uint64(refInfo.RejectedAt)
				}
			case "Cancelled":
				if refInfo.CancelledAt > 0 {
					now = uint64(refInfo.CancelledAt)
				}
			case "TimedOut":
				if refInfo.TimedOutAt > 0 {
					now = uint64(refInfo.TimedOutAt)
				}
			case "Killed":
				if refInfo.KilledAt > 0 {
					now = uint64(refInfo.KilledAt)
				}
			}
			updates["confirm_end"] = now

			log.Printf("%s ref #%d finalized with status: %s", ni.networkName, refID, refInfo.Status)
		}

		if err := ni.db.Model(&ref).Updates(updates).Error; err != nil {
			log.Printf("Failed to update %s ref #%d: %v", ni.networkName, refID, err)
		}
	} else {
		log.Printf("Database error for %s ref #%d: %v", ni.networkName, refID, dbErr)
	}
}

func IndexerService(ctx context.Context, db *gorm.DB, interval time.Duration, workers int) {
	log.Printf("Starting indexer service with %d workers, interval: %v", workers, interval)

	indexer := NewMultiNetworkIndexer(db, workers)
	if err := indexer.StartAll(ctx, interval, workers); err != nil {
		log.Printf("Failed to start indexer service: %v", err)
		return
	}

	<-ctx.Done()
	log.Println("Indexer service stopping")
}
