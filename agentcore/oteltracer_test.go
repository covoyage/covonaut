package agentcore

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func attrMap(spans []tracetest.SpanStub) map[string]map[string]attribute.KeyValue {
	out := make(map[string]map[string]attribute.KeyValue)
	for _, s := range spans {
		m := make(map[string]attribute.KeyValue)
		for _, kv := range s.Attributes {
			m[string(kv.Key)] = kv
		}
		out[s.Name] = m
	}
	return out
}

func TestOtelTracerAdaptsSpans(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	tracer := NewOtelTracer(provider.Tracer("test"))

	ctx, span, info := StartComponentRun(context.Background(), tracer, "model", "gpt-4o",
		Attr("model", "gpt-4o"),
		Attr("streaming", true),
		Attr("max_tokens", int64(2048)),
		Attr("temperature", 0.7),
		Attr("tags", []string{"a", "b"}),
	)
	span.SetAttributes(
		Attr("gen_ai.usage.input_tokens", int64(100)),
		Attr("gen_ai.response.finish_reasons", []string{"stop"}),
	)
	span.AddEvent("gen_ai.choice", Attr("index", 0))
	span.RecordError(errors.New("boom"))
	span.End()

	// Nested span becomes a child via the context returned by Start.
	_, child, _ := StartComponentRun(ctx, tracer, "tool", "search")
	child.End()

	spans := exporter.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("expected 2 spans, got %d", len(spans))
	}
	root := spans[0]
	childSpan := spans[1]

	if root.Name != "model.gpt-4o" {
		t.Fatalf("root name = %q", root.Name)
	}
	attrs := attrMap(spans)["model.gpt-4o"]
	got := func(key string) attribute.Value {
		kv, ok := attrs[key]
		if !ok {
			t.Fatalf("missing attribute %q (have %v)", key, attrs)
		}
		return kv.Value
	}
	if got("model").AsString() != "gpt-4o" {
		t.Fatalf("model attr = %v", got("model"))
	}
	if got("streaming").AsBool() != true {
		t.Fatalf("streaming attr = %v", got("streaming"))
	}
	if got("max_tokens").AsInt64() != 2048 {
		t.Fatalf("max_tokens attr = %v", got("max_tokens"))
	}
	if got("temperature").AsFloat64() != 0.7 {
		t.Fatalf("temperature attr = %v", got("temperature"))
	}
	if got("gen_ai.usage.input_tokens").AsInt64() != 100 {
		t.Fatalf("usage attr = %v", got("gen_ai.usage.input_tokens"))
	}
	if tags := got("tags").AsStringSlice(); len(tags) != 2 || tags[0] != "a" {
		t.Fatalf("tags attr = %v", tags)
	}
	if got("run.id").AsString() != info.RunID {
		t.Fatalf("run.id = %q, want %q", got("run.id").AsString(), info.RunID)
	}
	if got("run.parent_id").AsString() != "" {
		t.Fatalf("root run.parent_id = %q, want empty", got("run.parent_id").AsString())
	}

	if root.Status.Code != codes.Error {
		t.Fatalf("status = %v, want error", root.Status.Code)
	}
	foundException := false
	foundChoice := false
	for _, e := range root.Events {
		switch e.Name {
		case "exception":
			foundException = true
		case "gen_ai.choice":
			foundChoice = true
		}
	}
	if !foundException {
		t.Fatalf("expected exception event, got %+v", root.Events)
	}
	if !foundChoice {
		t.Fatal("missing gen_ai.choice event")
	}

	if childSpan.Name != "tool.search" {
		t.Fatalf("child name = %q", childSpan.Name)
	}
	if childSpan.Parent.SpanID() != root.SpanContext.SpanID() {
		t.Fatal("child span should be parented under root via context")
	}
	if c := attrMap(spans)["tool.search"]; c["run.parent_id"].Value.AsString() != info.RunID {
		t.Fatalf("child run.parent_id = %q, want %q", c["run.parent_id"].Value.AsString(), info.RunID)
	}
}

func TestOtelTracerNilBehavesLikeNoop(t *testing.T) {
	ctx, span, _ := StartComponentRun(context.Background(), NewOtelTracer(nil), "tool", "noop")
	span.SetAttributes(Attr("k", "v"))
	span.RecordError(errors.New("x"))
	span.AddEvent("e")
	span.End()
	if _, ok := RunInfoFromContext(ctx); !ok {
		t.Fatal("run info should still be present")
	}
}

type genAIUsageProvider struct{}

func (*genAIUsageProvider) Complete(context.Context, *ProviderRequest) (*ProviderResponse, error) {
	return &ProviderResponse{
		Content:      "hi",
		Usage:        TokenUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		FinishReason: "stop",
	}, nil
}

func (*genAIUsageProvider) Stream(context.Context, *ProviderRequest) (<-chan StreamDelta, error) {
	return nil, nil
}

func TestAgentModelSpanCarriesGenAIUsage(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	agent := New(StubConfig(&genAIUsageProvider{},
		WithTracer(NewOtelTracer(provider.Tracer("test"))),
	))
	if _, err := agent.Run(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}

	attrs := attrMap(exporter.GetSpans())["model.stub"]
	if attrs == nil {
		t.Fatalf("no model.stub span recorded: %v", exporter.GetSpans())
	}
	if got := attrs["gen_ai.usage.input_tokens"].Value.AsInt64(); got != 10 {
		t.Fatalf("input_tokens = %d, want 10", got)
	}
	if got := attrs["gen_ai.usage.output_tokens"].Value.AsInt64(); got != 5 {
		t.Fatalf("output_tokens = %d, want 5", got)
	}
	if got := attrs["gen_ai.usage.total_tokens"].Value.AsInt64(); got != 15 {
		t.Fatalf("total_tokens = %d, want 15", got)
	}
	if got := attrs["gen_ai.request.model"].Value.AsString(); got != "stub" {
		t.Fatalf("request.model = %q", got)
	}
	if got := attrs["gen_ai.prompt"].Value.AsString(); got == "" {
		t.Fatal("expected non-empty gen_ai.prompt")
	}
	if got := attrs["gen_ai.completion"].Value.AsString(); got != "hi" {
		t.Fatalf("completion = %q", got)
	}
	if got := attrs["gen_ai.response.finish_reasons"].Value.AsStringSlice(); len(got) != 1 || got[0] != "stop" {
		t.Fatalf("finish_reasons = %v", got)
	}
}

func TestGenAIPromptSerialization(t *testing.T) {
	prompt := genAIPromptFromMessages([]Message{
		{Role: RoleSystem, Content: "sys"},
		{Role: RoleUser, Content: "hi"},
	})
	if prompt == "" || !strings.Contains(prompt, "hi") {
		t.Fatalf("prompt = %q", prompt)
	}
	if genAIPromptFromMessages(nil) != "" {
		t.Fatal("empty messages should produce empty prompt")
	}
}

func TestTruncateGenAIContent(t *testing.T) {
	short := "hello"
	if truncateGenAIContent(short) != short {
		t.Fatal("short content should pass through")
	}
	long := strings.Repeat("界", maxGenAIAttributeBytes)
	truncated := truncateGenAIContent(long)
	if len(truncated) > maxGenAIAttributeBytes+32 {
		t.Fatalf("truncated length = %d", len(truncated))
	}
	if !strings.HasSuffix(truncated, "...[truncated]") {
		t.Fatalf("truncation marker missing: %q", truncated[len(truncated)-20:])
	}
}
