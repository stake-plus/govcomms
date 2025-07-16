package polkassembly

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/ChainSafe/go-schnorrkel"
	"github.com/vedhavyas/go-subkey/v2"
	"github.com/vedhavyas/go-subkey/v2/sr25519"
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

	// Use go-subkey to derive the keypair from mnemonic
	// This library is specifically designed for Substrate compatibility
	scheme := sr25519.Scheme{}

	// Create derivation path URI (mnemonic without path)
	uri := seedPhrase

	// Derive keypair from URI
	kp, err := subkey.DeriveKeyPair(scheme, uri)
	if err != nil {
		return nil, fmt.Errorf("derive keypair: %w", err)
	}

	// Get the secret key bytes
	secretBytes := kp.Seed()
	if len(secretBytes) != 32 {
		return nil, fmt.Errorf("unexpected secret key length: %d", len(secretBytes))
	}

	// Create schnorrkel secret key from the seed
	var miniSecret [32]byte
	copy(miniSecret[:], secretBytes)

	miniSecretKey, err := schnorrkel.NewMiniSecretKeyFromRaw(miniSecret)
	if err != nil {
		return nil, fmt.Errorf("create mini secret key: %w", err)
	}

	secretKey := miniSecretKey.ExpandEd25519()
	publicKey, err := secretKey.Public()
	if err != nil {
		return nil, fmt.Errorf("get public key: %w", err)
	}

	// Get SS58 address using the network prefix
	var ss58Format uint16
	switch network {
	case 0: // Polkadot
		ss58Format = 0
	case 2: // Kusama
		ss58Format = 2
	default:
		ss58Format = 42 // Generic substrate
	}

	// Use go-subkey's SS58 encoding which matches Polkadot.js exactly
	address := kp.SS58Address(ss58Format)

	return &PolkadotSigner{
		privateKey: secretKey,
		publicKey:  publicKey,
		address:    address,
	}, nil
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

	// Create URI from hex seed
	uri := "0x" + hexKey

	// Create keypair from seed using go-subkey
	scheme := sr25519.Scheme{}
	kp, err := subkey.DeriveKeyPair(scheme, uri)
	if err != nil {
		return nil, fmt.Errorf("create keypair from hex: %w", err)
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

	// Get SS58 address
	var ss58Format uint16
	switch network {
	case 0: // Polkadot
		ss58Format = 0
	case 2: // Kusama
		ss58Format = 2
	default:
		ss58Format = 42 // Generic substrate
	}

	address := kp.SS58Address(ss58Format)

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
