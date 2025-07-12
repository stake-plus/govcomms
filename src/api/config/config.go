// File: src/api/config/config.go

package config

import (
	"log"
	"os"
	"strconv"

	"github.com/stake-plus/polkadot-gov-comms/src/api/data"
	"gorm.io/gorm"
)

type Config struct {
	MySQLDSN     string
	RedisURL     string
	JWTSecret    string
	Port         string
	PollInterval int
	SSLCert      string
	SSLKey       string
	EnableSSL    bool
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

// Update to load JWT secret from database
func Load(db *gorm.DB) Config {
	// Load settings first
	if err := data.LoadSettings(db); err != nil {
		log.Printf("Failed to load settings: %v", err)
	}

	pi, _ := strconv.Atoi(getenv("POLL_INTERVAL", "60"))

	// Try to get JWT secret from database first
	jwtSecret := data.GetSetting("jwt_secret")
	if jwtSecret == "" {
		jwtSecret = getenv("JWT_SECRET", "9eafd87a084c0cf4ededa3b0ad774b77be9bb1b1a5696b9e5b11d59b71fa57ce")
	}

	// Check if SSL is enabled - only if BOTH cert and key are provided
	sslCert := os.Getenv("SSL_CERT")
	sslKey := os.Getenv("SSL_KEY")
	enableSSL := sslCert != "" && sslKey != ""

	return Config{
		MySQLDSN:     getenv("MYSQL_DSN", "govcomms:DK3mfv93jf4m@tcp(172.16.254.7:3306)/govcomms"),
		RedisURL:     getenv("REDIS_URL", "redis://172.16.254.7:6379/0"),
		JWTSecret:    jwtSecret,
		Port:         getenv("PORT", "8080"),
		PollInterval: pi,
		SSLCert:      sslCert,
		SSLKey:       sslKey,
		EnableSSL:    enableSSL,
	}
}
