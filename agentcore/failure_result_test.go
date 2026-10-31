package agentcore

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type explicitFailure struct{ cause error }

func (e *explicitFailure) Error() string { return "batch failed" }
func (e *explicitFailure) Unwrap() error { return e.cause }
func (*explicitFailure) ToolFailureResult() any {
	return map[string]any{"status": "partial", "outcomes": []string{"completed", "unknown", "skipped"}}
}

type failureResultProvider struct {
	calls       int
	payloadSeen bool
}

func (p *failureResultProvider) Complete(_ context.Context, request *ProviderRequest) (*ProviderResponse, error) {
	p.calls++
	if p.calls == 1 {
		return &ProviderResponse{ToolCalls: []ToolCall{{ID: "call", Name: "batch", Arguments: `{}`}}}, nil
	}
	for _, message := range request.Messages {
		if message.Role == RoleTool && message.ToolCallID == "call" {
			var data struct {
				Status   string   `json:"status"`
				Outcomes []string `json:"outcomes"`
			}
			if json.Unmarshal([]byte(message.Content), &data) == nil && data.Status == "partial" && len(data.Outcomes) == 3 {
				p.payloadSeen = true
			}
		}
	}
	return &ProviderResponse{Content: "handled"}, nil
}
func (*failureResultProvider) Stream(context.Context, *ProviderRequest) (<-chan StreamDelta, error) {
	return nil, errors.New("unused")
}

func TestExplicitFailureResultSurvivesInvokeExecutorAndConversation(t *testing.T) {
	cause := errors.New("partial write")
	provider := &failureResultProvider{}
	core := New(Config{ModelConfig: ModelConfig{Provider: provider}, Tools: []*Tool{{Name: "batch", Parameters: map[string]any{"type": "object"},
		Func: func(context.Context, json.RawMessage) (any, error) { return nil, &explicitFailure{cause: cause} }}}})
	defer core.Close()
	output, err := core.InvokeTool(context.Background(), "batch", json.RawMessage(`{}`))
	if err == nil || !errors.Is(err, cause) || !json.Valid([]byte(output)) {
		t.Fatal("direct invocation dropped the opt-in failure payload or error cause")
	}
	failed := false
	core.On(EventToolCallEnd, func(event Event) {
		if event.(*ToolCallEndEvent).Err != nil {
			failed = true
		}
	})
	if _, err := core.Run(context.Background(), "run batch"); err != nil {
		t.Fatal(err)
	}
	if !provider.payloadSeen || !failed {
		t.Fatal("structured payload was lost or tool failure became success")
	}
}

func TestCancellationDoesNotEraseExplicitCompletedEffects(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	core := New(Config{ModelConfig: ModelConfig{Provider: &failureResultProvider{}}, Tools: []*Tool{{Name: "batch", Parameters: map[string]any{"type": "object"},
		Func: func(context.Context, json.RawMessage) (any, error) {
			cancel()
			return nil, &explicitFailure{cause: context.Canceled}
		}}}})
	defer core.Close()
	core.Run(ctx, "run batch")
	found := false
	for _, message := range core.State().Messages() {
		if message.Role == RoleTool {
			found = json.Valid([]byte(message.Content))
		}
	}
	if !found {
		t.Fatal("cancellation replaced the explicit failure report with a generic interruption")
	}
}

func TestOrdinaryToolErrorsDoNotExposeUnmarkedResults(t *testing.T) {
	cause := errors.New("ordinary failure")
	core := New(Config{Tools: []*Tool{{Name: "ordinary", Func: func(context.Context, json.RawMessage) (any, error) { return "not intended for transport", cause }}}})
	defer core.Close()
	result, err := core.InvokeTool(context.Background(), "ordinary", json.RawMessage(`{}`))
	if result != "" || !errors.Is(err, cause) {
		t.Fatal("ordinary tool error behavior changed")
	}
}
