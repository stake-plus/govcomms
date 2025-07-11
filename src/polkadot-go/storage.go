package polkadot

import (
	"encoding/binary"
	"encoding/hex"

	"github.com/OneOfOne/xxhash"
)

// StorageKey creates a storage key for a pallet and item
func StorageKey(pallet, item string) string {
	key := append(Twox128([]byte(pallet)), Twox128([]byte(item))...)
	return "0x" + hex.EncodeToString(key)
}

// StorageKeyWithHashedKey creates a storage key with a hashed key parameter
func StorageKeyWithHashedKey(pallet, item string, keyData []byte) string {
	key := append(Twox128([]byte(pallet)), Twox128([]byte(item))...)
	// Hash the key data and append it
	hashedKey := append(Twox64(keyData), keyData...)
	key = append(key, hashedKey...)
	return "0x" + hex.EncodeToString(key)
}

// StorageKeyUint32 creates a storage key for a uint32 parameter
func StorageKeyUint32(pallet, item string, value uint32) string {
	keyData := make([]byte, 4)
	binary.LittleEndian.PutUint32(keyData, value)
	return StorageKeyWithHashedKey(pallet, item, keyData)
}

// Twox128 implements the TwoX 128-bit hash
func Twox128(data []byte) []byte {
	hash1 := xxhash.NewS64(0)
	hash1.Write(data)
	hash2 := xxhash.NewS64(1)
	hash2.Write(data)

	out := make([]byte, 16)
	binary.LittleEndian.PutUint64(out[0:], hash1.Sum64())
	binary.LittleEndian.PutUint64(out[8:], hash2.Sum64())
	return out
}

// Twox64 implements the TwoX 64-bit hash
func Twox64(data []byte) []byte {
	hash := xxhash.NewS64(0)
	hash.Write(data)

	out := make([]byte, 8)
	binary.LittleEndian.PutUint64(out, hash.Sum64())
	return out
}

// Blake2_128 implements Blake2 128-bit hash
func Blake2_128(data []byte) []byte {
	// For now, using xxhash as placeholder - you'd want proper blake2
	return Twox128(data)
}

// Blake2_256 implements Blake2 256-bit hash
func Blake2_256(data []byte) []byte {
	// For now, using xxhash as placeholder - you'd want proper blake2
	hash1 := Twox128(data)
	hash2 := Twox128(append(data, 0x01))
	return append(hash1, hash2...)
}
