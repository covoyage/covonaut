package agentcore

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

type guardrailRejectProvider struct {
	callCount int
}

func (p *guardrailRejectProvider) Complete(ctx context.Context, req *ProviderRequest) (*ProviderResponse, error) {
	p.callCount++
	return &ProviderResponse{Content: "model response"}, nil
}

func (p *guardrailRejectProvider) Stream(ctx context.Context, req *ProviderRequest) (<-chan StreamDelta, error) {
	ch := make(chan StreamDelta, 1)
	ch <- StreamDelta{Content: "model response", Done: true}
	close(ch)
	p.callCount++
	return ch, nil
}

func TestAgentRun_AfterModelCallReject_PersistsResponseAndContinues(t *testing.T) {
	provider := &guardrailRejectProvider{}
	rejectCount := 0
	agent := New(Config{
		ModelConfig: ModelConfig{
			Name:     "guardrail",
			Model:    "stub",
			Provider: provider,
		},
		Lifecycle: LifecycleChain{
			&GuardrailHook{
				Validate: func(ctx context.Context, resp *ProviderResponse) error {
					rejectCount++
					if rejectCount == 1 {
						return errors.New("content rejected")
					}
					return nil
				},
			},
		},
		ExecutionConfig: ExecutionConfig{
			MaxTurns: 3,
		},
	})

	_, err := agent.Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs := agent.State().Messages()
	if len(msgs) < 3 {
		t.Fatalf("expected at least 3 messages (user + assistant + system error), got %d", len(msgs))
	}

	if msgs[1].Role != RoleAssistant {
		t.Fatalf("msgs[1] role = %q, want %q", msgs[1].Role, RoleAssistant)
	}
	if msgs[1].Content != "model response" {
		t.Fatalf("msgs[1] content = %q, want %q", msgs[1].Content, "model response")
	}

	if msgs[2].Role != RoleSystem {
		t.Fatalf("msgs[2] role = %q, want %q", msgs[2].Role, RoleSystem)
	}
	if msgs[2].Content != "Error: content rejected" {
		t.Fatalf("msgs[2] content = %q, want %q", msgs[2].Content, "Error: content rejected")
	}

	if provider.callCount < 2 {
		t.Fatalf("expected provider called at least 2 times (initial + retry), got %d", provider.callCount)
	}
}

func TestTransfer_InheritsParentToolsAndExtensions(t *testing.T) {
	parentTool := &Tool{
		Name:        "parent_tool",
		Description: "A tool from parent",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			return map[string]string{"result": "parent_tool_result"}, nil
		},
	}

	parentLifecycle := &GuardrailHook{
		Validate: func(ctx context.Context, resp *ProviderResponse) error {
			return nil
		},
	}

	transferProvider := &transferTestProvider{
		responses: []string{
			`I'll transfer to child`,
			`final answer from child`,
		},
	}

	parent := New(Config{
		ModelConfig: ModelConfig{
			Name:     "parent",
			Model:    "stub",
			Provider: transferProvider,
		},
		Tools: []*Tool{parentTool},
		Lifecycle: LifecycleChain{parentLifecycle},
		Handoffs: []HandoffConfig{
			{
				Name:        "child",
				Description: "Child agent",
				Mode:        HandoffTransfer,
				AgentConfig: Config{
					ModelConfig: ModelConfig{
						Name:     "child",
						Model:    "stub",
						Provider: transferProvider,
					},
					ExecutionConfig: ExecutionConfig{
						MaxTurns: 3,
					},
				},
			},
		},
		ExecutionConfig: ExecutionConfig{
			MaxTurns: 5,
		},
	})

	out, err := parent.Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "final answer from child" {
		t.Fatalf("output = %q", out)
	}

	// Verify the child agent received the parent's tool via inheritance.
	childReq := transferProvider.requests[1]
	found := false
	for _, tool := range childReq.Tools {
		if tool.Name == "parent_tool" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("child request tools = %v, missing parent_tool", childReq.Tools)
	}
}

type transferTestProvider struct {
	responses []string
	callCount int
	requests  []*ProviderRequest
}

func (p *transferTestProvider) Complete(ctx context.Context, req *ProviderRequest) (*ProviderResponse, error) {
	cp := *req
	cp.Messages = append([]Message(nil), req.Messages...)
	cp.Tools = append([]ToolDefinition(nil), req.Tools...)
	p.requests = append(p.requests, &cp)
	idx := p.callCount
	p.callCount++
	if idx < len(p.responses) {
		if idx == 0 {
			return &ProviderResponse{
				Content: p.responses[0],
				ToolCalls: []ToolCall{
					{
						ID:        "call_transfer",
						Name:      "transfer_to_child",
						Arguments: `{"message":"take over"}`,
					},
				},
			}, nil
		}
		return &ProviderResponse{Content: p.responses[idx]}, nil
	}
	return &ProviderResponse{Content: "default"}, nil
}

