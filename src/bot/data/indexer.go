package data

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/stake-plus/govcomms/src/bot/types"
	polkadot "github.com/stake-plus/govcomms/src/polkadot-go"
	"gorm.io/gorm"
)

type NetworkIndexer struct {
	networkID   uint8
	networkName string
	rpcURLs     []string
	db          *gorm.DB
	client      *polkadot.Client
	mu          sync.RWMutex
	lastError   error
	running     bool
	workers     int
}

func NewNetworkIndexer(networkID uint8, networkName string, db *gorm.DB, workers int) (*NetworkIndexer, error) {
	ni := &NetworkIndexer{
		networkID:   networkID,
		networkName: networkName,
		db:          db,
		workers:     workers,
	}

	if err := ni.loadRPCURLs(); err != nil {
		return nil, err
	}

	if len(ni.rpcURLs) == 0 {
		return nil, fmt.Errorf("no active RPC URLs found for network %s", networkName)
	}

	return ni, nil
}

func (ni *NetworkIndexer) loadRPCURLs() error {
	var rpcs []types.NetworkRPC
	err := ni.db.Where("network_id = ? AND active = ?", ni.networkID, true).Find(&rpcs).Error
	if err != nil {
		return err
	}

	ni.rpcURLs = make([]string, 0, len(rpcs))
	for _, rpc := range rpcs {
		ni.rpcURLs = append(ni.rpcURLs, rpc.URL)
	}

	return nil
}

func (ni *NetworkIndexer) connectToRPC() error {
	var lastErr error
	for _, rpcURL := range ni.rpcURLs {
		log.Printf("Trying to connect to %s RPC: %s", ni.networkName, rpcURL)
		client, err := polkadot.NewClient(rpcURL)
		if err != nil {
			lastErr = err
			log.Printf("Failed to connect to %s: %v", rpcURL, err)
			continue
		}

		header, err := client.GetHeader(nil)
		if err != nil {
			client.Close()
			lastErr = err
			log.Printf("Failed to get header from %s: %v", rpcURL, err)
			continue
		}

		log.Printf("Successfully connected to %s RPC at block %s", ni.networkName, header.Number)
		ni.client = client
		return nil
	}

	return fmt.Errorf("failed to connect to any RPC for %s: %v", ni.networkName, lastErr)
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
	if ni.client == nil {
		if err := ni.connectToRPC(); err != nil {
			log.Printf("Failed to connect for %s: %v", ni.networkName, err)
			ni.lastError = err
			return
		}
	}

	log.Printf("Starting indexing run for %s", ni.networkName)

	if err := ni.runIndexing(ctx); err != nil {
		log.Printf("Indexing error for %s: %v", ni.networkName, err)
		ni.lastError = err
		if ni.client != nil {
			ni.client.Close()
			ni.client = nil
		}
	}
}

func (ni *NetworkIndexer) runIndexing(ctx context.Context) error {
	header, err := ni.client.GetHeader(nil)
	if err != nil {
		return fmt.Errorf("failed to get header: %w", err)
	}

	currentBlock := header.Number
	log.Printf("%s indexer: current block %s", ni.networkName, currentBlock)

	// Get unfinalized referendum IDs from database
	var unfinalizedRefs []uint64
	err = ni.db.Model(&types.Ref{}).
		Where("network_id = ? AND finalized = ?", ni.networkID, false).
		Pluck("ref_id", &unfinalizedRefs).Error
	if err != nil {
		return fmt.Errorf("get unfinalized refs: %w", err)
	}

	// Get max ref ID from database
	var maxRefID uint64
	ni.db.Model(&types.Ref{}).
		Where("network_id = ?", ni.networkID).
		Select("MAX(ref_id)").
		Scan(&maxRefID)

	// Get all referendum keys from chain
	prefix := "0x" + polkadot.HexEncode(polkadot.Twox128([]byte("Referenda"))) + polkadot.HexEncode(polkadot.Twox128([]byte("ReferendumInfoFor")))
	keys, err := ni.client.GetKeys(prefix, nil)
	if err != nil {
		return fmt.Errorf("failed to get keys: %w", err)
	}

	// Extract referendum IDs to check
	refsToCheck := make(map[uint32]bool)
	prefixLen := len(prefix) - 2

	for _, key := range keys {
		keyHex := strings.TrimPrefix(key, "0x")
		if len(keyHex) > prefixLen {
			remainder := keyHex[prefixLen:]
			if len(remainder) >= 40 {
				refIDHex := remainder[len(remainder)-8:]
				refIDBytes, err := polkadot.DecodeHex(refIDHex)
				if err != nil || len(refIDBytes) != 4 {
					continue
				}
				refID := binary.LittleEndian.Uint32(refIDBytes)

				// Check if it's a gap referendum (missing from DB)
				if refID < uint32(maxRefID) {
					var exists bool
					ni.db.Model(&types.Ref{}).
						Where("network_id = ? AND ref_id = ?", ni.networkID, refID).
						Select("1").
						Limit(1).
						Scan(&exists)
					if !exists {
						refsToCheck[refID] = true
						continue
					}
				}

				// Check if it's new or unfinalized
				if refID > uint32(maxRefID) {
					refsToCheck[refID] = true
				} else {
					// Check if it's in our unfinalized list
					for _, unfinalizedID := range unfinalizedRefs {
						if uint32(unfinalizedID) == refID {
							refsToCheck[refID] = true
							break
						}
					}
				}
			}
		}
	}

	log.Printf("%s indexer: will check %d referenda", ni.networkName, len(refsToCheck))

	// Convert map to sorted slice
	var refIDs []uint32
	for refID := range refsToCheck {
		refIDs = append(refIDs, refID)
	}
	sort.Slice(refIDs, func(i, j int) bool {
		return refIDs[i] < refIDs[j]
	})

	// Process with worker pool
	return ni.processWithWorkers(ctx, refIDs)
}

