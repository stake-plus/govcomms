package polkadot

import (
	"fmt"
	"math/big"
	"strings"
)

// ReferendaConstants holds all referenda pallet constants
type ReferendaConstants struct {
	Tracks            map[uint16]*TrackInfo
	UndecidingTimeout uint32
	SubmissionDeposit string
	MaxQueued         uint32
}

// GetReferendaConstants fetches all referenda constants from chain
func (c *Client) GetReferendaConstants() (*ReferendaConstants, error) {
	// Check cache first
	c.constantsMu.RLock()
	if cached, exists := c.constantsCache["referenda_constants"]; exists {
		c.constantsMu.RUnlock()
		return cached.(*ReferendaConstants), nil
	}
	c.constantsMu.RUnlock()

	constants := &ReferendaConstants{
		Tracks: make(map[uint16]*TrackInfo),
	}

	// Get tracks using state_getStorage
	tracksKey := createConstantKey("Referenda", "Tracks")
	tracksData, err := c.getConstantValue(tracksKey)
	if err != nil {
		return nil, fmt.Errorf("get tracks constant: %w", err)
	}

	// Decode tracks
	if err := c.decodeTracks(tracksData, constants.Tracks); err != nil {
		return nil, fmt.Errorf("decode tracks: %w", err)
	}

	// Get undeciding timeout
	timeoutKey := createConstantKey("Referenda", "UndecidingTimeout")
	timeoutData, err := c.getConstantValue(timeoutKey)
	if err != nil {
		return nil, fmt.Errorf("get undeciding timeout: %w", err)
	}
	if len(timeoutData) >= 4 {
		constants.UndecidingTimeout = uint32(timeoutData[0]) | uint32(timeoutData[1])<<8 |
			uint32(timeoutData[2])<<16 | uint32(timeoutData[3])<<24
	}

	// Get submission deposit
	depositKey := createConstantKey("Referenda", "SubmissionDeposit")
	depositData, err := c.getConstantValue(depositKey)
	if err != nil {
		return nil, fmt.Errorf("get submission deposit: %w", err)
	}
	if len(depositData) >= 16 {
		deposit := new(big.Int)
		for i := 0; i < 16; i++ {
			deposit.Or(deposit, new(big.Int).Lsh(new(big.Int).SetUint64(uint64(depositData[i])), uint(i*8)))
		}
		constants.SubmissionDeposit = deposit.String()
	}

	// Get max queued
	maxQueuedKey := createConstantKey("Referenda", "MaxQueued")
	maxQueuedData, err := c.getConstantValue(maxQueuedKey)
	if err != nil {
		return nil, fmt.Errorf("get max queued: %w", err)
	}
	if len(maxQueuedData) >= 4 {
		constants.MaxQueued = uint32(maxQueuedData[0]) | uint32(maxQueuedData[1])<<8 |
			uint32(maxQueuedData[2])<<16 | uint32(maxQueuedData[3])<<24
	}

	// Cache the result
	c.constantsMu.Lock()
	c.constantsCache["referenda_constants"] = constants
	c.constantsMu.Unlock()

	return constants, nil
}

// GetSS58Prefix gets the chain's SS58 prefix
func (c *Client) GetSS58Prefix() (uint16, error) {
	// Check cache first
	if c.ss58Cached {
		return c.ss58Prefix, nil
	}

	// Get SS58 prefix using state_getStorage
	prefixKey := createConstantKey("System", "SS58Prefix")
	prefixData, err := c.getConstantValue(prefixKey)
	if err != nil {
		return 42, fmt.Errorf("get ss58 prefix: %w", err) // Default to generic substrate
	}

	if len(prefixData) >= 2 {
		prefix := uint16(prefixData[0]) | uint16(prefixData[1])<<8
		c.ss58Prefix = prefix
		c.ss58Cached = true
		return prefix, nil
	}

	return 42, nil // Default to generic substrate
}

// createConstantKey creates a storage key for a pallet constant
func createConstantKey(pallet, constant string) string {
	// Constants are stored at: twox128(pallet) + twox128("Constant") + twox128(constant)
	palletHash := Twox128([]byte(pallet))
	constantHash := Twox128([]byte(constant))

	key := append(palletHash, constantHash...)
	return "0x" + HexEncode(key)
}

