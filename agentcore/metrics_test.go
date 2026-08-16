package agentcore

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// recordingMetrics is a test sink that records everything the Metrics contract
// reports.
type recordingMetrics struct {
	mu         sync.Mutex
	modelCalls []modelCall
	errs       []errRec
}

type modelCall struct {
	model            string
	promptTokens     int64
	completionTokens int64
	durationRecorded bool
}

type errRec struct {
	component string
	err       error
}

func (r *recordingMetrics) RecordModelCall(_ context.Context, req *ProviderRequest, usage *TokenUsage, start time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	mc := modelCall{model: req.Model, durationRecorded: !start.IsZero()}
	if usage != nil {
		mc.promptTokens = usage.PromptTokens
		mc.completionTokens = usage.CompletionTokens
	}
	r.modelCalls = append(r.modelCalls, mc)
}

func (r *recordingMetrics) RecordError(_ context.Context, component string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errs = append(r.errs, errRec{component: component, err: err})
}

func (r *recordingMetrics) counts() (modelCalls int, errs []errRec) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.modelCalls), append([]errRec(nil), r.errs...)
}

// usageProvider reports token usage on Complete.
type usageProvider struct{}

func (u *usageProvider) Complete(_ context.Context, _ *ProviderRequest) (*ProviderResponse, error) {
	return &ProviderResponse{Content: "ok", Usage: TokenUsage{PromptTokens: 100, CompletionTokens: 25}}, nil
}

func (u *usageProvider) Stream(_ context.Context, _ *ProviderRequest) (<-chan StreamDelta, error) {
	ch := make(chan StreamDelta, 2)
	ch <- StreamDelta{Content: "ok", Usage: &TokenUsage{PromptTokens: 50, CompletionTokens: 10}}
	ch <- StreamDelta{Done: true}
	close(ch)
	return ch, nil
}

func TestNewModelMetricsMiddlewareRecordsComplete(t *testing.T) {
	rec := &recordingMetrics{}
	provider := NewModelMetricsMiddleware(rec)(&usageProvider{})

	resp, err := provider.Complete(context.Background(), &ProviderRequest{Model: "gpt-4o"})
	if err != nil || resp == nil {
		t.Fatalf("complete: %v", err)
	}

	calls, errs := rec.counts()
	if calls != 1 {
		t.Fatalf("model calls = %d, want 1", calls)
	}
	rec.mu.Lock()
	mc := rec.modelCalls[0]
	rec.mu.Unlock()
	if mc.model != "gpt-4o" {
		t.Errorf("model = %q", mc.model)
	}
	if mc.promptTokens != 100 || mc.completionTokens != 25 {
		t.Errorf("tokens = %d/%d, want 100/25", mc.promptTokens, mc.completionTokens)
	}
	if !mc.durationRecorded {
		t.Error("duration not recorded")
	}
	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
}

func TestNewModelMetricsMiddlewareRecordsStream(t *testing.T) {
	rec := &recordingMetrics{}
	provider := NewModelMetricsMiddleware(rec)(&usageProvider{})

	stream, err := provider.Stream(context.Background(), &ProviderRequest{Model: "gpt-4o"})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	for delta := range stream {
		_ = delta
	}

	calls, errs := rec.counts()
	if calls != 1 {
		t.Fatalf("model calls = %d, want 1", calls)
	}
	rec.mu.Lock()
	mc := rec.modelCalls[0]
	rec.mu.Unlock()
	if mc.promptTokens != 50 || mc.completionTokens != 10 {
		t.Errorf("tokens = %d/%d, want 50/10", mc.promptTokens, mc.completionTokens)
	}
	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
}

