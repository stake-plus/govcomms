package core

import (
	"strconv"
	"strings"
)

// ExtraString returns a trimmed value from the Extra map or the fallback when unset.
func ExtraString(extra map[string]string, key, fallback string) string {
	if extra == nil {
		return fallback
	}
	raw, ok := extra[key]
	if !ok {
		return fallback
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return fallback
	}
	return trimmed
}

// ExtraFloat parses a float value from the Extra map or returns the fallback.
func ExtraFloat(extra map[string]string, key string, fallback float64) float64 {
	raw := ExtraString(extra, key, "")
	if raw == "" {
		return fallback
	}
	if val, err := strconv.ParseFloat(raw, 64); err == nil {
		return val
	}
	return fallback
}

// ExtraInt parses an int value from the Extra map or returns the fallback.
func ExtraInt(extra map[string]string, key string, fallback int) int {
	raw := ExtraString(extra, key, "")
	if raw == "" {
		return fallback
	}
	if val, err := strconv.Atoi(raw); err == nil {
		return val
	}
	return fallback
}

// ExtraBool parses a boolean toggle from the Extra map or returns the fallback.
func ExtraBool(extra map[string]string, key string, fallback bool) bool {
	raw, ok := extra[key]
	if !ok {
		return fallback
	}
	trimmed := strings.TrimSpace(strings.ToLower(raw))
	if trimmed == "" {
		return fallback
	}
	switch trimmed {
	case "1", "true", "t", "yes", "y", "on":
		return true
	case "0", "false", "f", "no", "n", "off":
		return false
	default:
		if val, err := strconv.ParseBool(trimmed); err == nil {
			return val
		}
		return fallback
	}
}

// ClampFloat confines val to the provided [min, max] range.
func ClampFloat(val, min, max float64) float64 {
	if val < min {
		return min
	}
	if val > max {
		return max
	}
	return val
}
