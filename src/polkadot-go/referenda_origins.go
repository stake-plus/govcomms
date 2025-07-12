package polkadot

import (
	"fmt"

	"github.com/centrifuge/go-substrate-rpc-client/v4/scale"
)

// getOriginName maps the origin variant to a name
func getOriginName(variant uint8) string {
	// These map to the Polkadot runtime Origins enum
	// Updated based on actual Polkadot runtime
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
	// Polkadot uses a fixed set of origins in the runtime
	// These are defined in the Origins pallet
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
		return getOriginName(originVariant), nil
	} else if originType == 2 { // Void
		return "Void", nil
	} else {
		// Try to read as Origins variant directly
		// Some older referenda might encode the origin differently
		return getOriginName(originType), nil
	}
}
