package webserver

import (
	"encoding/hex"
	"fmt"
	"time"

	schnorrkel "github.com/ChainSafe/go-schnorrkel"
	"github.com/golang-jwt/jwt/v5"
	"github.com/mr-tron/base58"
)

// decodeSS58 converts an SS58‑formatted address to the raw 32‑byte public key.
func decodeSS58(addr string) ([]byte, error) {
	raw, err := base58.Decode(addr)
	if err != nil || len(raw) < 35 {
		return nil, fmt.Errorf("invalid ss58 address")
	}
	return raw[1:33], nil // strip prefix & checksum
}

func strip0x(s string) string {
	if len(s) > 1 && s[:2] == "0x" {
		return s[2:]
	}
	return s
}

func verifySignature(addr, sigHex, nonce string) error {
	pubKeyBytes, err := decodeSS58(addr)
	if err != nil {
		return err
	}
	if len(pubKeyBytes) != 32 {
		return fmt.Errorf("invalid public key length")
	}

	sigBytes, err := hex.DecodeString(strip0x(sigHex))
	if err != nil {
		return err
	}
	if len(sigBytes) != 64 {
		return fmt.Errorf("invalid signature length")
	}

	var pkRaw [32]byte
	copy(pkRaw[:], pubKeyBytes)

	var sigRaw [64]byte
	copy(sigRaw[:], sigBytes)

	var pk schnorrkel.PublicKey
	if err = pk.Decode(pkRaw); err != nil {
		return err
	}

	var sig schnorrkel.Signature
	if err = sig.Decode(sigRaw); err != nil {
		return err
	}

	ctx := schnorrkel.NewSigningContext([]byte("substrate"), []byte(nonce))
	valid := pk.Verify(&sig, ctx)
	if !valid {
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
