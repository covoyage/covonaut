package agentcore

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type transitionSummaryProvider struct {
	complete func(context.Context, *ProviderRequest) (*ProviderResponse, error)
}

func (p transitionSummaryProvider) Complete(ctx context.Context, request *ProviderRequest) (*ProviderResponse, error) {
	return p.complete(ctx, request)
}
func (transitionSummaryProvider) Stream(context.Context, *ProviderRequest) (<-chan StreamDelta, error) {
	return nil, errors.New("unused")
}

func TestStrictTransitionCompactionNeverUsesLossyFailureFallback(t *testing.T) {
	for _, kind := range []string{"provider-error", "invalid-json", "no-response", "empty-content", "empty-object", "no-shrink", "cancel", "success"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cause := errors.New("summary unavailable")
			messages := []Message{{Role: RoleSystem, Content: "system"}}
			for range 20 {
				messages = append(messages, Message{Role: RoleUser, Content: strings.Repeat("history ", 500)})
			}
			baseline := make([]Message, len(messages))
			copy(baseline, messages)
			provider := transitionSummaryProvider{complete: func(context.Context, *ProviderRequest) (*ProviderResponse, error) {
				switch kind {
				case "provider-error":
					return nil, cause
				case "invalid-json":
					return &ProviderResponse{Content: "invalid"}, nil
				case "no-response":
					return nil, nil
				case "empty-content":
					return &ProviderResponse{Content: " \n "}, nil
				case "empty-object":
					return &ProviderResponse{Content: `{"active_task":" "}`}, nil
				case "no-shrink":
					return &ProviderResponse{Content: `{"active_task":"` + strings.Repeat("bigger ", 20000) + `"}`}, nil
				case "cancel":
					cancel()
				}
				return &ProviderResponse{Content: `{"active_task":"human request"}`}, nil
			}}
			engine := NewCompressorEngine(ContextEngineConfig{Model: "old", Provider: provider, ContextWindow: 16000, KeepRecentTokens: 1000, StructuredCompaction: true})
			internal := engine.(*CompressorEngine)
			beforeState := *internal.state
			strict := engine.(interface {
				CompressForTransition(context.Context, []Message, string) ([]Message, int64, error)
			})
			result, cut, err := strict.CompressForTransition(ctx, messages, "human request")
			if (err == nil) != (kind == "success") {
				t.Fatalf("incorrect strict result: cut=%d err=%v", cut, err)
			}
			if kind == "provider-error" && !errors.Is(err, cause) {
				t.Fatal("original summary error was lost")
			}
			if kind == "success" && (cut <= 0 || EstimateMessagesTokens(result) >= EstimateMessagesTokens(messages)) {
				t.Fatal("successful strict compaction did not shrink")
			}
			if !reflect.DeepEqual(messages, baseline) {
				t.Fatal("strict compaction mutated the input history")
			}
			if err != nil && (!reflect.DeepEqual(*internal.state, beforeState) || internal.CompressionCount() != 0) {
				t.Fatal("failed strict compaction committed summary state or counters")
			}
		})
	}
}

type transitionPreviewHook struct {
	BaseLifecycleHook
	t *testing.T
}

func (h transitionPreviewHook) BeforeModelCall(ctx context.Context, arc *AgentRunContext, call *ModelCallContext) error {
	preview, err := arc.Agent.PreviewModelRequest(ctx, arc.Agent.State().Messages())
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(preview, call.Request) {
		h.t.Error("preview diverged from actual loop request construction")
	}
	return nil
}

func TestModelRequestPreviewSharesConstructionWithoutDispatchOrStateMutation(t *testing.T) {
	var calls int
	provider := transitionSummaryProvider{complete: func(context.Context, *ProviderRequest) (*ProviderResponse, error) {
		calls++
		return &ProviderResponse{Content: "done"}, nil
	}}
	core := New(Config{ModelConfig: ModelConfig{Model: "target", Provider: provider, MaxTokens: 200},
		Tools: []*Tool{{Name: "read", Parameters: map[string]any{"type": "object"}}},
		TransformContext: func(_ context.Context, messages []Message) []Message {
			messages = append(messages, Message{Role: RoleUser, Content: "request-only context"})
			return messages
		},
		Lifecycle: transitionPreviewHook{t: t},
	})
	defer core.Close()
	messages := []Message{{Role: RoleUser, Content: "input", Metadata: map[string]any{"source": "human"}}}
	request, err := core.PreviewModelRequest(context.Background(), messages)
	if err != nil {
		t.Fatal(err)
	}
	request.Messages[0].Metadata["source"] = "mutated"
	if calls != 0 || len(core.State().Messages()) != 0 || messages[0].Metadata["source"] != "human" || len(request.Tools) != 1 || request.MaxTokens != 200 {
		t.Fatal("preview dispatched, mutated input or omitted schemas/output settings")
	}
	if _, err := core.Run(context.Background(), "real input"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("model call count changed")
	}
}