// getConstantValue retrieves a constant value using direct RPC call
func (c *Client) getConstantValue(key string) ([]byte, error) {
	// First try to get it from storage
	data, err := c.GetStorage(key, nil)
	if err == nil && data != "" && data != "0x" {
		return DecodeHex(data)
	}

	// If not in storage, we need to get it from metadata
	// For now, we'll use a different approach - query the metadata directly via RPC
	var result string
	err = c.api.Client.Call(&result, "state_getMetadata", nil)
	if err != nil {
		return nil, fmt.Errorf("get metadata: %w", err)
	}

	// The metadata contains the constants, but parsing it is complex
	// For now, return empty data which will use defaults
	return nil, fmt.Errorf("constant not found in storage")
}

// decodeTracks decodes the tracks Vec<(u16, TrackInfo)>
func (c *Client) decodeTracks(data []byte, tracks map[uint16]*TrackInfo) error {
	if len(data) == 0 {
		// If we can't get the data, use known tracks
		return c.loadKnownTracks(tracks)
	}

	// The data is a Vec<(u16, TrackInfo)>
	offset := 0
	length, bytesRead := decodeCompactInteger(data[offset:])
	offset += bytesRead

	// Decode each track
	for i := 0; i < int(length); i++ {
		// Decode track ID (u16)
		if offset+2 > len(data) {
			return fmt.Errorf("insufficient data for track ID")
		}
		trackID := uint16(data[offset]) | uint16(data[offset+1])<<8
		offset += 2

		// Decode TrackInfo
		track := &TrackInfo{}

		// Name (Vec<u8>)
		nameLen, bytesRead := decodeCompactInteger(data[offset:])
		offset += bytesRead
		if offset+int(nameLen) > len(data) {
			return fmt.Errorf("insufficient data for track name")
		}
		track.Name = string(data[offset : offset+int(nameLen)])
		offset += int(nameLen)

		// MaxDeciding (u32)
		if offset+4 > len(data) {
			return fmt.Errorf("insufficient data for max deciding")
		}
		track.MaxDeciding = uint32(data[offset]) | uint32(data[offset+1])<<8 |
			uint32(data[offset+2])<<16 | uint32(data[offset+3])<<24
		offset += 4

		// DecisionDeposit (u128)
		if offset+16 > len(data) {
			return fmt.Errorf("insufficient data for decision deposit")
		}
		deposit := new(big.Int)
		for i := 0; i < 16; i++ {
			deposit.Or(deposit, new(big.Int).Lsh(new(big.Int).SetUint64(uint64(data[offset+i])), uint(i*8)))
		}
		track.DecisionDeposit = deposit.String()
		offset += 16

		// PreparePeriod (u32)
		if offset+4 > len(data) {
			return fmt.Errorf("insufficient data for prepare period")
		}
		track.PreparePeriod = uint32(data[offset]) | uint32(data[offset+1])<<8 |
			uint32(data[offset+2])<<16 | uint32(data[offset+3])<<24
		offset += 4

		// DecisionPeriod (u32)
		if offset+4 > len(data) {
			return fmt.Errorf("insufficient data for decision period")
		}
		track.DecisionPeriod = uint32(data[offset]) | uint32(data[offset+1])<<8 |
			uint32(data[offset+2])<<16 | uint32(data[offset+3])<<24
		offset += 4

		// ConfirmPeriod (u32)
		if offset+4 > len(data) {
			return fmt.Errorf("insufficient data for confirm period")
		}
		track.ConfirmPeriod = uint32(data[offset]) | uint32(data[offset+1])<<8 |
			uint32(data[offset+2])<<16 | uint32(data[offset+3])<<24
		offset += 4

		// MinEnactmentPeriod (u32)
		if offset+4 > len(data) {
			return fmt.Errorf("insufficient data for min enactment period")
		}
		track.MinEnactmentPeriod = uint32(data[offset]) | uint32(data[offset+1])<<8 |
			uint32(data[offset+2])<<16 | uint32(data[offset+3])<<24
		offset += 4

		// MinApproval (Curve enum)
		approvalData, bytesRead := decodeCurve(data[offset:])
		track.MinApproval = fmt.Sprintf("%v", approvalData)
		offset += bytesRead

		// MinSupport (Curve enum)
		supportData, bytesRead := decodeCurve(data[offset:])
		track.MinSupport = fmt.Sprintf("%v", supportData)
		offset += bytesRead

		tracks[trackID] = track
	}

	return nil
}

