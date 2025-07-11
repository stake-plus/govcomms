package webserver

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	schnorrkel "github.com/ChainSafe/go-schnorrkel"
	"github.com/golang-jwt/jwt/v5"
	"github.com/mr-tron/base58"
)

// decodeSS58 converts an SS58 address → raw 32-byte pubkey.
func decodeSS58(addr string) ([]byte, error) {
	if strings.HasPrefix(addr, "0x") {
		return hex.DecodeString(addr[2:])
	}
	raw, err := base58.Decode(addr)
	if err != nil || len(raw) < 35 {
		return nil, fmt.Errorf("invalid ss58 address")
	}
	return raw[1:33], nil
}

func strip0x(s string) string {
	if strings.HasPrefix(s, "0x") {
		return s[2:]
	}
	return s
}

// verifySignature matches polkadot-extension `signRaw`:
func verifySignature(addr, sigHex, nonce string) error {
	// Decode public key from address
	pub, err := decodeSS58(addr)
	if err != nil {
		return fmt.Errorf("failed to decode address: %w", err)
	}

	// Decode signature
	rawSig, err := hex.DecodeString(strip0x(sigHex))
	if err != nil {
		return fmt.Errorf("failed to decode signature: %w", err)
	}

	// Drop type prefix when present (65-byte sig)
	if len(rawSig) == 65 {
		rawSig = rawSig[1:]
	}
	if len(rawSig) != 64 {
		return fmt.Errorf("invalid signature length: %d", len(rawSig))
	}

	// Convert public key bytes to PublicKey
	var pubKeyBytes [32]byte
	copy(pubKeyBytes[:], pub)

	pubKey := &schnorrkel.PublicKey{}
	err = pubKey.Decode(pubKeyBytes)
	if err != nil {
		return fmt.Errorf("failed to decode public key: %w", err)
	}

	// Convert signature bytes to Signature
	var sigBytes [64]byte
	copy(sigBytes[:], rawSig)

	sig := &schnorrkel.Signature{}
	err = sig.Decode(sigBytes)
	if err != nil {
		return fmt.Errorf("failed to decode signature: %w", err)
	}

	// Polkadot.js signs: <Bytes>NONCE</Bytes> where NONCE includes 0x prefix
	message := []byte(fmt.Sprintf("<Bytes>%s</Bytes>", nonce))

	// Create signing context
	transcript := schnorrkel.NewSigningContext([]byte("substrate"), message)

	// Verify
	ok, err := pubKey.Verify(sig, transcript)
	if err != nil {
		return fmt.Errorf("verification error: %w", err)
	}
	if !ok {
		return fmt.Errorf("signature verification failed")
	}

	return nil
}

func issueJWT(addr string, secret []byte) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"addr": addr,
		"exp":  time.Now().Add(time.Hour).Unix(),
	})
	return token.SignedString(secret)
}
