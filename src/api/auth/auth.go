// internal/auth/auth.go
package auth

import (
	"context"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// generateNonce stores a 5‑minute nonce keyed by address.
func GenerateNonce(ctx context.Context, rdb *redis.Client, addr string) (string, error) {
	nonce := uuid.NewString()
	return nonce, rdb.Set(ctx, "nonce:"+addr, nonce, 5*time.Minute).Err()
}

// checkAndDeleteNonce verifies nonce + sig. Signature validation is TODO.
func CheckAndDeleteNonce(ctx context.Context, rdb *redis.Client, addr, sig, jwtSecret string) (string, error) {
	nonce, err := rdb.GetDel(ctx, "nonce:"+addr).Result()
	if err != nil {
		return "", err
	}

	// TODO: verify sr25519 signature (sig) over nonce with addr.
	_ = sig
	_ = nonce

	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"addr": addr,
		"exp":  time.Now().Add(1 * time.Hour).Unix(),
	})
	return tok.SignedString([]byte(jwtSecret))
}
