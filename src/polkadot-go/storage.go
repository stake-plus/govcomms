package polkadot

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"log"

	"github.com/OneOfOne/xxhash"
	"golang.org/x/crypto/blake2b"
)

// StorageKey creates a storage key for a pallet and item
func StorageKey(pallet, item string) string {
	key := append(Twox128([]byte(pallet)), Twox128([]byte(item))...)
	return "0x" + hex.EncodeToString(key)
}

// StorageKeyWithHashedKey creates a storage key with a hashed key parameter
func StorageKeyWithHashedKey(pallet, item string, keyData []byte) string {
	key := append(Twox128([]byte(pallet)), Twox128([]byte(item))...)
	// For Blake2_128_Concat hasher
	hashedKey := append(Blake2_128(keyData), keyData...)
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

// Blake2_128 implements Blake2b 128-bit hash
func Blake2_128(data []byte) []byte {
	h, err := blake2b.New(16, nil)
	if err != nil {
		// blake2b.New should never fail with valid size, but handle gracefully
		// Use a fallback: create a new hash on each call if initialization fails
		// This should never happen in practice, but prevents panic
		log.Printf("polkadot: blake2b.New(16) failed: %v", err)
		// Return empty hash as fallback (callers should handle this)
		return make([]byte, 16)
	}
	h.Write(data)
	return h.Sum(nil)
}

// Blake2_256 implements Blake2b 256-bit hash
func Blake2_256(data []byte) []byte {
	h, err := blake2b.New256(nil)
	if err != nil {
		// blake2b.New256 should never fail, but handle gracefully
		// This should never happen in practice, but prevents panic
		log.Printf("polkadot: blake2b.New256 failed: %v", err)
		// Return empty hash as fallback (callers should handle this)
		return make([]byte, 32)
	}
	h.Write(data)
	return h.Sum(nil)
}

// HexEncode encodes bytes to hex string without 0x prefix
func HexEncode(data []byte) string {
	return hex.EncodeToString(data)
}

// generateRandomBytes generates random bytes of given length
func generateRandomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	return b, err
}

// Interface for hashers
type Hasher interface {
	Hash(data []byte) []byte
	IsBlake2() bool
	RequiresConcat() bool
}

// Blake2_128Concat hasher
type Blake2_128Concat struct{}

func (h Blake2_128Concat) Hash(data []byte) []byte {
	return append(Blake2_128(data), data...)
}

func (h Blake2_128Concat) IsBlake2() bool {
	return true
}

func (h Blake2_128Concat) RequiresConcat() bool {
	return true
}

// Twox64Concat hasher
type Twox64Concat struct{}

func (h Twox64Concat) Hash(data []byte) []byte {
	return append(Twox64(data), data...)
}

func (h Twox64Concat) IsBlake2() bool {
	return false
}

func (h Twox64Concat) RequiresConcat() bool {
	return true
}

// Identity hasher (no hashing)
type Identity struct{}

func (h Identity) Hash(data []byte) []byte {
	return data
}

func (h Identity) IsBlake2() bool {
	return false
}

func (h Identity) RequiresConcat() bool {
	return false
}
