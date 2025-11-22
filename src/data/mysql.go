package data

import (
	"fmt"
	"os"
	"strings"
)

// GetMySQLDSN returns the MySQL DSN configured via environment.
func GetMySQLDSN() (string, error) {
	dsn := os.Getenv("MYSQL_DSN")
	if strings.TrimSpace(dsn) == "" {
		return "", fmt.Errorf("MYSQL_DSN is not set")
	}
	return dsn, nil
}
