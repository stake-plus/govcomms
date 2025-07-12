package data

import (
	"fmt"
	"log"
	"strings"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// MustMySQL opens a connection and panics on error.
// * Forces parseTime=true so TIMESTAMP ↔ time.Time works.
// * Ensures utf8mb4 charset / collation unless user already set one.
func MustMySQL(dsn string) *gorm.DB {
	dsn = ensureParam(dsn, "parseTime", "true")
	if !strings.Contains(dsn, "charset=") {
		dsn = ensureParam(dsn, "charset", "utf8mb4")
		dsn = ensureParam(dsn, "collation", "utf8mb4_unicode_ci")
	}

	// Configure logger to be less verbose
	gormLogger := logger.New(
		log.New(log.Writer(), "\r\n", log.LstdFlags),
		logger.Config{
			SlowThreshold:             time.Second,
			LogLevel:                  logger.Warn,
			IgnoreRecordNotFoundError: true,
			Colorful:                  false,
		},
	)

	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger: gormLogger,
	})
	if err != nil {
		log.Fatalf("mysql: %v", err)
	}
	return db
}

func ensureParam(dsn, key, val string) string {
	if strings.Contains(dsn, key+"=") {
		return dsn
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return dsn + sep + key + "=" + val
}

// Add validation helper
func ValidateIdentifier(name string) error {
	// Only allow alphanumeric and underscore
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_') {
			return fmt.Errorf("invalid identifier: %s", name)
		}
	}
	return nil
}
