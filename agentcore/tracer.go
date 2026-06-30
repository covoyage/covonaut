package agentcore

import "context"

// Span represents a unit of work in a distributed trace.
// Implement this interface with OpenTelemetry, Datadog, Jaeger, or any other backend.
type Span interface {
	End()
	SetAttributes(attrs ...SpanAttribute)
	RecordError(err error)
	AddEvent(name string, attrs ...SpanAttribute)
}

// SpanAttribute is a key-value pair attached to a span.
type SpanAttribute struct {
	Key   string
	Value any
}

// Attr is a convenience constructor for SpanAttribute.
func Attr(key string, value any) SpanAttribute {
	return SpanAttribute{Key: key, Value: value}
}

// Tracer creates spans for tracing agent operations.
// Set Config.Tracer to plug in your preferred tracing backend.
type Tracer interface {
	Start(ctx context.Context, name string, attrs ...SpanAttribute) (context.Context, Span)
}

// noopTracer is the default when no tracer is configured.
type noopTracer struct{}
type noopSpan struct{}

func (noopTracer) Start(ctx context.Context, _ string, _ ...SpanAttribute) (context.Context, Span) {
	return ctx, noopSpan{}
}

func (noopSpan) End()                                  {}
func (noopSpan) SetAttributes(_ ...SpanAttribute)      {}
func (noopSpan) RecordError(_ error)                   {}
func (noopSpan) AddEvent(_ string, _ ...SpanAttribute) {}

// TracingMiddleware creates an Executor middleware that wraps each tool call in a trace span.
func TracingMiddleware(tracer Tracer) Middleware {
	return func(next ExecuteFunc) ExecuteFunc {
		return func(ctx context.Context, tc ToolCall) (string, error) {
			ctx, span := tracer.Start(ctx, "tool."+tc.Name,
				Attr("tool.name", tc.Name),
				Attr("tool.call_id", tc.ID),
			)
			defer span.End()

			result, err := next(ctx, tc)
			if err != nil {
				span.RecordError(err)
			}
			span.SetAttributes(Attr("tool.result_size", len(result)))
			return result, err
		}
	}
}
