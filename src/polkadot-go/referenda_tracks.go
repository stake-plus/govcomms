package polkadot

import (
	"encoding/binary"
	"fmt"

	"github.com/centrifuge/go-substrate-rpc-client/v4/types"
)

// getTrackName returns the name for a given track ID
func getTrackName(trackID uint16) string {
	trackNames := map[uint16]string{
		0:    "Root",
		1:    "WhitelistedCaller",
		10:   "StakingAdmin",
		11:   "Treasurer",
		12:   "LeaseAdmin",
		13:   "FellowshipAdmin",
		14:   "GeneralAdmin",
		15:   "AuctionAdmin",
		20:   "ReferendumCanceller",
		21:   "ReferendumKiller",
		30:   "SmallTipper",
		31:   "BigTipper",
		32:   "SmallSpender",
		33:   "MediumSpender",
		34:   "BigSpender",
		1000: "WishForChange",
	}

	if name, ok := trackNames[trackID]; ok {
		return name
	}
	return fmt.Sprintf("Track%d", trackID)
}

// GetTrackInfo gets information about a specific track
func (c *Client) GetTrackInfo(trackID uint16) (*TrackInfo, error) {
	// Create storage key for Referenda.Tracks
	palletHash := Twox128([]byte("Referenda"))
	storageHash := Twox128([]byte("Tracks"))

	// Encode track ID for the key
	trackIDBytes := make([]byte, 2)
	binary.LittleEndian.PutUint16(trackIDBytes, trackID)

	// Use Blake2_128_Concat hasher for the key
	hashedKey := append(Blake2_128(trackIDBytes), trackIDBytes...)

	key := append(palletHash, storageHash...)
	key = append(key, hashedKey...)

	storageKey := types.NewStorageKey(key)
	var raw types.StorageDataRaw
	ok, err := c.api.RPC.State.GetStorageLatest(storageKey, &raw)
	if err != nil {
		return nil, err
	}
	if !ok {
		// Return default values for tracks that don't exist in storage
		return &TrackInfo{
			Name:               getTrackName(trackID),
			MaxDeciding:        1,
			DecisionPeriod:     403200, // 28 days default
			ConfirmPeriod:      14400,  // 1 day default
			MinEnactmentPeriod: 14400,
			MinApproval:        "50%",
			MinSupport:         "0.01%",
		}, nil
	}

	// TODO: Decode actual track info from raw data
	// For now, return defaults with the correct name
	return &TrackInfo{
		Name:               getTrackName(trackID),
		MaxDeciding:        1,
		DecisionPeriod:     403200, // 28 days default
		ConfirmPeriod:      14400,  // 1 day default
		MinEnactmentPeriod: 14400,
		MinApproval:        "50%",
		MinSupport:         "0.01%",
	}, nil
}

// GetAllTracks gets all track information from the chain
func (c *Client) GetAllTracks() (map[uint16]*TrackInfo, error) {
	// Query all track keys
	prefix := "0x" + HexEncode(Twox128([]byte("Referenda"))) + HexEncode(Twox128([]byte("Tracks")))

	keys, err := c.GetKeys(prefix, nil)
	if err != nil {
		return nil, fmt.Errorf("get track keys: %w", err)
	}

	tracks := make(map[uint16]*TrackInfo)

	// Process each key to extract track ID
	prefixLen := len(prefix) - 2 // Remove "0x"
	for _, key := range keys {
		// Remove 0x prefix
		keyHex := key[2:]

		// The key format is: prefix + blake2_128(trackID) + trackID
		if len(keyHex) > prefixLen {
			// Get the part after the prefix
			remainder := keyHex[prefixLen:]

			// The remainder should be: blake2_128_hash(16 bytes = 32 hex chars) + trackID(2 bytes = 4 hex chars)
			if len(remainder) >= 36 { // 32 + 4
				// Extract the last 4 hex characters (2 bytes) which is the trackID
				trackIDHex := remainder[len(remainder)-4:]

				// Convert hex to bytes
				trackIDBytes, err := DecodeHex(trackIDHex)
				if err != nil || len(trackIDBytes) != 2 {
					continue
				}

				// Convert to uint16 (little endian)
				trackID := binary.LittleEndian.Uint16(trackIDBytes)

				// Get track info
				info, err := c.GetTrackInfo(trackID)
				if err == nil {
					tracks[trackID] = info
				}
			}
		}
	}

	// Add known tracks that might not be in storage
	knownTracks := []uint16{0, 1, 10, 11, 12, 13, 14, 15, 20, 21, 30, 31, 32, 33, 34, 1000}
	for _, trackID := range knownTracks {
		if _, exists := tracks[trackID]; !exists {
			tracks[trackID] = &TrackInfo{
				Name:               getTrackName(trackID),
				MaxDeciding:        1,
				DecisionPeriod:     403200,
				ConfirmPeriod:      14400,
				MinEnactmentPeriod: 14400,
				MinApproval:        "50%",
				MinSupport:         "0.01%",
			}
		}
	}

	return tracks, nil
}
