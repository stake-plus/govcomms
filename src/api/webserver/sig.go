package webserver

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	schnorrkel "github.com/ChainSafe/go-schnorrkel"
	"github.com/golang-jwt/jwt/v5"
	"github.com/itering/scale.go/ss58"
)

func verifySignature(addr, sigHex, nonce string) error {
	pub, err := ss58.DecodeToPub(addr)
	if err != nil {
		return err
	}
	sigBytes, err := hex.DecodeString(strings.TrimPrefix(sigHex, "0x"))
	if err != nil {
		return err
	}

	var pk schnorrkel.PublicKey
	if err = pk.Decode(pub); err != nil {
		return err
	}
	var sig schnorrkel.Signature
	if err = sig.Decode(sigBytes); err != nil {
		return err
	}
	ok, _ := pk.Verify(sig, []byte(nonce))
	if !ok {
		return fmt.Errorf("signature verify failed")
	}
	return nil
}

func issueJWT(addr string, secret []byte) (string, error) {
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"addr": addr,
		"exp":  time.Now().Add(1 * time.Hour).Unix(),
	})
	return t.SignedString(secret)
}
