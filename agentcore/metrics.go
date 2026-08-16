package agentcore

import (
	"context"
	"time"
)

// Metrics receives agent runtime metrics. It is the semantic contract for
// agent observability; concrete adapters map it to a backend (see
// NewOtelMetrics). A nil Metrics means metrics are disabled (noop).
//
// The interface is deliberately small and aligned with the GenAI semantic
// conventions so that adapters can emit standard metrics without loss. It is
// complementary to the Tracer interface: tracing records what happened, metrics
// count and measure it.
type Metrics interface {
	// RecordModelCall records token usage and wall-clock duration of a
	// completed LLM call. usage may be nil for calls that did not report
	// usage; start must be the time the call began.
	RecordModelCall(ctx context.Context, req *ProviderRequest, usage *TokenUsage, start time.Time)

	// RecordError counts a failed component run. component is one of "model",
	// "tool", or "agent"; err is the failure that triggered the recording and
	// may be nil.
	RecordError(ctx context.Context, component string, err error)
}

type noopMetrics struct{}

func (noopMetrics) RecordModelCall(context.Context, *ProviderRequest, *TokenUsage, time.Time) {}
func (noopMetrics) RecordError(context.Context, string, error)                                {}

// ModelMetricsMiddleware wraps every provider call to record token usage,
// duration, and model errors. Unlike NewModelSpanMiddleware it does not skip
// when a model span already exists — every invocation counts, including the
// agent's own turns. Returns a pass-through when metrics are nil.
func NewModelMetricsMiddleware(metrics Metrics) func(Provider) Provider {
	if metrics == nil {
		return func(p Provider) Provider { return p }
	}
	return func(inner Provider) Provider {
		return &modelMetricsMiddleware{metrics: metrics, inner: inner}
	}
}

type modelMetricsMiddleware struct {
	metrics Metrics
	inner   Provider
}

func (m *modelMetricsMiddleware) Complete(ctx context.Context, req *ProviderRequest) (*ProviderResponse, error) {
	start := time.Now()
	resp, err := m.inner.Complete(ctx, req)
	if err != nil {
		m.metrics.RecordError(ctx, "model", err)
		return resp, err
	}
	if resp != nil {
		m.metrics.RecordModelCall(ctx, req, &resp.Usage, start)
	}
	return resp, nil
}

func (m *modelMetricsMiddleware) Stream(ctx context.Context, req *ProviderRequest) (<-chan StreamDelta, error) {
	start := time.Now()
	stream, err := m.inner.Stream(ctx, req)
	if err != nil {
		m.metrics.RecordError(ctx, "model", err)
		return stream, err
	}

	out := make(chan StreamDelta)
	go func() {
		defer func() {
			// Never let observability panic kill the host process.
			if r := recover(); r != nil {
				close(out)
				return
			}
			close(out)
		}()
		var usage *TokenUsage
		var terminalErr error
		for delta := range stream {
			if delta.Usage != nil {
				usage = delta.Usage
			}
			if delta.Err != nil {
				terminalErr = delta.Err
			}
			out <- delta
		}
		if terminalErr != nil {
			m.metrics.RecordError(ctx, "model", terminalErr)
		}
		m.metrics.RecordModelCall(ctx, req, usage, start)
	}()
	return out, nil
}

// MetricsMiddleware wraps each tool execution to record tool errors. It is
// injected into the executor chain automatically when Config.Metrics is set;
// it is also exported for hosts composing their own middleware chains.
func MetricsMiddleware(metrics Metrics) Middleware {
	if metrics == nil {
		return func(next ExecuteFunc) ExecuteFunc { return next }
	}
	return func(next ExecuteFunc) ExecuteFunc {
		return func(ctx context.Context, tc ToolCall) (string, error) {
			result, err := next(ctx, tc)
			if err != nil {
				metrics.RecordError(ctx, "tool", err)
			}
			return result, err
		}
	}
}
