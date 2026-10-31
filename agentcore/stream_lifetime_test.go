package agentcore

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

type lifecycleStreamProvider struct {
	stream func(context.Context) (<-chan StreamDelta, error)
}

func (p lifecycleStreamProvider) Complete(context.Context, *ProviderRequest) (*ProviderResponse, error) {
	return &ProviderResponse{}, nil
}
func (p lifecycleStreamProvider) Stream(ctx context.Context, _ *ProviderRequest) (<-chan StreamDelta, error) {
	return p.stream(ctx)
}

func TestModelTelemetryStreamsReleaseOnCancelErrorAndEOF(t *testing.T) {
	for _, kind := range []string{"metrics", "span", "combined"} {
		for _, mode := range []string{"eof", "error", "cancel-blocked", "cancel-idle", "startup-error", "startup-panic"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				accepted := make(chan struct{})
				var upstream context.Context
				cause := errors.New("original stream error")
				provider := lifecycleStreamProvider{stream: func(ctx context.Context) (<-chan StreamDelta, error) {
					upstream = ctx
					if mode == "startup-panic" {
						panic(cause)
					}
					if mode == "startup-error" {
						return nil, cause
					}
					input := make(chan StreamDelta)
					go func() {
						usage := TokenUsage{PromptTokens: 50, CacheReadTokens: 7}
						if mode == "cancel-idle" {
							<-ctx.Done()
							return
						}
						select {
						case input <- StreamDelta{Usage: &usage}:
						case <-ctx.Done():
							return
						}
						switch mode {
						case "eof":
							close(input)
						case "error":
							select {
							case input <- StreamDelta{Err: cause, Usage: &TokenUsage{CompletionTokens: 12}}:
							case <-ctx.Done():
							}
						case "cancel-blocked":
							select {
							case input <- StreamDelta{Content: "blocked downstream"}:
							case <-ctx.Done():
								return
							}
							close(accepted)
						}
						<-ctx.Done()
					}()
					return input, nil
				}}
				recorder := &recordingMetrics{}
				tracer, exporter := modelTestTracer()
				var wrapped Provider = provider
				if kind != "metrics" {
					wrapped = NewModelSpanMiddleware(tracer)(wrapped)
				}
				if kind != "span" {
					wrapped = NewModelMetricsMiddleware(recorder)(wrapped)
				}
				var stream <-chan StreamDelta
				var err error
				var recovered any
				func() {
					defer func() { recovered = recover() }()
					stream, err = wrapped.Stream(ctx, &ProviderRequest{Model: "mock"})
				}()
				if mode == "startup-panic" {
					if recovered != cause || upstream == nil || upstream.Err() == nil {
						t.Fatal("startup panic was masked or leaked context")
					}
					if kind != "metrics" && len(exporter.GetSpans()) != 1 {
						t.Fatal("startup panic leaked span")
					}
					return
				}
				if recovered != nil {
					t.Fatalf("unexpected startup panic: %v", recovered)
				}
				if mode == "startup-error" {
					if err != cause || upstream.Err() == nil {
						t.Fatal("startup error leaked context or was masked")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if mode == "cancel-idle" {
					cancel()
				}
				if mode == "cancel-blocked" {
					select {
					case delta := <-stream:
						if delta.Usage == nil {
							t.Fatal("missing initial usage")
						}
					case <-time.After(2 * time.Second):
						t.Fatal("initial usage never reached consumer")
					}
					select {
					case <-accepted:
					case <-time.After(2 * time.Second):
						t.Fatal("usage was not consumed")
					}
					cancel()
				}
				timeout := time.NewTimer(2 * time.Second)
				defer timeout.Stop()
				for stream != nil {
					select {
					case delta, ok := <-stream:
						if !ok {
							stream = nil
							continue
						}
						if delta.Err != nil && delta.Err != cause {
							t.Fatal("terminal error was changed")
						}
					case <-timeout.C:
						t.Fatal("telemetry wrapper remained blocked")
					}
				}
				if upstream.Err() == nil {
					t.Fatal("stream completion did not cancel owned context")
				}
				if kind != "span" {
					count, errs := recorder.counts()
					if count != 1 {
						t.Fatalf("metrics finalized %d times", count)
					}
					if mode == "eof" && len(errs) != 0 {
						t.Fatal("normal EOF was reported as cancellation")
					}
					if mode != "eof" && len(errs) != 1 {
						t.Fatalf("terminal error missing: %+v", errs)
					}
					if mode == "error" && errs[0].err != cause {
						t.Fatal("original terminal error was replaced")
					}
					if (mode == "cancel-idle" || mode == "cancel-blocked") && !errors.Is(errs[0].err, context.Canceled) {
						t.Fatal("cancellation was reported as a successful model call")
					}
					recorder.mu.Lock()
					call := recorder.modelCalls[0]
					recorder.mu.Unlock()
					if mode != "cancel-idle" && (call.promptTokens != 50 || call.cacheReadTokens != 7) {
						t.Fatal("observed usage was lost on cancellation/error")
					}
				}
				if kind != "metrics" {
					spans := exporter.GetSpans()
					// The inner span can finish just after an outer cancellation closes.
					deadline := time.Now().Add(time.Second)
					for len(spans) == 0 && time.Now().Before(deadline) {
						time.Sleep(time.Millisecond)
						spans = exporter.GetSpans()
					}
					if len(spans) != 1 {
						t.Fatalf("span ended %d times", len(spans))
					}
					if mode != "eof" && spans[0].Status.Code != codes.Error {
						t.Fatal("span did not record terminal cancellation/error")
					}
					if mode == "error" {
						attrs := attrMap(spans)["model.mock"]
						if attrs["gen_ai.usage.output_tokens"].Value != attribute.Int64Value(12) {
							t.Fatal("terminal usage missing from failed span")
						}
					}
				}
			})
		}
	}
}
