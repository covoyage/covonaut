package agentcore

import (
	"errors"
	"testing"
	"time"
)

func TestIsRetryableErrorNil(t *testing.T) {
	if IsRetryableError(nil) {
		t.Fatal("nil should not be retryable")
	}
}

func TestIsRetryableErrorUnretryable(t *testing.T) {
	if IsRetryableError(errors.New("some random error")) {
		t.Fatal("random error should not be retryable")
	}
}

func TestIsRetryableErrorContextOverflow(t *testing.T) {
	err := errors.New("context length exceeded maximum")
	if IsRetryableError(err) {
		t.Fatal("context overflow errors should not be retryable")
	}
}

func TestIsRetryableHttpCodes(t *testing.T) {
	cases := []string{
		"429 Too Many Requests",
		"got HTTP 429",
		"rate limit exceeded",
		"too many requests, please slow down",
		"500 Internal Server Error",
		"503 Service Unavailable",
		"502 Bad Gateway",
		"504 Gateway Timeout",
	}
	for _, c := range cases {
		if !IsRetryableError(errors.New(c)) {
			t.Errorf("expected retryable: %s", c)
		}
	}
}

func TestIsRetryableConnectionErrors(t *testing.T) {
	cases := []string{
		"connection refused",
		"connection timeout",
		"ECONNRESET",
		"ECONNREFUSED",
		"server error occurred",
		"internal server error",
		"service unavailable",
		"gateway timeout",
		"bad gateway",
		"overloaded: too many requests",
	}
	for _, c := range cases {
		if !IsRetryableError(errors.New(c)) {
			t.Errorf("expected retryable: %s", c)
		}
	}
}

func TestIsContextOverflowError(t *testing.T) {
	cases := []string{
		"context length exceeded",
		"context window limit reached",
		"context limit hit",
		"token limit exceeded",
		"token maximum reached",
		"maximum context size exceeded",
		"too long: content exceeds model limit",
		"content too large for context",
		"max tokens exceeded",
		"exceeds model token limit",
	}
	for _, c := range cases {
		if !IsContextOverflowError(errors.New(c)) {
			t.Errorf("expected context overflow: %s", c)
		}
	}
}

func TestIsContextOverflowNil(t *testing.T) {
	if IsContextOverflowError(nil) {
		t.Fatal("nil should not be context overflow")
	}
}

func TestIsContextOverflowUnrelated(t *testing.T) {
	if IsContextOverflowError(errors.New("some other error")) {
		t.Fatal("unrelated error should not be context overflow")
	}
}

func TestRetryDelayDefaults(t *testing.T) {
	d := retryDelay(1, &RetryConfig{})
	if d != 1000*time.Millisecond {
		t.Fatalf("expected 1s, got %v", d)
	}
}

func TestRetryDelayExponential(t *testing.T) {
	cfg := &RetryConfig{BaseDelayMs: 100, MaxDelayMs: 10000}
	expected := []int64{100, 200, 400, 800, 1600, 3200, 6400, 10000}
	for i, exp := range expected {
		got := retryDelay(int64(i+1), cfg)
		want := time.Duration(exp) * time.Millisecond
		if got != want {
			t.Fatalf("attempt %d: got %v, want %v", i+1, got, want)
		}
	}
}

func TestRetryDelayCapsAtMax(t *testing.T) {
	cfg := &RetryConfig{BaseDelayMs: 1000, MaxDelayMs: 5000}
	d := retryDelay(10, cfg)
	if d > 5000*time.Millisecond {
		t.Fatalf("delay %v exceeds max 5s", d)
	}
}

func TestRetryDelayCustomBaseMax(t *testing.T) {
	cfg := &RetryConfig{BaseDelayMs: 500, MaxDelayMs: 2000}
	d := retryDelay(1, cfg)
	if d != 500*time.Millisecond {
		t.Fatalf("expected 500ms, got %v", d)
	}
	d = retryDelay(3, cfg)
	if d != 2000*time.Millisecond {
		t.Fatalf("expected 2000ms (capped), got %v", d)
	}
}
