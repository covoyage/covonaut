package agentcore

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// NewOtelMetrics adapts the agentcore Metrics contract to an OpenTelemetry
// meter, emitting standard GenAI semantic-convention metrics plus the library's
// own error counter. The meter is typically provided by the host's metrics SDK;
// a nil meter yields a noop adapter.
func NewOtelMetrics(meter metric.Meter) Metrics {
	if meter == nil {
		return noopMetrics{}
	}
	tokenUsage, err := meter.Int64Counter(
		"gen_ai.client.token.usage",
		metric.WithDescription("Number of input and output tokens used by LLM calls"),
		metric.WithUnit("{token}"),
	)
	if err != nil {
		// On failure the SDK returns a noop instrument; fall back so the
		// adapter stays usable.
		tokenUsage, _ = meter.Int64Counter("gen_ai.client.token.usage")
	}
	opDuration, err := meter.Float64Histogram(
		"gen_ai.client.operation.duration",
		metric.WithDescription("Duration of LLM operations"),
		metric.WithUnit("s"),
	)
	if err != nil {
		opDuration, _ = meter.Float64Histogram("gen_ai.client.operation.duration")
	}
	errors, err := meter.Int64Counter(
		"agentcore.errors",
		metric.WithDescription("Number of failed component runs, by component"),
		metric.WithUnit("{error}"),
	)
	if err != nil {
		errors, _ = meter.Int64Counter("agentcore.errors")
	}
	return &otelMetrics{tokenUsage: tokenUsage, opDuration: opDuration, errors: errors}
}

type otelMetrics struct {
	tokenUsage metric.Int64Counter
	opDuration metric.Float64Histogram
	errors     metric.Int64Counter
}

func (m *otelMetrics) RecordModelCall(ctx context.Context, req *ProviderRequest, usage *TokenUsage, start time.Time) {
	if req == nil {
		return
	}
	base := []attribute.KeyValue{
		attribute.String("gen_ai.operation.name", "chat"),
		attribute.String("gen_ai.request.model", req.Model),
	}

	if usage != nil {
		if usage.PromptTokens > 0 {
			m.tokenUsage.Add(ctx, usage.PromptTokens,
				metric.WithAttributes(append(base, attribute.String("gen_ai.token.type", "input"))...))
		}
		if usage.CompletionTokens > 0 {
			m.tokenUsage.Add(ctx, usage.CompletionTokens,
				metric.WithAttributes(append(base, attribute.String("gen_ai.token.type", "output"))...))
		}
	}

	m.opDuration.Record(ctx, time.Since(start).Seconds(), metric.WithAttributes(base...))
}

func (m *otelMetrics) RecordError(ctx context.Context, component string, _ error) {
	if component == "" {
		return
	}
	m.errors.Add(ctx, 1, metric.WithAttributes(
		attribute.String("component", component),
	))
}
