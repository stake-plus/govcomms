package webserver

import (
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"time"

	schnorrkel "github.com/ChainSafe/go-schnorrkel"
	"github.com/golang-jwt/jwt/v5"
	"github.com/mr-tron/base58"
)

// decodeSS58 converts an SS58‑formatted address to the raw 32‑byte public key.
func decodeSS58(addr string) ([]byte, error) {
	// Handle both SS58 format and hex format (0x...)
	if strings.HasPrefix(addr, "0x") {
		return hex.DecodeString(addr[2:])
	}

	raw, err := base58.Decode(addr)
	if err != nil || len(raw) < 35 {
		return nil, fmt.Errorf("invalid ss58 address")
	}
	return raw[1:33], nil // drop 1‑byte prefix & 2‑byte checksum
}

func strip0x(s string) string {
	if len(s) > 1 && s[:2] == "0x" {
		return s[2:]
	}
	return s
}

func verifySignature(addr, sigHex, nonce string) error {
	log.Printf("Verifying signature for address: %s", addr)

	pubKeyBytes, err := decodeSS58(addr)
	if err != nil {
		log.Printf("Failed to decode address %s: %v", addr, err)
		return err
	}
	if len(pubKeyBytes) != 32 {
		return fmt.Errorf("invalid public key length: %d", len(pubKeyBytes))
	}

	sigBytes, err := hex.DecodeString(strip0x(sigHex))
	if err != nil {
		log.Printf("Failed to decode signature: %v", err)
		return err
	}
	if len(sigBytes) != 64 {
		return fmt.Errorf("invalid signature length: %d", len(sigBytes))
	}

	var pkRaw [32]byte
	copy(pkRaw[:], pubKeyBytes)
	var sigRaw [64]byte
	copy(sigRaw[:], sigBytes)

	var pk schnorrkel.PublicKey
	if err = pk.Decode(pkRaw); err != nil {
		log.Printf("Failed to decode public key: %v", err)
		return err
	}

	var sig schnorrkel.Signature
	if err = sig.Decode(sigRaw); err != nil {
		log.Printf("Failed to decode signature: %v", err)
		return err
	}

	ctx := schnorrkel.NewSigningContext([]byte("substrate"), []byte(nonce))
	valid, err := pk.Verify(&sig, ctx)
	if err != nil {
		log.Printf("Signature verification error: %v", err)
		return err
	}
	if !valid {
		log.Printf("Signature verification failed - signature is invalid")
		return fmt.Errorf("signature verification failed")
	}

	log.Printf("Signature verification successful")
	return nil
}

func issueJWT(addr string, secret []byte) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"addr": addr,
		"exp":  time.Now().Add(time.Hour).Unix(),
	})
	return token.SignedString(secret)
}
