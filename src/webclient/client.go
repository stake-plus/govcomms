package webclient

import (
    "net/http"
    "time"
)

// NewDefault returns an HTTP client with sane timeouts.
func NewDefault(timeout time.Duration) *http.Client {
    if timeout == 0 { timeout = 60 * time.Second }
    return &http.Client{ Timeout: timeout }
}


