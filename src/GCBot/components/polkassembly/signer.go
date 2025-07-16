package polkassembly

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/ChainSafe/go-schnorrkel"
	"github.com/cosmos/go-bip39"
	"github.com/mr-tron/base58"
	"golang.org/x/crypto/blake2b"
)

// PolkadotSigner implements the Signer interface for Polkadot accounts
type PolkadotSigner struct {
	privateKey *schnorrkel.SecretKey
	publicKey  *schnorrkel.PublicKey
	address    string
}

// NewPolkadotSignerFromSeed creates a new Polkadot signer from a seed phrase
func NewPolkadotSignerFromSeed(seedPhrase string, network uint16) (*PolkadotSigner, error) {
	// Clean up the seed phrase - trim spaces and normalize
	seedPhrase = strings.TrimSpace(seedPhrase)

	// Validate the mnemonic
	if !bip39.IsMnemonicValid(seedPhrase) {
		words := strings.Fields(seedPhrase)
		return nil, fmt.Errorf("invalid seed phrase: got %d words, expected 12, 15, 18, 21, or 24", len(words))
	}

	// For Polkadot.js sr25519 compatibility:
	// When no derivation path is specified, it uses the mnemonic phrase directly
	// to generate the seed (not using BIP39 seed generation)

	// Generate mini secret by hashing the mnemonic
	// This matches @polkadot/util-crypto behavior for sr25519
	miniSecret := blake2b256([]byte(seedPhrase))

	miniSecretKey, err := schnorrkel.NewMiniSecretKeyFromRaw(miniSecret)
	if err != nil {
		return nil, fmt.Errorf("create mini secret key: %w", err)
	}

	secretKey := miniSecretKey.ExpandEd25519()
	publicKey, err := secretKey.Public()
	if err != nil {
		return nil, fmt.Errorf("get public key: %w", err)
	}

	// Generate SS58 address
	// Network specific prefixes: Polkadot = 0, Kusama = 2, Generic Substrate = 42
	prefix := uint16(42) // Default to generic
	if network == 0 {    // Polkadot
		prefix = 0
	} else if network == 2 { // Kusama
		prefix = 2
	}

	address := publicKeyToSS58(publicKey, prefix)

	return &PolkadotSigner{
		privateKey: secretKey,
		publicKey:  publicKey,
		address:    address,
	}, nil
}

// blake2b256 returns blake2b 256 bit hash
func blake2b256(data []byte) [32]byte {
	h, _ := blake2b.New256(nil)
	h.Write(data)
	var result [32]byte
	copy(result[:], h.Sum(nil))
	return result
}

// NewPolkadotSignerFromHex creates a signer from a hex-encoded private key
func NewPolkadotSignerFromHex(hexKey string, network uint16) (*PolkadotSigner, error) {
	hexKey = strings.TrimPrefix(hexKey, "0x")
	keyBytes, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("decode hex key: %w", err)
	}

	if len(keyBytes) != 32 {
		return nil, fmt.Errorf("invalid key length: expected 32 bytes, got %d", len(keyBytes))
	}

	var miniSecret [32]byte
	copy(miniSecret[:], keyBytes)

	miniSecretKey, err := schnorrkel.NewMiniSecretKeyFromRaw(miniSecret)
	if err != nil {
		return nil, fmt.Errorf("create mini secret key: %w", err)
	}

	secretKey := miniSecretKey.ExpandEd25519()
	publicKey, err := secretKey.Public()
	if err != nil {
		return nil, fmt.Errorf("get public key: %w", err)
	}

	// Generate SS58 address with network specific prefix
	prefix := uint16(42) // Default to generic
	if network == 0 {    // Polkadot
		prefix = 0
	} else if network == 2 { // Kusama
		prefix = 2
	}

	address := publicKeyToSS58(publicKey, prefix)

	return &PolkadotSigner{
		privateKey: secretKey,
		publicKey:  publicKey,
		address:    address,
	}, nil
}

// Sign signs a message using sr25519
func (s *PolkadotSigner) Sign(message []byte) ([]byte, error) {
	transcript := schnorrkel.NewSigningContext([]byte("substrate"), message)
	sig, err := s.privateKey.Sign(transcript)
	if err != nil {
		return nil, fmt.Errorf("sign message: %w", err)
	}

	// Convert [64]byte to []byte
	sigBytes := sig.Encode()
	return sigBytes[:], nil
}

// Address returns the SS58 encoded address
func (s *PolkadotSigner) Address() string {
	return s.address
}

// publicKeyToSS58 converts a public key to SS58 format
func publicKeyToSS58(pubKey *schnorrkel.PublicKey, prefix uint16) string {
	// Create the payload: prefix + public key + checksum
	payload := make([]byte, 0, 35)

	// Add prefix
	if prefix < 64 {
		payload = append(payload, byte(prefix))
	} else {
		payload = append(payload, 0x40|((byte(prefix>>8))&0x3f))
		payload = append(payload, byte(prefix&0xff))
	}

	// Add public key
	pubKeyBytes := pubKey.Encode()
	payload = append(payload, pubKeyBytes[:]...)

	// Calculate checksum
	checksumInput := []byte("SS58PRE")
	if prefix < 64 {
		checksumInput = append(checksumInput, byte(prefix))
	} else {
		checksumInput = append(checksumInput, 0x40|((byte(prefix>>8))&0x3f))
		checksumInput = append(checksumInput, byte(prefix&0xff))
	}
	checksumInput = append(checksumInput, pubKeyBytes[:]...)

	h, _ := blake2b.New(64, nil)
	h.Write(checksumInput)
	checksum := h.Sum(nil)

	// Append first 2 bytes of checksum
	payload = append(payload, checksum[0:2]...)

	// Base58 encode
	return base58.Encode(payload)
}
