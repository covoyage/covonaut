package agentcore

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"
)

func TestLoggingBeforeHook(t *testing.T) {
	hook := LoggingBeforeHook(slog.Default())
	hc := &HookContext{
		ToolName:  "test_tool",
		Arguments: json.RawMessage(`{"key":"value"}`),
		State:     &AgentState{},
	}
	err := hook(context.Background(), hc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRateLimitBeforeHook(t *testing.T) {
	hook := RateLimitBeforeHook(2, time.Minute)

	// First two should succeed
	if err := hook(nil, &HookContext{ToolName: "t1", State: &AgentState{}}); err != nil {
		t.Fatal("expected first call to succeed")
	}
	if err := hook(nil, &HookContext{ToolName: "t2", State: &AgentState{}}); err != nil {
		t.Fatal("expected second call to succeed")
	}

	// Third should fail
	if err := hook(nil, &HookContext{ToolName: "t3", State: &AgentState{}}); err == nil {
		t.Fatal("expected third call to be rate limited")
	}
}

func TestRateLimitBeforeHookRefill(t *testing.T) {
	hook := RateLimitBeforeHook(1, 50*time.Millisecond)

	if err := hook(nil, &HookContext{State: &AgentState{}}); err != nil {
		t.Fatal("expected first call to succeed")
	}
	// Should fail immediately
	if err := hook(nil, &HookContext{State: &AgentState{}}); err == nil {
		t.Fatal("expected second call to fail")
	}

	// Wait for refill
	time.Sleep(60 * time.Millisecond)
	if err := hook(nil, &HookContext{State: &AgentState{}}); err != nil {
		t.Fatal("expected call after refill to succeed")
	}
}

func TestLoggingAfterHook(t *testing.T) {
	hook := LoggingAfterHook(slog.Default())
	hc := &HookContext{
		ToolName: "test_tool",
		State:    &AgentState{},
	}
	// Should not panic
	hook(context.Background(), hc, "result", nil)
	hook(context.Background(), hc, "", errors.New("boom"))
}

func TestTimeoutMiddleware(t *testing.T) {
	mw := TimeoutMiddleware(100 * time.Millisecond)
	executed := false
	fn := mw(func(ctx context.Context, tc ToolCall) (string, error) {
		executed = true
		return "ok", nil
	})
	result, err := fn(context.Background(), ToolCall{Name: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "ok" {
		t.Fatalf("result = %q", result)
	}
	if !executed {
		t.Fatal("inner function should have been called")
	}
}

func TestTimeoutMiddlewareCancels(t *testing.T) {
	mw := TimeoutMiddleware(10 * time.Millisecond)
	fn := mw(func(ctx context.Context, tc ToolCall) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
	_, err := fn(context.Background(), ToolCall{Name: "test"})
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestRetryMiddlewareSuccess(t *testing.T) {
	mw := RetryMiddleware(3, time.Millisecond)
	callCount := 0
	fn := mw(func(ctx context.Context, tc ToolCall) (string, error) {
		callCount++
		return "ok", nil
	})
	result, err := fn(context.Background(), ToolCall{Name: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "ok" {
		t.Fatalf("result = %q", result)
	}
	if callCount != 1 {
		t.Fatalf("expected 1 call, got %d", callCount)
	}
}

func TestRetryMiddlewareRetries(t *testing.T) {
	mw := RetryMiddleware(3, time.Millisecond)
	callCount := 0
	fn := mw(func(ctx context.Context, tc ToolCall) (string, error) {
		callCount++
		if callCount < 3 {
			return "", errors.New("transient error")
		}
		return "ok", nil
	})
	result, err := fn(context.Background(), ToolCall{Name: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "ok" {
		t.Fatalf("result = %q", result)
	}
	if callCount != 3 {
		t.Fatalf("expected 3 calls, got %d", callCount)
	}
}

func TestRetryMiddlewareExhausted(t *testing.T) {
	mw := RetryMiddleware(2, time.Millisecond)
	fn := mw(func(ctx context.Context, tc ToolCall) (string, error) {
		return "", errors.New("persistent error")
	})
	_, err := fn(context.Background(), ToolCall{Name: "test"})
	if err == nil {
		t.Fatal("expected error after retries exhausted")
	}
}

func TestRetryMiddlewareContextCancelled(t *testing.T) {
	mw := RetryMiddleware(5, 100*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	fn := mw(func(ctx context.Context, tc ToolCall) (string, error) {
		return "", errors.New("fail")
	})
	_, err := fn(ctx, ToolCall{Name: "test"})
	if err == nil {
		t.Fatal("expected error")
	}
}
