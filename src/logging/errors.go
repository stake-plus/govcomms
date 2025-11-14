package logging

import "strings"

func IsRateLimit(err error) bool {
    if err == nil { return false }
    msg := err.Error()
    return strings.Contains(msg, "rate_limit") || strings.Contains(msg, "429")
}


