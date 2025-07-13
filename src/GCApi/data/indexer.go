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

	"github.com/stake-plus/govcomms/src/GCApi/types"
	polkadot "github.com/stake-plus/govcomms/src/polkadot-go"
	"gorm.io/gorm"
)

// NetworkIndexer handles indexing for a specific network
type NetworkIndexer struct {
	networkID   uint8
	networkName string
	rpcURLs     []string
	db          *gorm.DB
	client      *polkadot.Client
	mu          sync.RWMutex
	lastError   error
	running     bool
}

// NewNetworkIndexer creates an indexer for a specific network
func NewNetworkIndexer(networkID uint8, networkName string, db *gorm.DB) (*NetworkIndexer, error) {
	ni := &NetworkIndexer{
		networkID:   networkID,
		networkName: networkName,
		db:          db,
	}

	// Load RPC URLs for this network
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

	// Try each RPC URL until one succeeds
	for _, rpcURL := range ni.rpcURLs {
		log.Printf("Trying to connect to %s RPC: %s", ni.networkName, rpcURL)

		client, err := polkadot.NewClient(rpcURL)
		if err != nil {
			lastErr = err
			log.Printf("Failed to connect to %s: %v", rpcURL, err)
			continue
		}

		// Test the connection
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
	// Ensure we have a connection
	if ni.client == nil {
		if err := ni.connectToRPC(); err != nil {
			log.Printf("Failed to connect for %s: %v", ni.networkName, err)
			ni.lastError = err
			return
		}
	}

	log.Printf("Starting indexing run for %s", ni.networkName)

	// Run the indexing logic
	if err := ni.runIndexing(ctx); err != nil {
		log.Printf("Indexing error for %s: %v", ni.networkName, err)
		ni.lastError = err

		// Close client on error to force reconnection
		if ni.client != nil {
			ni.client.Close()
			ni.client = nil
		}
	}
}

func (ni *NetworkIndexer) runIndexing(ctx context.Context) error {
	// Get current block
	header, err := ni.client.GetHeader(nil)
	if err != nil {
		return fmt.Errorf("failed to get header: %w", err)
	}

	currentBlock := header.Number
	log.Printf("%s indexer: current block %s", ni.networkName, currentBlock)

	// Get all referendum keys that actually exist
	prefix := "0x" + polkadot.HexEncode(polkadot.Twox128([]byte("Referenda"))) + polkadot.HexEncode(polkadot.Twox128([]byte("ReferendumInfoFor")))

	keys, err := ni.client.GetKeys(prefix, nil)
	if err != nil {
		return fmt.Errorf("failed to get keys: %w", err)
	}

	log.Printf("%s indexer: found %d referendum keys", ni.networkName, len(keys))

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

	log.Printf("%s indexer: found %d existing referenda", ni.networkName, len(existingRefs))

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
	ni.db.Model(&types.Ref{}).Where("network_id = ?", ni.networkID).Count(&dbCount)
	log.Printf("%s indexer: database has %d proposals", ni.networkName, dbCount)

	created := 0
	updated := 0
	errors := 0
	processed := 0

	// Process each existing referendum in order
	for _, refID := range refIDs {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		processed++

		// Get referendum info - the client handles historical lookups
		info, err := ni.client.GetReferendumInfo(refID)

		var ref types.Ref
		dbErr := ni.db.Where("network_id = ? AND ref_id = ?", ni.networkID, refID).First(&ref).Error

		if err != nil {
			// Only log errors for refs we expect to work
			if !strings.Contains(err.Error(), "does not exist") {
				log.Printf("%s indexer: failed to get info for ref %d: %v", ni.networkName, refID, err)
			}

			if dbErr == gorm.ErrRecordNotFound {
				// Create with minimal info for cleared refs
				ref = types.Ref{
					RefID:     uint64(refID),
					NetworkID: ni.networkID,
					Status:    "Cleared",
					Submitter: "Unknown",
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				}

				if err := ni.db.Create(&ref).Error; err != nil {
					log.Printf("%s indexer: failed to create ref %d: %v", ni.networkName, refID, err)
					errors++
				} else {
					created++
				}
			}
			continue
		}

		// For finished referenda, always try to fetch historical data
		if info.Status != "Ongoing" && (info.Submission.Who == "Unknown" || info.Origin == "") {
			historicalInfo := ni.fetchHistoricalReferendumInfo(refID, info)
			if historicalInfo != nil {
				info = historicalInfo
			}
		}

		// We have referendum info
		if dbErr == gorm.ErrRecordNotFound {
			// Create new referendum with all related data
			ref = types.Ref{
				NetworkID:    ni.networkID,
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

			// Create proponents list
			proponents := []types.RefProponent{}

			// Add submitter
			if info.Submission.Who != "" && info.Submission.Who != "Unknown" {
				proponents = append(proponents, types.RefProponent{
					Address: info.Submission.Who,
					Role:    "submitter",
					Active:  1,
				})
			}

			// Add decision deposit provider if different
			if info.DecisionDeposit != nil && info.DecisionDeposit.Who != info.Submission.Who {
				proponents = append(proponents, types.RefProponent{
					Address: info.DecisionDeposit.Who,
					Role:    "decision_deposit",
					Active:  1,
				})
			}

			// Decode preimage to extract participants if available
			var addresses []string
			if info.Proposal != "" && info.ProposalLen > 0 {
				preimageDecoder := polkadot.NewPreimageDecoder(ni.client)
				decodedAddresses, err := preimageDecoder.FetchAndDecodePreimage(
					info.Proposal,
					info.ProposalLen,
					uint32(ref.Submitted),
				)
				if err != nil {
					log.Printf("Failed to decode preimage for ref %d: %v", refID, err)
				} else {
					addresses = decodedAddresses
					log.Printf("Decoded %d addresses from preimage for ref %d", len(addresses), refID)
				}
			}

			// Create referendum with all related data in a transaction
			if err := ni.createReferendumWithProponents(ref, proponents, addresses); err != nil {
				log.Printf("%s indexer: failed to create ref %d with proponents: %v", ni.networkName, refID, err)
				errors++
			} else {
				created++
			}

		} else if dbErr == nil {
			// Update existing if changed
			changed := false

			if ref.Status != info.Status && info.Status != "" {
				log.Printf("%s indexer: updating ref %d status from %s to %s", ni.networkName, refID, ref.Status, info.Status)
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
				if err := ni.db.Save(&ref).Error; err != nil {
					log.Printf("%s indexer: failed to update ref %d: %v", ni.networkName, refID, err)
					errors++
				} else {
					updated++
				}
			}
		}

		// Progress logging
		if processed%100 == 0 {
			log.Printf("%s indexer: processed %d referenda (created: %d, updated: %d, errors: %d)",
				ni.networkName, processed, created, updated, errors)
		}

		time.Sleep(5 * time.Second)
	}

	log.Printf("%s indexer: sync complete - processed %d, created %d, updated %d, errors %d refs",
		ni.networkName, processed, created, updated, errors)

	return nil
}

// createReferendumWithProponents creates a referendum with all related data in a transaction
func (ni *NetworkIndexer) createReferendumWithProponents(ref types.Ref, proponents []types.RefProponent, addresses []string) error {
	// Use a transaction to ensure atomicity
	return ni.db.Transaction(func(tx *gorm.DB) error {
		// Create the referendum
		if err := tx.Create(&ref).Error; err != nil {
			return err
		}

		// Create proponents
		for _, p := range proponents {
			p.RefID = ref.ID // Ensure correct RefID
			if err := tx.Create(&p).Error; err != nil {
				return err
			}
		}

		// Add recipients from preimage if available
		for _, addr := range addresses {
			if addr != "" && addr != ref.Submitter {
				proponent := types.RefProponent{
					RefID:   ref.ID,
					Address: addr,
					Role:    "recipient",
					Active:  1,
				}
				if err := tx.Create(&proponent).Error; err != nil {
					log.Printf("Failed to create recipient proponent: %v", err)
					// Don't fail the transaction for recipient errors
				}
			}
		}

		return nil
	})
}

// fetchHistoricalReferendumInfo tries to get referendum info from when it was ongoing
func (ni *NetworkIndexer) fetchHistoricalReferendumInfo(refID uint32, currentInfo *polkadot.ReferendumInfo) *polkadot.ReferendumInfo {
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
		blockHash, err := ni.client.GetBlockHash(&targetBlock)
		if err != nil {
			continue
		}

		// Get referendum info at that block
		historicalInfo, err := ni.client.GetReferendumInfoAt(refID, blockHash)
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

// MultiNetworkIndexer manages multiple network indexers
type MultiNetworkIndexer struct {
	indexers map[uint8]*NetworkIndexer
	db       *gorm.DB
	mu       sync.RWMutex
}

// NewMultiNetworkIndexer creates a new multi-network indexer
func NewMultiNetworkIndexer(db *gorm.DB) *MultiNetworkIndexer {
	return &MultiNetworkIndexer{
		indexers: make(map[uint8]*NetworkIndexer),
		db:       db,
	}
}

// StartAll starts indexing for all configured networks
func (mni *MultiNetworkIndexer) StartAll(ctx context.Context, interval time.Duration) error {
	// Load all networks from database
	var networks []types.Network
	if err := mni.db.Find(&networks).Error; err != nil {
		return fmt.Errorf("failed to load networks: %w", err)
	}

	// Create and start an indexer for each network
	for _, network := range networks {
		indexer, err := NewNetworkIndexer(network.ID, network.Name, mni.db)
		if err != nil {
			log.Printf("Failed to create indexer for %s: %v", network.Name, err)
			continue
		}

		mni.mu.Lock()
		mni.indexers[network.ID] = indexer
		mni.mu.Unlock()

		// Start indexer in its own goroutine
		go func(idx *NetworkIndexer, netName string) {
			log.Printf("Starting indexer for %s", netName)
			idx.Run(ctx, interval)
			log.Printf("Indexer for %s stopped", netName)
		}(indexer, network.Name)
	}

	if len(mni.indexers) == 0 {
		return fmt.Errorf("no indexers started")
	}

	log.Printf("Started %d network indexers", len(mni.indexers))
	return nil
}

// GetStatus returns the status of all indexers
func (mni *MultiNetworkIndexer) GetStatus() map[string]interface{} {
	mni.mu.RLock()
	defer mni.mu.RUnlock()

	status := make(map[string]interface{})
	for networkID, indexer := range mni.indexers {
		indexer.mu.RLock()
		status[fmt.Sprintf("network_%d", networkID)] = map[string]interface{}{
			"name":      indexer.networkName,
			"running":   indexer.running,
			"lastError": indexer.lastError,
		}
		indexer.mu.RUnlock()
	}

	return status
}

// IndexerService runs the multi-network indexer service
func IndexerService(ctx context.Context, db *gorm.DB, interval time.Duration) {
	indexer := NewMultiNetworkIndexer(db)

	if err := indexer.StartAll(ctx, interval); err != nil {
		log.Printf("Failed to start indexer service: %v", err)
		return
	}

	// Wait for context cancellation
	<-ctx.Done()
	log.Println("Indexer service stopping")
}
