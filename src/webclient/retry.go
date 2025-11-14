package webclient

import (
    "context"
    "time"
)

type AttemptFunc func() (status int, body []byte, err error)

// DoWithRetry retries the attempt function on transient errors (429/5xx) or non-nil errors.
func DoWithRetry(ctx context.Context, attempts int, initialDelay time.Duration, fn AttemptFunc) (int, []byte, error) {
    if attempts <= 0 { attempts = 1 }
    if initialDelay <= 0 { initialDelay = 2 * time.Second }
    delay := initialDelay
    for i := 0; i < attempts; i++ {
        status, body, err := fn()
        if err == nil && status != 429 && status < 500 {
            return status, body, nil
        }
        if i == attempts-1 {
            return status, body, err
        }
        t := time.NewTimer(delay)
        select {
        case <-ctx.Done():
            t.Stop()
            return status, body, ctx.Err()
        case <-t.C:
        }
        if delay < 30*time.Second { delay *= 2 }
    }
    return 0, nil, context.DeadlineExceeded
}