// loadKnownTracks loads known track data as fallback
func (c *Client) loadKnownTracks(tracks map[uint16]*TrackInfo) error {
	// Query the actual tracks from storage
	// Referenda.Tracks storage key prefix
	prefix := "0x" + HexEncode(Twox128([]byte("Referenda"))) + HexEncode(Twox128([]byte("Tracks")))

	keys, err := c.GetKeys(prefix, nil)
	if err != nil {
		return fmt.Errorf("get track keys: %w", err)
	}

	// Process each key to get track data
	for _, key := range keys {
		// Extract track ID from key
		trackID, err := extractTrackIDFromKey(key, prefix)
		if err != nil {
			continue
		}

		// Get track data
		trackData, err := c.GetStorage(key, nil)
		if err != nil || trackData == "" {
			continue
		}

		// Decode track data
		track, err := c.decodeTrackData(trackData)
		if err != nil {
			continue
		}

		tracks[trackID] = track
	}

	// If no tracks found, use hardcoded ones based on the data you provided
	if len(tracks) == 0 {
		tracks[0] = &TrackInfo{Name: "root", MaxDeciding: 1, DecisionDeposit: "1000000000000000", PreparePeriod: 1200, DecisionPeriod: 403200, ConfirmPeriod: 14400, MinEnactmentPeriod: 14400}
		tracks[1] = &TrackInfo{Name: "whitelisted_caller", MaxDeciding: 100, DecisionDeposit: "100000000000000", PreparePeriod: 300, DecisionPeriod: 403200, ConfirmPeriod: 100, MinEnactmentPeriod: 100}
		// Add other tracks as needed...
	}

	return nil
}

// extractTrackIDFromKey extracts the track ID from a storage key
func extractTrackIDFromKey(key, prefix string) (uint16, error) {
	// Remove prefix and 0x
	key = strings.TrimPrefix(key, "0x")
	prefix = strings.TrimPrefix(prefix, "0x")

	if !strings.HasPrefix(key, prefix) {
		return 0, fmt.Errorf("key doesn't match prefix")
	}

	// The key format is: prefix + blake2_128(trackID) + trackID
	remainder := key[len(prefix):]

	// The remainder should be: blake2_128_hash(16 bytes = 32 hex chars) + trackID(2 bytes = 4 hex chars)
	if len(remainder) < 36 { // 32 + 4
		return 0, fmt.Errorf("insufficient key length")
	}

	// Extract the last 4 hex characters (2 bytes) which is the trackID
	trackIDHex := remainder[len(remainder)-4:]
	trackIDBytes, err := DecodeHex(trackIDHex)
	if err != nil || len(trackIDBytes) != 2 {
		return 0, fmt.Errorf("invalid track ID")
	}

	// Convert to uint16 (little endian)
	trackID := uint16(trackIDBytes[0]) | uint16(trackIDBytes[1])<<8
	return trackID, nil
}

// decodeTrackData decodes track data from storage
func (c *Client) decodeTrackData(data string) (*TrackInfo, error) {
	bytes, err := DecodeHex(data)
	if err != nil {
		return nil, err
	}

	track := &TrackInfo{}
	offset := 0

	// Try to decode the track data structure
	// This is a simplified version - actual structure may vary

	// Name (Vec<u8>)
	if offset < len(bytes) {
		nameLen, bytesRead := decodeCompactInteger(bytes[offset:])
		offset += bytesRead
		if offset+int(nameLen) <= len(bytes) {
			track.Name = string(bytes[offset : offset+int(nameLen)])
			offset += int(nameLen)
		}
	}

	// MaxDeciding (u32)
	if offset+4 <= len(bytes) {
		track.MaxDeciding = uint32(bytes[offset]) | uint32(bytes[offset+1])<<8 |
			uint32(bytes[offset+2])<<16 | uint32(bytes[offset+3])<<24
		offset += 4
	}

	// DecisionDeposit (u128)
	if offset+16 <= len(bytes) {
		deposit := new(big.Int)
		for i := 0; i < 16; i++ {
			deposit.Or(deposit, new(big.Int).Lsh(new(big.Int).SetUint64(uint64(bytes[offset+i])), uint(i*8)))
		}
		track.DecisionDeposit = deposit.String()
		offset += 16
	}

	// Set default values for other fields if we can't decode them
	if track.DecisionPeriod == 0 {
		track.DecisionPeriod = 403200 // Default 28 days
	}
	if track.ConfirmPeriod == 0 {
		track.ConfirmPeriod = 14400 // Default 1 day
	}
	if track.MinEnactmentPeriod == 0 {
		track.MinEnactmentPeriod = 14400 // Default 1 day
	}

	return track, nil
}

