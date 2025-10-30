package data

import (
    "os"
)

// GetMySQLDSN returns DSN from env with a fixed default.
func GetMySQLDSN() string {
    dsn := os.Getenv("MYSQL_DSN")
    if dsn == "" {
        dsn = "govcomms:DK3mfv93jf4m@tcp(127.0.0.1:3306)/govcomms"
    }
    return dsn
}


