package agentcore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
)

// RunInfo identifies one component invocation in a parent/child execution tree.
type RunInfo struct {
	RunID       string   `json:"run_id"`
	ParentRunID string   `json:"parent_run_id,omitempty"`
	Component   string   `json:"component"`
	Name        string   `json:"name,omitempty"`
	Path        []string `json:"path,omitempty"`
}

type runInfoContextKey struct{}

func RunInfoFromContext(ctx context.Context) (RunInfo, bool) {
	info, ok := ctx.Value(runInfoContextKey{}).(RunInfo)
	return info, ok
}

func WithRunInfo(ctx context.Context, info RunInfo) context.Context {
	info.Path = append([]string(nil), info.Path...)
	return context.WithValue(ctx, runInfoContextKey{}, info)
}

// StartComponentRun creates correlated metadata and a span for a component.
func StartComponentRun(ctx context.Context, tracer Tracer, component, name string, attrs ...SpanAttribute) (context.Context, Span, RunInfo) {
	parent, hasParent := RunInfoFromContext(ctx)
	info := RunInfo{RunID: newRunID(), Component: component, Name: name}
	if hasParent {
		info.ParentRunID = parent.RunID
		info.Path = append(append([]string(nil), parent.Path...), name)
	} else if name != "" {
		info.Path = []string{name}
	}
	ctx = WithRunInfo(ctx, info)
	if tracer == nil {
		tracer = noopTracer{}
	}
	common := []SpanAttribute{
		Attr("run.id", info.RunID),
		Attr("run.parent_id", info.ParentRunID),
		Attr("component", component),
		Attr("component.name", name),
		Attr("component.path", append([]string(nil), info.Path...)),
	}
	ctx, span := tracer.Start(ctx, component+"."+name, append(common, attrs...)...)
	ctx = WithRunInfo(ctx, info)
	return ctx, span, info
}

func newRunID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return newArtifactID()
	}
	return hex.EncodeToString(value[:])
}

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
// Input (tool arguments) and output (result content) are recorded as span attributes
// so observability backends can inspect what was passed to and returned by each tool.
//
// Multi-backend compatibility: different LLM observability platforms use different
// attribute names for input/output. We write all known conventions so data appears
// regardless of which backend is active:
//   - langfuse.observation.input/output  (Langfuse)
//   - langsmith.input/output             (LangSmith)
//   - input.value/output.value           (OpenInference / MLflow)
//   - tool.input/output                  (generic fallback for Jaeger, Tempo, etc.)
//
// Additionally, observation type/kind attributes ensure tool calls are correctly
// classified (not as LLM generations):
//   - langfuse.observation.type = "span"       (Langfuse)
//   - langsmith.span.kind = "tool"             (LangSmith)
func TracingMiddleware(tracer Tracer) Middleware {
	return func(next ExecuteFunc) ExecuteFunc {
		return func(ctx context.Context, tc ToolCall) (string, error) {
			input := truncateToolData(string(tc.Arguments), 4096)
			ctx, span, _ := StartComponentRun(ctx, tracer, "tool", tc.Name,
				Attr("tool.name", tc.Name),
				Attr("tool.call_id", tc.ID),
				// Input/output — multi-backend
				Attr("langfuse.observation.input", input),
				Attr("langsmith.input", input),
				Attr("input.value", input),
				Attr("tool.input", input),
				// Observation type — classify as span/tool, not generation
				Attr("langfuse.observation.type", "span"),
				Attr("langsmith.span.kind", "tool"),
			)
			defer span.End()

			result, err := next(ctx, tc)
			if err != nil {
				span.RecordError(err)
			}
			output := truncateToolData(result, 4096)
			span.SetAttributes(
				// Output — multi-backend
				Attr("langfuse.observation.output", output),
				Attr("langsmith.output", output),
				Attr("output.value", output),
				Attr("tool.output", output),
				Attr("tool.result_size", len(result)),
			)
			return result, err
		}
	}
}

// truncateToolData truncates tool input/output to maxLen characters,
// appending "..." if truncated. Prevents oversized span attributes.
func truncateToolData(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
