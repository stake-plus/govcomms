package polkadot

import (
	"fmt"

	"github.com/centrifuge/go-substrate-rpc-client/v4/scale"
)

// getOriginName maps the origin variant to a name based on track info
func (c *Client) getOriginName(variant uint8) string {
	// Try to get track info to map origins
	tracks, err := c.GetAllTracks()
	if err == nil {
		// Map track IDs to origin variants
		for trackID, track := range tracks {
			if uint8(trackID) == variant {
				return track.Name
			}
		}
	}

	// Fallback to common known origins
	origins := map[uint8]string{
		0:  "Root",
		1:  "WishForChange",
		10: "StakingAdmin",
		11: "Treasurer",
		12: "LeaseAdmin",
		13: "FellowshipAdmin",
		14: "GeneralAdmin",
		15: "AuctionAdmin",
		20: "ReferendumCanceller",
		21: "ReferendumKiller",
		30: "SmallTipper",
		31: "BigTipper",
		32: "SmallSpender",
		33: "MediumSpender",
		34: "BigSpender",
		35: "WhitelistedCaller",
	}

	if name, ok := origins[variant]; ok {
		return name
	}
	return fmt.Sprintf("Origin(%d)", variant)
}

// GetOriginsInfo returns the mapping of origin IDs to names
func (c *Client) GetOriginsInfo() (map[uint8]string, error) {
	// Get track info which contains origin names
	tracks, err := c.GetAllTracks()
	if err != nil {
		return nil, fmt.Errorf("get tracks: %w", err)
	}

	origins := make(map[uint8]string)
	for trackID, track := range tracks {
		origins[uint8(trackID)] = track.Name
	}

	return origins, nil
}

// decodeOrigin decodes the origin from referendum data
func decodeOrigin(decoder *scale.Decoder) (string, error) {
	// First byte is the origin type
	originType, err := decoder.ReadOneByte()
	if err != nil {
		return "", fmt.Errorf("read origin type: %w", err)
	}

	if originType == 0 { // system
		// System origin doesn't have additional data
		return "system", nil
	} else if originType == 1 { // Origins pallet
		// Read the origin variant
		originVariant, err := decoder.ReadOneByte()
		if err != nil {
			return "", fmt.Errorf("read origin variant: %w", err)
		}
		// We'll need the client context to properly map this
		// For now, use the static mapping
		return getOriginNameStatic(originVariant), nil
	} else if originType == 2 { // Void
		return "Void", nil
	} else {
		// Try to read as Origins variant directly
		// Some older referenda might encode the origin differently
		return getOriginNameStatic(originType), nil
	}
}

// getOriginNameStatic provides static fallback origin names
func getOriginNameStatic(variant uint8) string {
	origins := map[uint8]string{
		0:  "Root",
		1:  "WishForChange",
		10: "StakingAdmin",
		11: "Treasurer",
		12: "LeaseAdmin",
		13: "FellowshipAdmin",
		14: "GeneralAdmin",
		15: "AuctionAdmin",
		20: "ReferendumCanceller",
		21: "ReferendumKiller",
		30: "SmallTipper",
		31: "BigTipper",
		32: "SmallSpender",
		33: "MediumSpender",
		34: "BigSpender",
		35: "WhitelistedCaller",
	}

	if name, ok := origins[variant]; ok {
		return name
	}
	return fmt.Sprintf("Origin(%d)", variant)
}
