package polkassembly

import (
	"encoding/hex"
	"fmt"
	"strings"

	subkey "github.com/vedhavyas/go-subkey/v2"
	"github.com/vedhavyas/go-subkey/v2/sr25519"
)

// PolkadotSigner implements the Signer interface using sr25519 keys.
type PolkadotSigner struct {
	keyPair       subkey.KeyPair
	address       string
	networkPrefix uint16
}

// NewPolkadotSignerFromSeed constructs a signer from a mnemonic or URI.
func NewPolkadotSignerFromSeed(seedPhrase string, networkPrefix uint16) (*PolkadotSigner, error) {
	seedPhrase = strings.TrimSpace(seedPhrase)
	if seedPhrase == "" {
		return nil, fmt.Errorf("seed phrase cannot be empty")
	}

	keyPair, err := subkey.DeriveKeyPair(sr25519.Scheme{}, seedPhrase)
	if err != nil {
		return nil, fmt.Errorf("derive sr25519 keypair: %w", err)
	}

	return &PolkadotSigner{
		keyPair:       keyPair,
		address:       keyPair.SS58Address(networkPrefix),
		networkPrefix: networkPrefix,
	}, nil
}

// NewPolkadotSignerFromHex constructs a signer from a hex encoded secret.
func NewPolkadotSignerFromHex(hexKey string, networkPrefix uint16) (*PolkadotSigner, error) {
	hexKey = strings.TrimSpace(strings.TrimPrefix(hexKey, "0x"))
	keyBytes, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("decode hex key: %w", err)
	}
	if len(keyBytes) != 32 {
		return nil, fmt.Errorf("invalid key length: expected 32 bytes, got %d", len(keyBytes))
	}

	keyPair, err := sr25519.Scheme{}.FromSeed(keyBytes)
	if err != nil {
		return nil, fmt.Errorf("load sr25519 seed: %w", err)
	}

	return &PolkadotSigner{
		keyPair:       keyPair,
		address:       keyPair.SS58Address(networkPrefix),
		networkPrefix: networkPrefix,
	}, nil
}

// Sign signs the provided message using sr25519.
func (s *PolkadotSigner) Sign(message []byte) ([]byte, error) {
	if s == nil || s.keyPair == nil {
		return nil, fmt.Errorf("signer not initialized")
	}
	sig, err := s.keyPair.Sign(message)
	if err != nil {
		return nil, fmt.Errorf("sign message: %w", err)
	}
	return sig, nil
}

// Address returns the SS58 encoded address for this signer.
func (s *PolkadotSigner) Address() string {
	if s == nil {
		return ""
	}
	return s.address
}