func TestNewModelMetricsMiddlewareRecordsErrors(t *testing.T) {
	rec := &recordingMetrics{}
	boom := errors.New("boom")
	failProvider := &failProvider{err: boom}
	provider := NewModelMetricsMiddleware(rec)(failProvider)

	if _, err := provider.Complete(context.Background(), &ProviderRequest{Model: "x"}); err == nil {
		t.Fatal("expected error")
	}
	if _, err := provider.Stream(context.Background(), &ProviderRequest{Model: "x"}); err == nil {
		t.Fatal("expected error")
	}

	_, errs := rec.counts()
	if len(errs) != 2 {
		t.Fatalf("errors = %d, want 2", len(errs))
	}
	for _, e := range errs {
		if e.component != "model" {
			t.Errorf("component = %q, want model", e.component)
		}
		if e.err != boom {
			t.Errorf("err = %v, want boom", e.err)
		}
	}
}

func TestNewModelMetricsMiddlewareStreamTerminalError(t *testing.T) {
	rec := &recordingMetrics{}
	boom := errors.New("mid-stream failure")
	provider := NewModelMetricsMiddleware(rec)(&streamTerminalProvider{boom: boom})

	stream, err := provider.Stream(context.Background(), &ProviderRequest{Model: "x"})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	for delta := range stream {
		_ = delta
	}

	_, errs := rec.counts()
	if len(errs) != 1 {
		t.Fatalf("errors = %d, want 1 (mid-stream)", len(errs))
	}
	if errs[0].component != "model" {
		t.Errorf("component = %q, want model", errs[0].component)
	}
}

// failProvider fails every call.
type failProvider struct{ err error }

func (f *failProvider) Complete(_ context.Context, _ *ProviderRequest) (*ProviderResponse, error) {
	return nil, f.err
}

func (f *failProvider) Stream(_ context.Context, _ *ProviderRequest) (<-chan StreamDelta, error) {
	return nil, f.err
}

// streamTerminalProvider sends a terminal delta.Err, mimicking a degenerate
// repetition-loop signal from a provider middleware.
type streamTerminalProvider struct {
	boom error
}

func (s *streamTerminalProvider) Complete(_ context.Context, _ *ProviderRequest) (*ProviderResponse, error) {
	return nil, nil
}

func (s *streamTerminalProvider) Stream(_ context.Context, _ *ProviderRequest) (<-chan StreamDelta, error) {
	ch := make(chan StreamDelta, 2)
	ch <- StreamDelta{Content: "partial"}
	ch <- StreamDelta{Done: true, Err: s.boom}
	close(ch)
	return ch, nil
}

func TestMetricsMiddlewareRecordsToolErrors(t *testing.T) {
	rec := &recordingMetrics{}
	boom := errors.New("tool failed")
	mw := MetricsMiddleware(rec)
	exec := mw(func(_ context.Context, _ ToolCall) (string, error) {
		return "", boom
	})

	if _, err := exec(context.Background(), ToolCall{Name: "bad"}); err == nil {
		t.Fatal("expected error")
	}

	_, errs := rec.counts()
	if len(errs) != 1 || errs[0].component != "tool" {
		t.Fatalf("errors = %v, want one tool error", errs)
	}
	if errs[0].err != boom {
		t.Errorf("err = %v, want boom", errs[0].err)
	}
}

