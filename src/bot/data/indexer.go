package data

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/stake-plus/govcomms/src/bot/types"
	"gorm.io/gorm"
)

// Helper functions for creating pointers
func strPtr(s string) *string {
	return &s
}

func uint16Ptr(u uint16) *uint16 {
	return &u
}

func uint32Ptr(u uint32) *uint32 {
	return &u
}

func timePtr(t time.Time) *time.Time {
	return &t
}

type NetworkIndexer struct {
	networkID   uint8
	networkName string
	db          *gorm.DB
	rpcURL      string
	mu          sync.Mutex
	running     bool
	workers     int
}

type MultiNetworkIndexer struct {
	indexers map[uint8]*NetworkIndexer
	db       *gorm.DB
	workers  int
}

func NewNetworkIndexer(networkID uint8, networkName string, db *gorm.DB, workers int) (*NetworkIndexer, error) {
	// Get RPC endpoint from database
	var rpc types.NetworkRPC
	err := db.Where("network_id = ? AND active = ?", networkID, true).First(&rpc).Error
	if err != nil {
		return nil, fmt.Errorf("no active RPC for network %d: %w", networkID, err)
	}

	return &NetworkIndexer{
		networkID:   networkID,
		networkName: networkName,
		db:          db,
		rpcURL:      rpc.URL,
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
	// Get all networks from database
	var networks []types.Network
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

		// Start each indexer in its own goroutine
		go func(idx *NetworkIndexer, netName string) {
			log.Printf("Starting indexer for %s", netName)
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
	}()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Run immediately
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

	// For now, we'll use a simplified approach
	// Check recent referenda and any ongoing ones from the database

	// Get ongoing refs from DB
	var ongoingRefs []types.Ref
	ongoingStatus := "Ongoing"
	ni.db.Where("network_id = ? AND (status IS NULL OR status = ?)", ni.networkID, ongoingStatus).Find(&ongoingRefs)

	log.Printf("%s indexer: Found %d ongoing referenda in database", ni.networkName, len(ongoingRefs))

	// Also check recent refs (last 50 created)
	var recentRefs []types.Ref
	ni.db.Where("network_id = ?", ni.networkID).Order("ref_id DESC").Limit(50).Find(&recentRefs)

	// Combine and deduplicate
	refMap := make(map[uint64]bool)
	var refsToCheck []uint64

	for _, ref := range ongoingRefs {
		if !refMap[ref.RefID] {
			refMap[ref.RefID] = true
			refsToCheck = append(refsToCheck, ref.RefID)
		}
	}

	for _, ref := range recentRefs {
		if !refMap[ref.RefID] {
			refMap[ref.RefID] = true
			refsToCheck = append(refsToCheck, ref.RefID)
		}
	}

	// If no refs in DB, check some initial refs (0-20)
	if len(refsToCheck) == 0 {
		for i := uint64(0); i < 20; i++ {
			refsToCheck = append(refsToCheck, i)
		}
	}

	log.Printf("%s indexer: Will check %d referenda", ni.networkName, len(refsToCheck))

	// Process refs in batches using worker pool
	batchSize := len(refsToCheck) / ni.workers
	if batchSize < 1 {
		batchSize = 1
	}

	var wg sync.WaitGroup
	for i := 0; i < len(refsToCheck); i += batchSize {
		end := i + batchSize
		if end > len(refsToCheck) {
			end = len(refsToCheck)
		}

		wg.Add(1)
		go func(refs []uint64) {
			defer wg.Done()
			for _, refID := range refs {
				select {
				case <-ctx.Done():
					return
				default:
					ni.processReferendum(refID)
				}
			}
		}(refsToCheck[i:end])
	}

	wg.Wait()

	// Also discover new refs by checking a range beyond the highest known
	var maxRef types.Ref
	if err := ni.db.Where("network_id = ?", ni.networkID).Order("ref_id DESC").First(&maxRef).Error; err == nil {
		// Check next 10 refs after the highest known
		for i := maxRef.RefID + 1; i <= maxRef.RefID+10; i++ {
			ni.processReferendum(i)
		}
	}

	log.Printf("%s indexer: Completed index run", ni.networkName)
}

func (ni *NetworkIndexer) processReferendum(refID uint64) {
	// For now, we'll create a basic entry if it doesn't exist
	// In production, this would fetch data from the chain

	var ref types.Ref
	err := ni.db.Where("network_id = ? AND ref_id = ?", ni.networkID, refID).First(&ref).Error

	if err == gorm.ErrRecordNotFound {
		// Create new referendum entry with basic info
		ref = types.Ref{
			NetworkID: ni.networkID,
			RefID:     refID,
			Submitter: "Unknown",
			Status:    strPtr("Ongoing"),
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}

		// Set some default values based on network
		if ni.networkID == 1 { // Polkadot
			ref.TrackID = uint16Ptr(0)
		} else if ni.networkID == 2 { // Kusama
			ref.TrackID = uint16Ptr(0)
		}

		if err := ni.db.Create(&ref).Error; err != nil {
			// Check if it's a duplicate key error (already exists)
			// MySQL error 1062 is duplicate entry
			if !strings.Contains(err.Error(), "Duplicate entry") && !strings.Contains(err.Error(), "duplicate key") {
				log.Printf("Failed to create referendum %s #%d: %v", ni.networkName, refID, err)
			}
		} else {
			log.Printf("Created referendum %s #%d", ni.networkName, refID)
		}
	} else if err == nil {
		// Referendum exists, check if we need to update status
		// In production, this would check the chain for current status

		// For now, we'll just update the timestamp
		updates := map[string]interface{}{
			"updated_at": time.Now(),
		}

		// Simulate status changes for testing
		// In production, this would come from chain data
		if ref.Status == nil || *ref.Status == "Ongoing" {
			// Check if it should be finalized (simplified logic)
			if ref.DecisionEnd > 0 && uint64(time.Now().Unix()) > ref.DecisionEnd {
				// Randomly approve or reject for testing
				if refID%2 == 0 {
					approvedStatus := "Approved"
					updates["status"] = &approvedStatus
					updates["approved"] = true
					updates["finalized"] = true
				} else {
					rejectedStatus := "Rejected"
					updates["status"] = &rejectedStatus
					updates["approved"] = false
					updates["finalized"] = true
				}
			}
		}

		if len(updates) > 0 {
			if err := ni.db.Model(&ref).Updates(updates).Error; err != nil {
				log.Printf("Failed to update referendum %s #%d: %v", ni.networkName, refID, err)
			}
		}
	} else {
		log.Printf("Database error for referendum %s #%d: %v", ni.networkName, refID, err)
	}
}

func (ni *NetworkIndexer) markReferendumCleared(refID uint64) {
	var ref types.Ref
	err := ni.db.Where("network_id = ? AND ref_id = ?", ni.networkID, refID).First(&ref).Error

	if err == nil && (ref.Status == nil || *ref.Status == "Ongoing") {
		clearedStatus := "Cleared"
		updates := map[string]interface{}{
			"status":     &clearedStatus,
			"finalized":  true,
			"updated_at": time.Now(),
		}

		if err := ni.db.Model(&ref).Updates(updates).Error; err != nil {
			log.Printf("Failed to mark referendum %s #%d as cleared: %v", ni.networkName, refID, err)
		} else {
			log.Printf("Marked referendum %s #%d as cleared", ni.networkName, refID)
		}
	}
}

// IndexerService starts the indexer for all networks
func IndexerService(ctx context.Context, db *gorm.DB, interval time.Duration, workers int) {
	log.Printf("Starting indexer service with %d workers, interval: %v", workers, interval)

	indexer := NewMultiNetworkIndexer(db, workers)
	if err := indexer.StartAll(ctx, interval, workers); err != nil {
		log.Printf("Failed to start indexer service: %v", err)
		return
	}

	// Wait for context cancellation
	<-ctx.Done()
	log.Println("Indexer service stopping")
}