func (p *transferTestProvider) Stream(ctx context.Context, req *ProviderRequest) (<-chan StreamDelta, error) {
	ch := make(chan StreamDelta, 1)
	ch <- StreamDelta{Done: true}
	close(ch)
	return ch, nil
}

func TestRateLimitHook_ResetsAcrossRuns(t *testing.T) {
	hook := &RateLimitHook{MaxTurnsPerMinute: 2}
	provider := &guardrailRejectProvider{}

	agent := New(Config{
		ModelConfig: ModelConfig{
			Name:     "ratelimit",
			Model:    "stub",
			Provider: provider,
		},
		Lifecycle: LifecycleChain{hook},
		ExecutionConfig: ExecutionConfig{
			MaxTurns: 10,
		},
	})

	// First run: 1 turn, should succeed.
	_, err := agent.Run(context.Background(), "run1")
	if err != nil {
		t.Fatalf("run1 unexpected error: %v", err)
	}

	// Second run: counter should have reset, 1 turn, should succeed.
	_, err = agent.Run(context.Background(), "run2")
	if err != nil {
		t.Fatalf("run2 unexpected error: %v", err)
	}
}

func TestRateLimitHook_ExceedsLimit(t *testing.T) {
	hook := &RateLimitHook{MaxTurnsPerMinute: 1}
	dummyTool := &Tool{
		Name:        "dummy_tool",
		Description: "A dummy tool",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			return "ok", nil
		},
	}
	provider := &multiTurnProvider{
		responses: []string{"first", "second"},
	}

	agent := New(Config{
		ModelConfig: ModelConfig{
			Name:     "ratelimit",
			Model:    "stub",
			Provider: provider,
		},
		Tools: []*Tool{dummyTool},
		Lifecycle: LifecycleChain{hook},
		ExecutionConfig: ExecutionConfig{
			MaxTurns: 10,
		},
	})

	_, err := agent.Run(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected rate limit error")
	}
}

type multiTurnProvider struct {
	responses []string
	callCount int
}

func (p *multiTurnProvider) Complete(ctx context.Context, req *ProviderRequest) (*ProviderResponse, error) {
	idx := p.callCount
	p.callCount++
	if idx < len(p.responses) {
		if idx == 0 {
			return &ProviderResponse{
				Content: p.responses[0],
				ToolCalls: []ToolCall{
					{
						ID:        "call_1",
						Name:      "dummy_tool",
						Arguments: `{}`,
					},
				},
			}, nil
		}
		return &ProviderResponse{Content: p.responses[idx]}, nil
	}
	return &ProviderResponse{Content: "default"}, nil
}

func (p *multiTurnProvider) Stream(ctx context.Context, req *ProviderRequest) (<-chan StreamDelta, error) {
	ch := make(chan StreamDelta, 1)
	ch <- StreamDelta{Done: true}
	close(ch)
	return ch, nil
}

func TestAgent_Close_SharedEventBus_NotClosed(t *testing.T) {
	parent := New(Config{ModelConfig: ModelConfig{Name: "parent"}})
	parentBus := parent.eventBus

	child := New(Config{ModelConfig: ModelConfig{Name: "child"}})
	child.SetEventBus(parentBus)

	// Close child — must NOT close the shared (parent) EventBus
	child.Close()

	// Parent bus should still be usable
	parent.EmitEvent(&AgentStartEvent{baseEvent: newBase(EventAgentStart)})

	// Closing parent should work fine
	parent.Close()
}

func TestAgent_Close_OwnedEventBus_Closed(t *testing.T) {
	agent := New(Config{ModelConfig: ModelConfig{Name: "test"}})
	bus := agent.eventBus

	agent.Close()

	// After close, Emit should be a no-op (not panic)
	bus.Emit(&AgentStartEvent{baseEvent: newBase(EventAgentStart)}) // bus.Emit is public, safe to test
}

func TestAgent_Close_Idempotent(t *testing.T) {
	agent := New(Config{ModelConfig: ModelConfig{Name: "test"}})
	agent.Close()
	agent.Close() // should not panic
}

type hangingStreamProvider struct {
	ch chan StreamDelta
}

func (p *hangingStreamProvider) Complete(ctx context.Context, req *ProviderRequest) (*ProviderResponse, error) {
	return nil, errors.New("not implemented")
}

func (p *hangingStreamProvider) Stream(ctx context.Context, req *ProviderRequest) (<-chan StreamDelta, error) {
	p.ch = make(chan StreamDelta) // intentionally unbuffered and never closed
	return p.ch, nil
}

func TestAgentRun_Streaming_ContextCancellation(t *testing.T) {
	provider := &hangingStreamProvider{}
	agent := New(Config{
		ModelConfig: ModelConfig{
			Name:      "test",
			Provider:  provider,
			Streaming: true,
		},
	})

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		_, err := agent.Run(ctx, "hello")
		errCh <- err
	}()

	// Give the goroutine time to enter runStreaming and block on the channel
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected nil (clean stop) from cancelled context, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("agent.Run did not exit after context cancellation — streaming goroutine is stuck")
	}

	// Clean up the hanging provider channel
	close(provider.ch)
}