func (ni *NetworkIndexer) processWithWorkers(ctx context.Context, refIDs []uint32) error {
	jobs := make(chan uint32, len(refIDs))
	results := make(chan error, len(refIDs))

	var wg sync.WaitGroup

	// Start workers
	for w := 0; w < ni.workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for refID := range jobs {
				select {
				case <-ctx.Done():
					return
				default:
					err := ni.processReferendum(refID)
					results <- err
				}
			}
		}()
	}

	// Send jobs
	for _, refID := range refIDs {
		jobs <- refID
	}
	close(jobs)

	// Wait for workers
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results
	var errors int
	for err := range results {
		if err != nil {
			errors++
		}
	}

	if errors > 0 {
		log.Printf("%s indexer: completed with %d errors", ni.networkName, errors)
	}

	return nil
}

func (ni *NetworkIndexer) processReferendum(refID uint32) error {
	info, err := ni.client.GetReferendumInfo(refID)

	var ref types.Ref
	dbErr := ni.db.Where("network_id = ? AND ref_id = ?", ni.networkID, refID).First(&ref).Error

	if err != nil {
		if !strings.Contains(err.Error(), "does not exist") {
			log.Printf("%s indexer: failed to get info for ref %d: %v", ni.networkName, refID, err)
		}
		if dbErr == gorm.ErrRecordNotFound {
			ref = types.Ref{
				RefID:     uint64(refID),
				NetworkID: ni.networkID,
				Status:    "Cleared",
				Submitter: "Unknown",
				Finalized: true,
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			}
			return ni.db.Create(&ref).Error
		}
		return nil
	}

	// Determine if referendum is finalized
	isFinalized := false
	switch info.Status {
	case "Approved", "Rejected", "Cancelled", "TimedOut", "Killed", "Cleared":
		isFinalized = true
	}

	if dbErr == gorm.ErrRecordNotFound {
		// Create new referendum
		ref = types.Ref{
			NetworkID:    ni.networkID,
			RefID:        uint64(refID),
			Status:       info.Status,
			TrackID:      info.Track,
			Origin:       info.Origin,
			Enactment:    info.Enactment,
			Submitted:    uint64(info.Submitted),
			Approved:     info.Status == "Approved",
			Finalized:    isFinalized,
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
			PreimageHash: info.Proposal,
			PreimageLen:  info.ProposalLen,
		}

		if info.Submission.Who != "" {
			ref.Submitter = info.Submission.Who
			ref.SubmissionDepositWho = info.Submission.Who
			ref.SubmissionDepositAmount = info.Submission.Amount
		} else {
			ref.Submitter = "Unknown"
		}

		if info.DecisionDeposit != nil {
			ref.DecisionDepositWho = info.DecisionDeposit.Who
			ref.DecisionDepositAmount = info.DecisionDeposit.Amount
		}

		if info.Status == "Ongoing" && info.Tally.Ayes != "" {
			ref.Ayes = info.Tally.Ayes
			ref.Nays = info.Tally.Nays
			ref.Support = info.Tally.Support
		}

		if info.Decision != nil {
			ref.DecisionStart = uint64(info.Decision.Since)
			if info.Decision.Confirming != nil {
				ref.ConfirmStart = uint64(*info.Decision.Confirming)
			}
		}

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

		return ni.db.Create(&ref).Error
	}

	// Update existing referendum if not finalized
	if !ref.Finalized {
		changed := false

		if ref.Status != info.Status && info.Status != "" {
			ref.Status = info.Status
			ref.Finalized = isFinalized
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

		if info.Status == "Approved" && !ref.Approved {
			ref.Approved = true
			changed = true
		}

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

		if changed {
			ref.UpdatedAt = time.Now()
			return ni.db.Save(&ref).Error
		}
	}

	return nil
}

type MultiNetworkIndexer struct {
	indexers map[uint8]*NetworkIndexer
	db       *gorm.DB
	mu       sync.RWMutex
}

func NewMultiNetworkIndexer(db *gorm.DB, workers int) *MultiNetworkIndexer {
	return &MultiNetworkIndexer{
		indexers: make(map[uint8]*NetworkIndexer),
		db:       db,
	}
}

func (mni *MultiNetworkIndexer) StartAll(ctx context.Context, interval time.Duration, workers int) error {
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

		mni.mu.Lock()
		mni.indexers[network.ID] = indexer
		mni.mu.Unlock()

		go func(idx *NetworkIndexer, netName string) {
			log.Printf("Starting indexer for %s", netName)
			idx.Run(ctx, interval)
			log.Printf("Indexer for %s stopped", netName)
		}(indexer, network.Name)
	}

	if len(mni.indexers) == 0 {
		return fmt.Errorf("no indexers started")
	}

	log.Printf("Started %d network indexers with %d workers each", len(mni.indexers), workers)
	return nil
}

func IndexerService(ctx context.Context, db *gorm.DB, interval time.Duration, workers int) {
	indexer := NewMultiNetworkIndexer(db, workers)
	if err := indexer.StartAll(ctx, interval, workers); err != nil {
		log.Printf("Failed to start indexer service: %v", err)
		return
	}

	<-ctx.Done()
	log.Println("Indexer service stopping")
}
