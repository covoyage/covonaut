package a2a

import (
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Token Bucket Rate Limiter
// ---------------------------------------------------------------------------

// RateLimiter implements a per-IP token bucket rate limiter.
type RateLimiter struct {
	mu        sync.RWMutex
	buckets   map[string]*bucket
	rate      float64   // tokens per second
	capacity  int       // bucket capacity
	cleanupInterval time.Duration
	stop      chan struct{}
}

type bucket struct {
	tokens    float64
	lastCheck time.Time
}

// NewRateLimiter creates a token bucket rate limiter.
// rate: tokens added per second.
// capacity: maximum tokens per bucket.
func NewRateLimiter(rate float64, capacity int) *RateLimiter {
	rl := &RateLimiter{
		buckets:         make(map[string]*bucket),
		rate:            rate,
		capacity:        capacity,
		cleanupInterval: 5 * time.Minute,
		stop:            make(chan struct{}),
	}
	go rl.cleanupLoop()
	return rl
}

// Stop stops the rate limiter's background cleanup goroutine.
func (rl *RateLimiter) Stop() {
	close(rl.stop)
}

// cleanupLoop periodically removes stale buckets.
func (rl *RateLimiter) cleanupLoop() {
	ticker := time.NewTicker(rl.cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			rl.purgeStale()
		case <-rl.stop:
			return
		}
	}
}

// purgeStale removes buckets that haven't been used in the last cleanup interval.
func (rl *RateLimiter) purgeStale() {
	cutoff := time.Now().Add(-rl.cleanupInterval)
	rl.mu.Lock()
	defer rl.mu.Unlock()
	for ip, b := range rl.buckets {
		if b.lastCheck.Before(cutoff) {
			delete(rl.buckets, ip)
		}
	}
}

// Allow checks if the given IP has enough tokens for a single request.
func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	b, ok := rl.buckets[ip]
	if !ok {
		b = &bucket{tokens: float64(rl.capacity) - 1, lastCheck: time.Now()}
		rl.buckets[ip] = b
		return true
	}

	now := time.Now()
	elapsed := now.Sub(b.lastCheck).Seconds()
	b.tokens = minFloat(float64(rl.capacity), b.tokens+elapsed*rl.rate)
	b.lastCheck = now

	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// Middleware returns an HTTP middleware that applies rate limiting.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ip = r.RemoteAddr
		}
		if !rl.Allow(ip) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(JSONRPCResponse{
				JSONRPC: "2.0",
				Error:   &JSONRPCError{Code: -32006, Message: "rate limit exceeded"},
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
