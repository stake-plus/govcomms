package polkadot

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
)

// DecodeHex decodes a hex string, handling 0x prefix
func DecodeHex(hexStr string) ([]byte, error) {
	hexStr = strings.TrimPrefix(hexStr, "0x")
	return hex.DecodeString(hexStr)
}

// DecodeU32 decodes a little-endian u32 from hex
func DecodeU32(hexStr string) (uint32, error) {
	data, err := DecodeHex(hexStr)
	if err != nil {
		return 0, err
	}
	if len(data) < 4 {
		return 0, fmt.Errorf("insufficient data for u32")
	}
	return binary.LittleEndian.Uint32(data[:4]), nil
}

// DecodeU64 decodes a little-endian u64 from hex
func DecodeU64(hexStr string) (uint64, error) {
	data, err := DecodeHex(hexStr)
	if err != nil {
		return 0, err
	}
	if len(data) < 8 {
		return 0, fmt.Errorf("insufficient data for u64")
	}
	return binary.LittleEndian.Uint64(data[:8]), nil
}

// DecodeU128 decodes a little-endian u128 from hex
func DecodeU128(hexStr string) (*big.Int, error) {
	data, err := DecodeHex(hexStr)
	if err != nil {
		return nil, err
	}
	if len(data) < 16 {
		return nil, fmt.Errorf("insufficient data for u128")
	}
	// Little-endian to big.Int
	reversed := make([]byte, 16)
	for i := 0; i < 16; i++ {
		reversed[i] = data[15-i]
	}
	return new(big.Int).SetBytes(reversed), nil
}

// DecodeCompact decodes a SCALE compact integer
func DecodeCompact(data []byte) (uint64, int, error) {
	if len(data) == 0 {
		return 0, 0, fmt.Errorf("empty data")
	}

	flag := data[0] & 0x03

	switch flag {
	case 0: // single byte
		return uint64(data[0] >> 2), 1, nil
	case 1: // two bytes
		if len(data) < 2 {
			return 0, 0, fmt.Errorf("insufficient data")
		}
		return uint64(binary.LittleEndian.Uint16(data[:2]) >> 2), 2, nil
	case 2: // four bytes
		if len(data) < 4 {
			return 0, 0, fmt.Errorf("insufficient data")
		}
		return uint64(binary.LittleEndian.Uint32(data[:4]) >> 2), 4, nil
	case 3: // big integer
		n := int(data[0]>>2) + 4
		if len(data) < n+1 {
			return 0, 0, fmt.Errorf("insufficient data")
		}
		// For simplicity, only handle up to 8 bytes
		if n > 8 {
			return 0, 0, fmt.Errorf("compact integer too large")
		}
		var result uint64
		for i := 0; i < n && i < 8; i++ {
			result |= uint64(data[i+1]) << (8 * i)
		}
		return result, n + 1, nil
	}

	return 0, 0, fmt.Errorf("invalid compact encoding")
}

// DecodeBlockNumber decodes a block number from hex header
func DecodeBlockNumber(hexStr string) (uint64, error) {
	// Block numbers in headers are hex encoded
	hexStr = strings.TrimPrefix(hexStr, "0x")
	blockNum := new(big.Int)
	blockNum.SetString(hexStr, 16)
	return blockNum.Uint64(), nil
}
