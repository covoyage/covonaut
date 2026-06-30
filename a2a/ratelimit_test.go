package a2a

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiter_Allow(t *testing.T) {
	rl := NewRateLimiter(10, 5)
	defer rl.Stop()

	ip := "127.0.0.1"

	// Should allow initial burst
	for i := 0; i < 5; i++ {
		if !rl.Allow(ip) {
			t.Fatalf("expected allow on attempt %d", i+1)
		}
	}

	// Should deny after burst
	if rl.Allow(ip) {
		t.Fatal("expected deny after burst")
	}

	// Wait for token refill
	time.Sleep(200 * time.Millisecond)
	if !rl.Allow(ip) {
		t.Fatal("expected allow after refill")
	}
}

func TestRateLimiter_Middleware(t *testing.T) {
	rl := NewRateLimiter(100, 2)
	defer rl.Stop()

	handler := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First two requests should succeed
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "127.0.0.1:1234"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
	}

	// Third request should be rate limited
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rr.Code)
	}
}

func TestRateLimiter_DifferentIPs(t *testing.T) {
	rl := NewRateLimiter(100, 2)
	defer rl.Stop()

	ip1 := "192.168.1.1"
	ip2 := "192.168.1.2"

	// Exhaust ip1
	for i := 0; i < 2; i++ {
		rl.Allow(ip1)
	}
	if rl.Allow(ip1) {
		t.Fatal("expected ip1 to be rate limited")
	}

	// ip2 should still be allowed
	if !rl.Allow(ip2) {
		t.Fatal("expected ip2 to be allowed")
	}
}
