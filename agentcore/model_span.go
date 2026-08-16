package agentcore

import (
	"context"
)

// NewModelSpanMiddleware wraps every provider call in a "model" component span
// carrying GenAI semantic-convention attributes (gen_ai.*), so observability
// backends (Langfuse, Jaeger, ...) can render per-call generations with token
// usage. It complements the span created by callProvider for the agent's own
// turns and covers direct/auxiliary provider calls that bypass callProvider
// (context compression, title generation, review, guardrails, etc.).
//
// The middleware is safe to apply to an already-instrumented chain: it skips
// when the context already carries a model RunInfo — created by callProvider
// or by a nested middleware instance — so a single LLM call is never traced
// twice. Passing a nil tracer yields a pass-through middleware (tracing off).
func NewModelSpanMiddleware(tracer Tracer) func(Provider) Provider {
	if tracer == nil {
		tracer = noopTracer{}
	}
	return func(inner Provider) Provider {
		return &modelSpanMiddleware{tracer: tracer, inner: inner}
	}
}

type modelSpanMiddleware struct {
	tracer Tracer
	inner  Provider
}

// alreadyHasModelSpan reports whether ctx already carries a model span; when
// true the middleware must not open a second one.
func alreadyHasModelSpan(ctx context.Context) bool {
	info, ok := RunInfoFromContext(ctx)
	return ok && info.Component == "model"
}

func modelRequestAttrs(req *ProviderRequest) []SpanAttribute {
	return []SpanAttribute{
		Attr("gen_ai.operation.name", "chat"),
		Attr("gen_ai.request.model", req.Model),
		Attr("gen_ai.request.max_tokens", req.MaxTokens),
		Attr("gen_ai.request.temperature", req.Temperature),
	}
}

func modelResponseAttrs(req *ProviderRequest, usage *TokenUsage, finishReason, content, prompt string) []SpanAttribute {
	attrs := []SpanAttribute{
		Attr("gen_ai.response.model", req.Model),
	}
	if usage != nil {
		attrs = append(attrs,
			Attr("gen_ai.usage.input_tokens", usage.PromptTokens),
			Attr("gen_ai.usage.output_tokens", usage.CompletionTokens),
			Attr("gen_ai.usage.total_tokens", usage.TotalTokens),
		)
	}
	if finishReason != "" {
		attrs = append(attrs, Attr("gen_ai.response.finish_reasons", []string{finishReason}))
	}
	if prompt != "" {
		attrs = append(attrs, Attr("gen_ai.prompt", prompt))
	}
	if content != "" {
		attrs = append(attrs, Attr("gen_ai.completion", truncateGenAIContent(content)))
	}
	return attrs
}

func (m *modelSpanMiddleware) Complete(ctx context.Context, req *ProviderRequest) (*ProviderResponse, error) {
	if alreadyHasModelSpan(ctx) {
		return m.inner.Complete(ctx, req)
	}
	ctx, span, _ := StartComponentRun(ctx, m.tracer, "model", req.Model, modelRequestAttrs(req)...)
	defer span.End()
	resp, err := m.inner.Complete(ctx, req)
	if err != nil {
		span.RecordError(err)
	}
	if resp != nil {
		span.SetAttributes(modelResponseAttrs(req, &resp.Usage, resp.FinishReason, resp.Content, genAIPromptFromMessages(req.Messages))...)
	}
	return resp, err
}

func (m *modelSpanMiddleware) Stream(ctx context.Context, req *ProviderRequest) (<-chan StreamDelta, error) {
	if alreadyHasModelSpan(ctx) {
		return m.inner.Stream(ctx, req)
	}
	ctx, span, _ := StartComponentRun(ctx, m.tracer, "model", req.Model, modelRequestAttrs(req)...)
	stream, err := m.inner.Stream(ctx, req)
	if err != nil {
		span.RecordError(err)
		span.End()
		return stream, err
	}

	out := make(chan StreamDelta)
	go func() {
		defer close(out)
		defer span.End()
		var usage *TokenUsage
		var finishReason, content string
		for delta := range stream {
			if delta.Usage != nil {
				usage = delta.Usage
			}
			if delta.FinishReason != "" {
				finishReason = delta.FinishReason
			}
			if delta.Content != "" {
				content += delta.Content
			}
			out <- delta
		}
		span.SetAttributes(modelResponseAttrs(req, usage, finishReason, content, genAIPromptFromMessages(req.Messages))...)
	}()
	return out, nil
}
