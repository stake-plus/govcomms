package config

import (
	"log"
	"os"
	"strconv"
)

type Config struct {
	MySQLDSN     string
	RedisURL     string
	JWTSecret    string
	RPCURL       string
	Port         string
	PollInterval int
}

func getenv(key, def string) string {
	v := os.Getenv(key)
	if v == "" {
		if def == "" {
			log.Fatalf("missing env %s", key)
		}
		return def
	}
	return v
}

func Load() Config {
	pi, _ := strconv.Atoi(getenv("POLL_INTERVAL", "60"))
	return Config{
		MySQLDSN:     getenv("MYSQL_DSN", ""),
		RedisURL:     getenv("REDIS_URL", "redis://127.0.0.1:6379/0"),
		JWTSecret:    getenv("JWT_SECRET", ""),
		RPCURL:       getenv("RPC_URL", "wss://rpc.polkadot.io"),
		Port:         getenv("PORT", "8080"),
		PollInterval: pi,
	}
}
