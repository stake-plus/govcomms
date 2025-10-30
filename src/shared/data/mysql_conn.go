package data

import (
    "log"
    "strings"
    "time"

    "gorm.io/driver/mysql"
    "gorm.io/gorm"
    "gorm.io/gorm/logger"
)

// ConnectMySQL opens a gorm DB with sane defaults.
func ConnectMySQL(dsn string) (*gorm.DB, error) {
    dsn = ensureParam(dsn, "parseTime", "true")
    if !strings.Contains(dsn, "charset=") {
        dsn = ensureParam(dsn, "charset", "utf8mb4")
        dsn = ensureParam(dsn, "collation", "utf8mb4_unicode_ci")
    }

    gormLogger := logger.New(
        log.New(log.Writer(), "\r\n", log.LstdFlags),
        logger.Config{ SlowThreshold: time.Second, LogLevel: logger.Warn, IgnoreRecordNotFoundError: true, Colorful: false },
    )

    return gorm.Open(mysql.Open(dsn), &gorm.Config{ Logger: gormLogger })
}

func ensureParam(dsn, key, val string) string {
    if strings.Contains(dsn, key+"=") { return dsn }
    sep := "?"
    if strings.Contains(dsn, "?") { sep = "&" }
    return dsn + sep + key + "=" + val
}


