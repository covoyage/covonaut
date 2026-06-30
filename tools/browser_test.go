package tools

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNavigationTimeout(t *testing.T) {
	if got := navigationTimeout(10 * time.Second); got != 60*time.Second {
		t.Fatalf("short command timeout should use 60s navigation timeout, got %s", got)
	}
	if got := navigationTimeout(90 * time.Second); got != 90*time.Second {
		t.Fatalf("long command timeout should be preserved, got %s", got)
	}
}

func TestNavigationTimeoutError(t *testing.T) {
	err := navigationTimeoutError("https://example.com", 60*time.Second)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	for _, want := range []string{"navigation timed out", "https://example.com", "try again", "browser_snapshot"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("expected %q to contain %q", msg, want)
		}
	}
}

func TestIsDeadlineError(t *testing.T) {
	if !isDeadlineError(context.DeadlineExceeded) {
		t.Fatal("expected direct deadline exceeded to match")
	}
	if !isDeadlineError(errors.Join(errors.New("wrapped"), context.DeadlineExceeded)) {
		t.Fatal("expected joined deadline exceeded to match")
	}
	if isDeadlineError(errors.New("other")) {
		t.Fatal("non-deadline error should not match")
	}
}

func TestBrowserManagerActiveSessionFallback(t *testing.T) {
	manager := &BrowserManager{
		sessions: map[string]*BrowserSession{
			"default": {
				sessionID:    "default",
				backendType:  BackendLocal,
				lastActivity: time.Now(),
			},
		},
	}

	session, ok := manager.GetActiveSession("default")
	if !ok || session.sessionID != "default" {
		t.Fatalf("expected fallback default session, got %#v ok=%v", session, ok)
	}
}

func TestBrowserManagerActiveSessionTracksHybridLocal(t *testing.T) {
	manager := &BrowserManager{
		sessions: map[string]*BrowserSession{
			"default": {
				sessionID:    "default",
				backendType:  BackendBrowserbase,
				lastActivity: time.Now(),
			},
			"default::local": {
				sessionID:    "default::local",
				backendType:  BackendLocal,
				lastActivity: time.Now(),
			},
		},
		activeSession: "default::local",
	}

	session, ok := manager.GetActiveSession("default")
	if !ok || session.sessionID != "default::local" {
		t.Fatalf("expected active hybrid local session, got %#v ok=%v", session, ok)
	}
}
