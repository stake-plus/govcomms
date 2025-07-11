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
		MySQLDSN:     getenv("MYSQL_DSN", "govcomms:DK3mfv93jf4m@tcp(172.16.254.7:3306)/govcomms"),
		RedisURL:     getenv("REDIS_URL", "redis://172.16.254.7:6379/0"),
		JWTSecret:    getenv("JWT_SECRET", "9eafd87a084c0cf4ededa3b0ad774b77be9bb1b1a5696b9e5b11d59b71fa57ce"),
		RPCURL:       getenv("RPC_URL", "wss://rpc.polkadot.io"),
		Port:         getenv("PORT", "443"),
		PollInterval: pi,
	}
}
