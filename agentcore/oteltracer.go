package agentcore

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// OtelTracer adapts the agentcore Tracer interface to OpenTelemetry so that
// agent/model/tool spans are emitted as standard OTLP spans (including the
// GenAI semantic convention attributes set elsewhere in agentcore).
type OtelTracer struct {
	tracer trace.Tracer
}

// NewOtelTracer wraps an OpenTelemetry tracer. Use nil-safe: a nil *OtelTracer
// behaves like the noop tracer.
func NewOtelTracer(t trace.Tracer) *OtelTracer {
	return &OtelTracer{tracer: t}
}

// Start implements agentcore.Tracer. The returned context carries the created
// span so nested StartComponentRun calls become child spans automatically.
func (t *OtelTracer) Start(ctx context.Context, name string, attrs ...SpanAttribute) (context.Context, Span) {
	if t == nil || t.tracer == nil {
		return ctx, noopSpan{}
	}
	opts := []trace.SpanStartOption{trace.WithSpanKind(trace.SpanKindClient)}
	if kv := toOtelAttributes(attrs); len(kv) > 0 {
		opts = append(opts, trace.WithAttributes(kv...))
	}
	ctx, span := t.tracer.Start(ctx, name, opts...)
	return ctx, &otelSpan{span: span}
}

// otelSpan adapts agentcore.Span to an OpenTelemetry span.
type otelSpan struct {
	span trace.Span
}

func (s *otelSpan) End() {
	if s == nil || s.span == nil {
		return
	}
	s.span.End()
}

func (s *otelSpan) SetAttributes(attrs ...SpanAttribute) {
	if s == nil || s.span == nil {
		return
	}
	s.span.SetAttributes(toOtelAttributes(attrs)...)
}

func (s *otelSpan) RecordError(err error) {
	if s == nil || s.span == nil || err == nil {
		return
	}
	s.span.RecordError(err)
	s.span.SetStatus(codes.Error, err.Error())
}

func (s *otelSpan) AddEvent(name string, attrs ...SpanAttribute) {
	if s == nil || s.span == nil {
		return
	}
	if kv := toOtelAttributes(attrs); len(kv) > 0 {
		s.span.AddEvent(name, trace.WithAttributes(kv...))
	} else {
		s.span.AddEvent(name)
	}
}

// toOtelAttributes converts agentcore SpanAttributes to OTel key-values,
// mapping the supported value types to their OTel attribute representations.
func toOtelAttributes(attrs []SpanAttribute) []attribute.KeyValue {
	if len(attrs) == 0 {
		return nil
	}
	kv := make([]attribute.KeyValue, 0, len(attrs))
	for _, a := range attrs {
		kv = append(kv, toOtelAttribute(a))
	}
	return kv
}

func toOtelAttribute(a SpanAttribute) attribute.KeyValue {
	switch v := a.Value.(type) {
	case string:
		return attribute.String(a.Key, v)
	case []string:
		return attribute.StringSlice(a.Key, v)
	case int:
		return attribute.Int(a.Key, v)
	case int64:
		return attribute.Int64(a.Key, v)
	case []int64:
		return attribute.Int64Slice(a.Key, v)
	case bool:
		return attribute.Bool(a.Key, v)
	case float64:
		return attribute.Float64(a.Key, v)
	default:
		return attribute.String(a.Key, fmt.Sprint(v))
	}
}
