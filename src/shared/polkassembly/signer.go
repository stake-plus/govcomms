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

// PolkadotSigner implements the Signer interface using sr25519 keys.
type PolkadotSigner struct {
	privateKey *schnorrkel.SecretKey
	publicKey  *schnorrkel.PublicKey
	address    string
}

// NewPolkadotSignerFromSeed constructs a signer from a mnemonic seed phrase.
func NewPolkadotSignerFromSeed(seedPhrase string, networkPrefix uint16) (*PolkadotSigner, error) {
	seed, err := bip39.NewSeedWithErrorChecking(seedPhrase, "")
	if err != nil {
		return nil, fmt.Errorf("invalid seed phrase: %w", err)
	}

	if len(seed) < 32 {
		return nil, fmt.Errorf("seed too short")
	}

	var miniSecret [32]byte
	copy(miniSecret[:], seed[:32])

	miniSecretKey, err := schnorrkel.NewMiniSecretKeyFromRaw(miniSecret)
	if err != nil {
		return nil, fmt.Errorf("create mini secret key: %w", err)
	}

	secretKey := miniSecretKey.ExpandEd25519()
	publicKey, err := secretKey.Public()
	if err != nil {
		return nil, fmt.Errorf("get public key: %w", err)
	}

	address := publicKeyToSS58(publicKey, networkPrefix)

	return &PolkadotSigner{
		privateKey: secretKey,
		publicKey:  publicKey,
		address:    address,
	}, nil
}

// NewPolkadotSignerFromHex constructs a signer from a hex encoded secret.
func NewPolkadotSignerFromHex(hexKey string, networkPrefix uint16) (*PolkadotSigner, error) {
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

	address := publicKeyToSS58(publicKey, networkPrefix)

	return &PolkadotSigner{
		privateKey: secretKey,
		publicKey:  publicKey,
		address:    address,
	}, nil
}

// Sign signs the provided message using sr25519.
func (s *PolkadotSigner) Sign(message []byte) ([]byte, error) {
	context := schnorrkel.NewSigningContext([]byte("substrate"), message)
	sig, err := s.privateKey.Sign(context)
	if err != nil {
		return nil, fmt.Errorf("sign message: %w", err)
	}

	encoded := sig.Encode()
	return encoded[:], nil
}

// Address returns the SS58 encoded address for this signer.
func (s *PolkadotSigner) Address() string {
	return s.address
}

func publicKeyToSS58(pubKey *schnorrkel.PublicKey, prefix uint16) string {
	payload := make([]byte, 0, 35)

	if prefix < 64 {
		payload = append(payload, byte(prefix))
	} else {
		payload = append(payload, 0x40|((byte(prefix>>8))&0x3f))
		payload = append(payload, byte(prefix&0xff))
	}

	pubKeyBytes := pubKey.Encode()
	payload = append(payload, pubKeyBytes[:]...)

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

	payload = append(payload, checksum[0:2]...)

	return base58.Encode(payload)
}
