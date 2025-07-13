package webserver

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type RateLimiter struct {
	requests map[string][]time.Time
	mu       sync.RWMutex
	rate     int           // requests per window
	window   time.Duration // time window
}

func NewRateLimiter(rate int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{
		requests: make(map[string][]time.Time),
		rate:     rate,
		window:   window,
	}

	// Cleanup old entries periodically
	go func() {
		ticker := time.NewTicker(window)
		defer ticker.Stop()
		for range ticker.C {
			rl.cleanup()
		}
	}()

	return rl
}

func (rl *RateLimiter) cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	for key, times := range rl.requests {
		// Remove old timestamps
		validTimes := []time.Time{}
		for _, t := range times {
			if now.Sub(t) < rl.window {
				validTimes = append(validTimes, t)
			}
		}
		if len(validTimes) == 0 {
			delete(rl.requests, key)
		} else {
			rl.requests[key] = validTimes
		}
	}
}

func RateLimitMiddleware(limiter *RateLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Use user address as key
		key := c.GetString("addr")
		if key == "" {
			key = c.ClientIP()
		}

		limiter.mu.Lock()
		defer limiter.mu.Unlock()

		now := time.Now()
		userRequests := limiter.requests[key]

		// Remove old requests
		validRequests := []time.Time{}
		for _, t := range userRequests {
			if now.Sub(t) < limiter.window {
				validRequests = append(validRequests, t)
			}
		}

		if len(validRequests) >= limiter.rate {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"err": fmt.Sprintf("rate limit exceeded: %d requests per %v", limiter.rate, limiter.window),
			})
			c.Abort()
			return
		}

		validRequests = append(validRequests, now)
		limiter.requests[key] = validRequests

		c.Next()
	}
}
