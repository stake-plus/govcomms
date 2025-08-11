package data

import (
	"context"
	"log"

	"github.com/redis/go-redis/v9"
)

func MustRedis(url string) *redis.Client {
	opt, err := redis.ParseURL(url)
	if err != nil {
		log.Fatalf("redis: %v", err)
	}
	return redis.NewClient(opt)
}

func PublishMessage(ctx context.Context, rdb *redis.Client, payload map[string]interface{}) error {
	_, err := rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: "govcomms.messages",
		Values: payload,
	}).Result()
	return err
}
