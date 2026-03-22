package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type rateLimitEntry struct {
	Count    int
	WindowAt time.Time
}

// RateLimiter tracks request counts per IP within a sliding window.
type RateLimiter struct {
	mu      sync.Mutex
	entries map[string]*rateLimitEntry
	limit   int
	window  time.Duration
}

// NewRateLimiter creates a rate limiter that allows limit requests per window per IP.
// It starts a background goroutine to clean up expired entries every 5 minutes.
func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{
		entries: make(map[string]*rateLimitEntry),
		limit:   limit,
		window:  window,
	}
	go rl.cleanup()
	return rl
}

func (rl *RateLimiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		rl.mu.Lock()
		for ip, e := range rl.entries {
			if now.Sub(e.WindowAt) > rl.window {
				delete(rl.entries, ip)
			}
		}
		rl.mu.Unlock()
	}
}

// Middleware returns a Gin handler that enforces the rate limit.
func (rl *RateLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		now := time.Now()

		rl.mu.Lock()
		e, ok := rl.entries[ip]
		if !ok || now.Sub(e.WindowAt) > rl.window {
			// New window.
			rl.entries[ip] = &rateLimitEntry{Count: 1, WindowAt: now}
			rl.mu.Unlock()
			c.Next()
			return
		}

		e.Count++
		if e.Count > rl.limit {
			rl.mu.Unlock()
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "too many requests, try again later",
			})
			return
		}
		rl.mu.Unlock()
		c.Next()
	}
}
