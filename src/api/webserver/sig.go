package webserver

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	schnorrkel "github.com/ChainSafe/go-schnorrkel"
	"github.com/golang-jwt/jwt/v5"
	"github.com/mr-tron/base58"
)

// ───────────────────────── helpers ─────────────────────────

// decodeSS58 extracts the 32-byte pubkey from an SS58 (or 0x…) address.
func decodeSS58(addr string) ([]byte, error) {
	if strings.HasPrefix(addr, "0x") {
		return hex.DecodeString(addr[2:])
	}
	raw, err := base58.Decode(addr)
	if err != nil {
		return nil, err
	}
	if len(raw) < 35 {
		return nil, errors.New("invalid SS58 length")
	}
	return raw[1:33], nil
}

// buildSr25519Message reproduces exactly what polkadot-js signs.
func buildSr25519Message(nonce string) ([]byte, error) {
	hexPart := strings.TrimPrefix(nonce, "0x")
	nonceBytes, err := hex.DecodeString(hexPart)
	if err != nil {
		return nil, fmt.Errorf("invalid nonce hex: %w", err)
	}
	msg := make([]byte, 0, len(nonceBytes)+14)
	msg = append(msg, []byte("<Bytes>")...)
	msg = append(msg, nonceBytes...)
	msg = append(msg, []byte("</Bytes>")...)
	return msg, nil
}

// ─────────────────────── public API ───────────────────────

// verifySignature validates sr25519 signatures produced by polkadot-js signRaw.
func verifySignature(address, sigHex, nonce string) error {
	// 1. pubkey
	pub, err := decodeSS58(address)
	if err != nil {
		return fmt.Errorf("decode address: %w", err)
	}
	if len(pub) != 32 {
		return fmt.Errorf("unexpected pubkey length %d", len(pub))
	}
	var pubArr [32]byte
	copy(pubArr[:], pub)
	var pk schnorrkel.PublicKey
	if err = pk.Decode(pubArr); err != nil {
		return fmt.Errorf("decode pubkey: %w", err)
	}

	// 2. signature
	rawSig, err := hex.DecodeString(strings.TrimPrefix(sigHex, "0x"))
	if err != nil {
		return fmt.Errorf("decode sig hex: %w", err)
	}
	if len(rawSig) == 65 { // drop prefix (0x00)
		rawSig = rawSig[1:]
	}
	if len(rawSig) != 64 {
		return fmt.Errorf("unexpected sig length %d", len(rawSig))
	}
	var sigArr [64]byte
	copy(sigArr[:], rawSig)
	var sig schnorrkel.Signature
	if err = sig.Decode(sigArr); err != nil {
		return fmt.Errorf("decode sig: %w", err)
	}

	// 3. message + context
	message, err := buildSr25519Message(nonce)
	if err != nil {
		return err
	}
	ctx := schnorrkel.NewSigningContext([]byte("substrate"), message)

	// 4. verify
	ok, err := pk.Verify(&sig, ctx)
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	if !ok {
		return errors.New("signature verification failed")
	}
	return nil
}

// issueJWT matches the original call-site signature in auth.go.
func issueJWT(addr string, secret []byte) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"addr": addr,
		"iat":  time.Now().Unix(),
		"exp":  time.Now().Add(time.Hour).Unix(),
	})
	return token.SignedString(secret)
}
