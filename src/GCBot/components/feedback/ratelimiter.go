package feedback

import (
	"sync"
	"time"
)

type RateLimiter struct {
	users map[string]time.Time
	mu    sync.Mutex
	limit time.Duration
}

func NewRateLimiter(limit time.Duration) *RateLimiter {
	return &RateLimiter{
		users: make(map[string]time.Time),
		limit: limit,
	}
}

func (rl *RateLimiter) CanUse(userID string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	lastUse, exists := rl.users[userID]
	if !exists || time.Since(lastUse) >= rl.limit {
		rl.users[userID] = time.Now()
		return true
	}
	return false
}

func (rl *RateLimiter) TimeUntilNext(userID string) time.Duration {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	lastUse, exists := rl.users[userID]
	if !exists {
		return 0
	}

	elapsed := time.Since(lastUse)
	if elapsed >= rl.limit {
		return 0
	}
	return rl.limit - elapsed
}

func (rl *RateLimiter) Cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	for userID, lastUse := range rl.users {
		if now.Sub(lastUse) > rl.limit*2 {
			delete(rl.users, userID)
		}
	}
}

func (rl *RateLimiter) StartCleanup(interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		for range ticker.C {
			rl.Cleanup()
		}
	}()
}
