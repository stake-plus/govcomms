package data

import (
	"log"
	"strings"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// MustMySQL opens a MySQL connection with sane defaults.
//
//   - Ensures `parseTime=true` so TIMESTAMP / DATETIME columns map to
//     go time.Time without scan errors.
//   - Adds a Unicode‑safe charset if the caller did not specify one
//     (utf8mb4 + utf8mb4_unicode_ci) so emoji / smart‑quotes, etc. store OK.
func MustMySQL(dsn string) *gorm.DB {
	dsn = ensureParam(dsn, "parseTime", "true")

	// default to a UTF‑8‑mb4 connection if the user did not provide charset
	if !strings.Contains(dsn, "charset=") {
		dsn = ensureParam(dsn, "charset", "utf8mb4")
		dsn = ensureParam(dsn, "collation", "utf8mb4_unicode_ci")
	}

	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("mysql: %v", err)
	}
	return db
}

// ensureParam appends ?key=value or &key=value as appropriate when the key
// is not already present in the DSN.
func ensureParam(dsn, key, value string) string {
	if strings.Contains(dsn, key+"=") {
		return dsn
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return dsn + sep + key + "=" + value
}
