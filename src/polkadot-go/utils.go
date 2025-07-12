package polkadot

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/centrifuge/go-substrate-rpc-client/v4/types"
)

// DecodeHex decodes a hex string to bytes
func DecodeHex(hexStr string) ([]byte, error) {
	cleaned := strings.TrimPrefix(hexStr, "0x")
	return hex.DecodeString(cleaned)
}

// DecodeBlockNumber extracts block number from header
func DecodeBlockNumber(header interface{}) (uint64, error) {
	// For gsrpc v4, header.Number is already a types.U64
	if h, ok := header.(*types.Header); ok {
		return uint64(h.Number), nil
	}
	return 0, fmt.Errorf("invalid header type")
}
