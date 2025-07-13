package data

import (
	"context"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	noncePrefix  = "nonce:"
	streamEvents = "govcomms.messages"
)

func MustRedis(url string) *redis.Client {
	opt, err := redis.ParseURL(url)
	if err != nil {
		log.Fatalf("redis: %v", err)
	}
	return redis.NewClient(opt)
}

func SetNonce(ctx context.Context, rdb *redis.Client, addr, nonce string) error {
	return rdb.Set(ctx, noncePrefix+addr, nonce, 30*time.Minute).Err()
}

func ConfirmNonce(ctx context.Context, rdb *redis.Client, addr string) error {
	return rdb.Set(ctx, noncePrefix+addr, "CONFIRMED", 30*time.Minute).Err()
}

// GetNonce retrieves nonce without deleting
func GetNonce(ctx context.Context, rdb *redis.Client, addr string) (string, error) {
	return rdb.Get(ctx, noncePrefix+addr).Result()
}

// DelNonce deletes the nonce
func DelNonce(ctx context.Context, rdb *redis.Client, addr string) error {
	return rdb.Del(ctx, noncePrefix+addr).Err()
}

// GetAndDelNonce atomically gets and deletes (deprecated - use GetNonce + DelNonce)
func GetAndDelNonce(ctx context.Context, rdb *redis.Client, addr string) (string, error) {
	return rdb.GetDel(ctx, noncePrefix+addr).Result()
}

func PublishMessage(ctx context.Context, rdb *redis.Client, payload map[string]interface{}) error {
	_, err := rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: streamEvents,
		Values: payload,
	}).Result()
	return err
}
