package data

import (
	"log"
	"os"
)

// GetMySQLDSN returns the MySQL DSN configured via environment.
func GetMySQLDSN() string {
	dsn := os.Getenv("MYSQL_DSN")
	if dsn == "" {
		log.Fatalf("MYSQL_DSN is not set")
	}
	return dsn
}