func TestNewOtelMetricsSemantics(t *testing.T) {
	manual := metric.NewManualReader()
	mp := metric.NewMeterProvider(metric.WithReader(manual))
	defer mp.Shutdown(context.Background())

	metrics := NewOtelMetrics(mp.Meter("test"))
	req := &ProviderRequest{Model: "gpt-4o"}
	metrics.RecordModelCall(context.Background(), req,
		&TokenUsage{PromptTokens: 100, CompletionTokens: 25}, time.Now().Add(-2*time.Second))
	metrics.RecordError(context.Background(), "tool", errors.New("boom"))

	var rm metricdata.ResourceMetrics
	if err := manual.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}

	got := map[string]bool{}
	var tokenPoints int
	var errComponent string
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			got[m.Name] = true
			switch m.Name {
			case "gen_ai.client.token.usage":
				sum := m.Data.(metricdata.Sum[int64])
				for _, dp := range sum.DataPoints {
					tokenPoints++
					attrs := dp.Attributes.ToSlice()
					if !hasAttr(attrs, "gen_ai.request.model", "gpt-4o") {
						t.Errorf("token point missing model attribute")
					}
					if !hasAttr(attrs, "gen_ai.operation.name", "chat") {
						t.Errorf("token point missing operation name")
					}
				}
			case "agentcore.errors":
				sum := m.Data.(metricdata.Sum[int64])
				for _, dp := range sum.DataPoints {
					for _, kv := range dp.Attributes.ToSlice() {
						if kv.Key == "component" {
							errComponent = kv.Value.AsString()
						}
					}
				}
			}
		}
	}

	for _, name := range []string{"gen_ai.client.token.usage", "gen_ai.client.operation.duration", "agentcore.errors"} {
		if !got[name] {
			t.Errorf("metric %q not emitted", name)
		}
	}
	if tokenPoints != 2 {
		t.Errorf("token data points = %d, want 2 (input+output)", tokenPoints)
	}
	if errComponent != "tool" {
		t.Errorf("error component = %q, want tool", errComponent)
	}
}

func TestAgentMetricsIntegrated(t *testing.T) {
	rec := &recordingMetrics{}
	provider := NewModelMetricsMiddleware(rec)(&echoProvider{})
	cfg := stubAgentConfig("mtest", nil)
	cfg.Provider = provider
	cfg.Metrics = rec

	agent, err := NewWithError(cfg)
	if err != nil {
		t.Fatalf("agent creation failed: %v", err)
	}
	out, err := agent.Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "hello") {
		t.Errorf("output = %q", out)
	}

	calls, errs := rec.counts()
	if calls == 0 {
		t.Error("expected model call recorded for the agent turn")
	}
	for _, e := range errs {
		if e.component != "model" && e.component != "tool" && e.component != "agent" {
			t.Errorf("unexpected component %q", e.component)
		}
	}
}

func TestAgentMetricsRecordsToolErrors(t *testing.T) {
	rec := &recordingMetrics{}
	cfg := stubAgentConfig("mtools", []*Tool{errTool("fail")})
	cfg.Provider = newMultiTurnToolProvider([]string{"err_tool"})
	cfg.Metrics = rec

	agent, err := NewWithError(cfg)
	if err != nil {
		t.Fatalf("agent creation failed: %v", err)
	}
	// A tool error surfaces as an error result to the model; the run may still
	// succeed, but the tool component error must have been recorded.
	_, err = agent.Run(context.Background(), "do it")
	if err != nil {
		t.Logf("run failed: %v", err)
	}

	_, errs := rec.counts()
	components := map[string]int{}
	for _, e := range errs {
		components[e.component]++
	}
	if components["tool"] == 0 {
		t.Error("expected tool error to be recorded")
	}
}

func TestAgentMetricsRecordsAgentErrors(t *testing.T) {
	rec := &recordingMetrics{}
	cfg := stubAgentConfig("magent", nil)
	cfg.Metrics = rec
	cfg.Provider = &failProvider{err: errors.New("model unreachable")}

	agent, err := NewWithError(cfg)
	if err != nil {
		t.Fatalf("agent creation failed: %v", err)
	}
	if _, err := agent.Run(context.Background(), "hello"); err == nil {
		t.Fatal("expected run to fail")
	}

	_, errs := rec.counts()
	components := map[string]int{}
	for _, e := range errs {
		components[e.component]++
	}
	if components["agent"] != 1 {
		t.Errorf("agent errors = %d, want 1", components["agent"])
	}
}

func hasAttr(kvs []attribute.KeyValue, key, value string) bool {
	for _, kv := range kvs {
		if kv.Key == attribute.Key(key) && kv.Value.AsString() == value {
			return true
		}
	}
	return false
}