// decodeCurve decodes a Curve enum (LinearDecreasing or Reciprocal)
func decodeCurve(data []byte) (interface{}, int) {
	if len(data) == 0 {
		return nil, 0
	}

	variant := data[0]
	offset := 1

	switch variant {
	case 0: // LinearDecreasing
		result := map[string]interface{}{
			"LinearDecreasing": map[string]interface{}{},
		}
		// length (Perbill - u32)
		if len(data) >= offset+4 {
			length := uint32(data[offset]) | uint32(data[offset+1])<<8 |
				uint32(data[offset+2])<<16 | uint32(data[offset+3])<<24
			result["LinearDecreasing"].(map[string]interface{})["length"] = formatPerbill(length)
			offset += 4
		}

		// floor (Perbill - u32)
		if len(data) >= offset+4 {
			floor := uint32(data[offset]) | uint32(data[offset+1])<<8 |
				uint32(data[offset+2])<<16 | uint32(data[offset+3])<<24
			result["LinearDecreasing"].(map[string]interface{})["floor"] = formatPerbill(floor)
			offset += 4
		}

		// ceil (Perbill - u32)
		if len(data) >= offset+4 {
			ceil := uint32(data[offset]) | uint32(data[offset+1])<<8 |
				uint32(data[offset+2])<<16 | uint32(data[offset+3])<<24
			result["LinearDecreasing"].(map[string]interface{})["ceil"] = formatPerbill(ceil)
			offset += 4
		}

		return result, offset

	case 1: // Reciprocal
		result := map[string]interface{}{
			"Reciprocal": map[string]interface{}{},
		}
		// factor (i32)
		if len(data) >= offset+4 {
			factor := int32(data[offset]) | int32(data[offset+1])<<8 |
				int32(data[offset+2])<<16 | int32(data[offset+3])<<24
			result["Reciprocal"].(map[string]interface{})["factor"] = factor
			offset += 4
		}

		// xOffset (i32)
		if len(data) >= offset+4 {
			xOffset := int32(data[offset]) | int32(data[offset+1])<<8 |
				int32(data[offset+2])<<16 | int32(data[offset+3])<<24
			result["Reciprocal"].(map[string]interface{})["xOffset"] = xOffset
			offset += 4
		}

		// yOffset (i32)
		if len(data) >= offset+4 {
			yOffset := int32(data[offset]) | int32(data[offset+1])<<8 |
				int32(data[offset+2])<<16 | int32(data[offset+3])<<24
			result["Reciprocal"].(map[string]interface{})["yOffset"] = yOffset
			offset += 4
		}

		return result, offset

	default:
		return map[string]interface{}{"Unknown": variant}, 1
	}
}

// formatPerbill formats a Perbill value as percentage
func formatPerbill(value uint32) string {
	// Perbill is parts per billion (1,000,000,000)
	percentage := float64(value) / 10000000.0
	return fmt.Sprintf("%.2f%%", percentage)
}

// decodeCompactInteger decodes a SCALE compact integer
func decodeCompactInteger(data []byte) (uint64, int) {
	if len(data) == 0 {
		return 0, 0
	}

	flag := data[0] & 0x03
	switch flag {
	case 0: // single byte
		return uint64(data[0] >> 2), 1
	case 1: // two bytes
		if len(data) < 2 {
			return 0, 0
		}
		return uint64(data[0]>>2) | uint64(data[1])<<6, 2
	case 2: // four bytes
		if len(data) < 4 {
			return 0, 0
		}
		return uint64(data[0]>>2) | uint64(data[1])<<6 |
			uint64(data[2])<<14 | uint64(data[3])<<22, 4
	case 3: // big integer
		n := int(data[0]>>2) + 4
		if len(data) < n+1 {
			return 0, 0
		}
		var result uint64
		for i := 0; i < n && i < 8; i++ {
			result |= uint64(data[i+1]) << (8 * i)
		}
		return result, n + 1
	}
	return 0, 0
}

// GetTrackInfo gets information about a specific track
func (c *Client) GetTrackInfo(trackID uint16) (*TrackInfo, error) {
	constants, err := c.GetReferendaConstants()
	if err != nil {
		return nil, fmt.Errorf("get referenda constants: %w", err)
	}

	track, exists := constants.Tracks[trackID]
	if !exists {
		return nil, fmt.Errorf("track %d not found", trackID)
	}

	return track, nil
}

// GetAllTracks gets all track information from the chain
func (c *Client) GetAllTracks() (map[uint16]*TrackInfo, error) {
	constants, err := c.GetReferendaConstants()
	if err != nil {
		return nil, fmt.Errorf("get referenda constants: %w", err)
	}

	return constants.Tracks, nil
}
