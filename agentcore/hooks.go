package agentcore

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// HookContext carries contextual information passed to before/after hooks.
type HookContext struct {
	ToolName  string
	Arguments json.RawMessage
	State     *AgentState
}

// BeforeHook runs before tool execution. Return a non-nil error to reject the call.
type BeforeHook func(ctx context.Context, hc *HookContext) error

// AfterHook runs after tool execution with the result string and any error.
type AfterHook func(ctx context.Context, hc *HookContext, result string, err error)

// ExecuteFunc is the signature for a single tool execution step in the middleware chain.
type ExecuteFunc func(ctx context.Context, tc ToolCall) (string, error)

// Middleware wraps tool execution with cross-cutting logic.
// Call next(ctx, tc) to proceed to the next middleware or the core executor.
type Middleware func(next ExecuteFunc) ExecuteFunc

// --- built-in before hooks ---

// LoggingBeforeHook logs tool call start via slog.
func LoggingBeforeHook(logger *slog.Logger) BeforeHook {
	return func(ctx context.Context, hc *HookContext) error {
		logger.InfoContext(ctx, "tool call starting",
			"tool", hc.ToolName,
			"args_size", len(hc.Arguments),
		)
		return nil
	}
}

// RateLimitBeforeHook rejects tool calls that exceed maxCalls within the given interval.
func RateLimitBeforeHook(maxCalls int64, interval time.Duration) BeforeHook {
	tb := &tokenBucket{
		tokens:   maxCalls,
		max:      maxCalls,
		interval: interval,
		lastFill: time.Now(),
	}
	return func(_ context.Context, hc *HookContext) error {
		if !tb.take() {
			return fmt.Errorf("rate limit exceeded for tool %s: max %d calls per %s",
				hc.ToolName, tb.max, tb.interval)
		}
		return nil
	}
}

type tokenBucket struct {
	mu       sync.Mutex
	tokens   int64
	max      int64
	interval time.Duration
	lastFill time.Time
}

func (tb *tokenBucket) take() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	now := time.Now()
	if now.Sub(tb.lastFill) >= tb.interval {
		tb.tokens = tb.max
		tb.lastFill = now
	}
	if tb.tokens <= 0 {
		return false
	}
	tb.tokens--
	return true
}

// --- built-in after hooks ---

// LoggingAfterHook logs tool call completion via slog.
func LoggingAfterHook(logger *slog.Logger) AfterHook {
	return func(ctx context.Context, hc *HookContext, result string, err error) {
		if err != nil {
			logger.ErrorContext(ctx, "tool call failed",
				"tool", hc.ToolName,
				"error", err,
			)
		} else {
			logger.InfoContext(ctx, "tool call succeeded",
				"tool", hc.ToolName,
				"result_size", len(result),
			)
		}
	}
}

// --- built-in middleware ---

// TimeoutMiddleware wraps each tool call with a context deadline.
func TimeoutMiddleware(timeout time.Duration) Middleware {
	return func(next ExecuteFunc) ExecuteFunc {
		return func(ctx context.Context, tc ToolCall) (string, error) {
			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			return next(ctx, tc)
		}
	}
}

// RetryMiddleware retries failed tool calls up to maxRetries times with a fixed delay.
func RetryMiddleware(maxRetries int64, delay time.Duration) Middleware {
	return func(next ExecuteFunc) ExecuteFunc {
		return func(ctx context.Context, tc ToolCall) (string, error) {
			var lastErr error
			for i := int64(0); i <= maxRetries; i++ {
				result, err := next(ctx, tc)
				if err == nil {
					return result, nil
				}
				lastErr = err
				if i < maxRetries {
					select {
					case <-time.After(delay):
					case <-ctx.Done():
						return "", ctx.Err()
					}
				}
			}
			return "", fmt.Errorf("after %d retries: %w", maxRetries, lastErr)
		}
	}
}
