package agentcore

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func modelTestTracer() (*OtelTracer, *tracetest.InMemoryExporter) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	return NewOtelTracer(provider.Tracer("test")), exporter
}

type recordingProvider struct {
	completeCalls int
	streamCalls   int
	usage         TokenUsage
	finishReason  string
	content       string
	streamDeltas  []StreamDelta
}

func (p *recordingProvider) Complete(ctx context.Context, req *ProviderRequest) (*ProviderResponse, error) {
	p.completeCalls++
	return &ProviderResponse{
		Content:      p.content,
		Usage:        p.usage,
		FinishReason: p.finishReason,
	}, nil
}

func (p *recordingProvider) Stream(ctx context.Context, req *ProviderRequest) (<-chan StreamDelta, error) {
	p.streamCalls++
	out := make(chan StreamDelta)
	go func() {
		defer close(out)
		for _, d := range p.streamDeltas {
			out <- d
		}
	}()
	return out, nil
}

func TestModelSpanMiddlewareComplete(t *testing.T) {
	tracer, exporter := modelTestTracer()
	inner := &recordingProvider{
		content:      "hello world",
		usage:        TokenUsage{PromptTokens: 100, CompletionTokens: 25, TotalTokens: 125},
		finishReason: "stop",
	}
	mw := NewModelSpanMiddleware(tracer)

	req := &ProviderRequest{
		Model:       "gpt-4o",
		Temperature: 0.7,
		MaxTokens:   2048,
		Messages: []Message{
			{Role: RoleUser, Content: "hi"},
		},
	}
	resp, err := mw(inner).Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != "hello world" {
		t.Fatalf("content = %q", resp.Content)
	}

	spans := attrMap(exporter.GetSpans())
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	attrs, ok := spans["model.gpt-4o"]
	if !ok {
		t.Fatalf("missing model.gpt-4o span, have %v", spans)
	}
	checks := map[string]attribute.Value{
		"gen_ai.operation.name":          attribute.StringValue("chat"),
		"gen_ai.request.model":           attribute.StringValue("gpt-4o"),
		"gen_ai.request.max_tokens":      attribute.Int64Value(2048),
		"gen_ai.request.temperature":     attribute.Float64Value(0.7),
		"gen_ai.response.model":          attribute.StringValue("gpt-4o"),
		"gen_ai.usage.input_tokens":      attribute.Int64Value(100),
		"gen_ai.usage.output_tokens":     attribute.Int64Value(25),
		"gen_ai.usage.total_tokens":      attribute.Int64Value(125),
		"gen_ai.response.finish_reasons": attribute.StringSliceValue([]string{"stop"}),
	}
	for key, want := range checks {
		kv, ok := attrs[key]
		if !ok {
			t.Errorf("missing attr %q", key)
			continue
		}
		if kv.Value != want {
			t.Errorf("attr %s = %v, want %v", key, kv.Value, want)
		}
	}
	if got := attrs["gen_ai.prompt"].Value.AsString(); got == "" || len(got) < 10 {
		t.Errorf("gen_ai.prompt = %q, want serialized messages", got)
	}
	if got := attrs["gen_ai.completion"].Value.AsString(); got != "hello world" {
		t.Errorf("gen_ai.completion = %q", got)
	}
}

func TestModelSpanMiddlewareSkipsExistingModelSpan(t *testing.T) {
	tracer, exporter := modelTestTracer()
	inner := &recordingProvider{content: "ok"}
	// The agent's own callProvider starts a model span before calling the
	// provider chain; the middleware must not create a second one.
	ctx, span, _ := StartComponentRun(context.Background(), tracer, "model", "gpt-4o")
	defer span.End()

	mw := NewModelSpanMiddleware(tracer)
	_, err := mw(inner).Complete(ctx, &ProviderRequest{Model: "gpt-4o"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if inner.completeCalls != 1 {
		t.Fatalf("inner called %d times", inner.completeCalls)
	}
	span.End()
	if len(exporter.GetSpans()) != 1 {
		t.Fatalf("expected only the callProvider span, got %d", len(exporter.GetSpans()))
	}
}

func TestModelSpanMiddlewareDoubleWrapSingleSpan(t *testing.T) {
	tracer, exporter := modelTestTracer()
	inner := &recordingProvider{content: "ok"}
	// Wrapping an already-wrapped provider must not double-trace.
	chain := NewModelSpanMiddleware(tracer)(NewModelSpanMiddleware(tracer)(inner))
	_, err := chain.Complete(context.Background(), &ProviderRequest{Model: "gpt-4o"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(exporter.GetSpans()) != 1 {
		t.Fatalf("expected 1 span after double wrap, got %d", len(exporter.GetSpans()))
	}
}

func TestModelSpanMiddlewareStreamRecordsUsage(t *testing.T) {
	tracer, exporter := modelTestTracer()
	inner := &recordingProvider{
		streamDeltas: []StreamDelta{
			{Content: "hel"},
			{Content: "lo"},
			{Usage: &TokenUsage{PromptTokens: 50, CompletionTokens: 10, TotalTokens: 60}, FinishReason: "stop", Done: true},
		},
	}
	mw := NewModelSpanMiddleware(tracer)
	stream, err := mw(inner).Stream(context.Background(), &ProviderRequest{Model: "gpt-4o"})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var got string
	for d := range stream {
		got += d.Content
	}
	if got != "hello" {
		t.Fatalf("streamed content = %q", got)
	}

	attrs := attrMap(exporter.GetSpans())["model.gpt-4o"]
	for key, want := range map[string]attribute.Value{
		"gen_ai.usage.input_tokens":      attribute.Int64Value(50),
		"gen_ai.usage.output_tokens":     attribute.Int64Value(10),
		"gen_ai.usage.total_tokens":      attribute.Int64Value(60),
		"gen_ai.response.finish_reasons": attribute.StringSliceValue([]string{"stop"}),
	} {
		kv, ok := attrs[key]
		if !ok || kv.Value != want {
			t.Errorf("attr %s = %v (present=%v), want %v", key, kv.Value, ok, want)
		}
	}
	if got := attrs["gen_ai.completion"].Value.AsString(); got != "hello" {
		t.Errorf("gen_ai.completion = %q", got)
	}
}

func TestModelSpanMiddlewareNilTracerPassesThrough(t *testing.T) {
	inner := &recordingProvider{content: "ok"}
	_, err := NewModelSpanMiddleware(nil)(inner).Complete(context.Background(), &ProviderRequest{Model: "gpt-4o"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if inner.completeCalls != 1 {
		t.Fatalf("inner not called")
	}
}
