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
// * polkadot.js extension signs the TEXT string wrapped as <Bytes>NONCE</Bytes>
// * The nonce IS the message - as a string, not decoded hex!
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

	// Convert to schnorrkel types
	var pkArr [32]byte
	copy(pkArr[:], pub)
	var sigArr [64]byte
	copy(sigArr[:], rawSig)

	var pk schnorrkel.PublicKey
	if err = pk.Decode(pkArr); err != nil {
		return fmt.Errorf("failed to decode public key: %w", err)
	}

	var sig schnorrkel.Signature
	if err = sig.Decode(sigArr); err != nil {
		return fmt.Errorf("failed to decode signature: %w", err)
	}

	// Try wrapped format first (polkadot.js extension style)
	// The extension signs the TEXT: <Bytes>NONCE</Bytes>
	wrappedMsg := []byte(fmt.Sprintf("<Bytes>%s</Bytes>", nonce))
	ctxWrapped := schnorrkel.NewSigningContext([]byte("substrate"), wrappedMsg)
	ok, err := pk.Verify(&sig, ctxWrapped)
	if err == nil && ok {
		return nil
	}

	// Try unwrapped format - just the nonce string itself
	msgBytes := []byte(nonce)
	ctx := schnorrkel.NewSigningContext([]byte("substrate"), msgBytes)
	ok, err = pk.Verify(&sig, ctx)
	if err == nil && ok {
		return nil
	}

	return fmt.Errorf("signature verification failed")
}

func issueJWT(addr string, secret []byte) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"addr": addr,
		"exp":  time.Now().Add(time.Hour).Unix(),
	})
	return token.SignedString(secret)
}
