package agentcore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// --- Stub provders ---

// constantProvider returns the same content for every call.
type constantProvider struct{ content string }

func (c *constantProvider) Complete(_ context.Context, _ *ProviderRequest) (*ProviderResponse, error) {
	return &ProviderResponse{Content: c.content}, nil
}

func (c *constantProvider) Stream(_ context.Context, _ *ProviderRequest) (<-chan StreamDelta, error) {
	ch := make(chan StreamDelta, 2)
	ch <- StreamDelta{Content: c.content}
	ch <- StreamDelta{Done: true}
	close(ch)
	return ch, nil
}

// echoProvider returns a provider that echoes the last user message.
type echoProvider struct{}

func (e *echoProvider) Complete(_ context.Context, req *ProviderRequest) (*ProviderResponse, error) {
	content := lastUserContent(req.Messages)
	return &ProviderResponse{Content: "echo:" + content}, nil
}

func (e *echoProvider) Stream(_ context.Context, req *ProviderRequest) (<-chan StreamDelta, error) {
	content := lastUserContent(req.Messages)
	ch := make(chan StreamDelta, 2)
	ch <- StreamDelta{Content: "echo:" + content}
	ch <- StreamDelta{Done: true}
	close(ch)
	return ch, nil
}

// interruptProvider returns a provider that on the first call returns a tool call
// to a tool that returns ErrInterrupt, and on subsequent calls returns content.
type interruptProvider struct {
	mu        sync.Mutex
	callCount int
	toolName  string
	responses []string
}

func newInterruptProvider(toolName string, responses ...string) *interruptProvider {
	if len(responses) == 0 {
		responses = []string{"resumed"}
	}
	return &interruptProvider{toolName: toolName, responses: responses}
}

func (p *interruptProvider) Complete(_ context.Context, req *ProviderRequest) (*ProviderResponse, error) {
	p.mu.Lock()
	call := p.callCount
	p.callCount++
	p.mu.Unlock()

	if call == 0 {
		return &ProviderResponse{
			ToolCalls: []ToolCall{
				{ID: "call_1", Name: p.toolName, Arguments: `{"input":"test"}`},
			},
		}, nil
	}

	idx := call - 1
	if idx >= len(p.responses) {
		idx = len(p.responses) - 1
	}
	return &ProviderResponse{Content: p.responses[idx]}, nil
}

func (p *interruptProvider) Stream(ctx context.Context, req *ProviderRequest) (<-chan StreamDelta, error) {
	resp, _ := p.Complete(ctx, req)
	ch := make(chan StreamDelta, 2)
	if resp.Content != "" {
		ch <- StreamDelta{Content: resp.Content}
	}
	for _, tc := range resp.ToolCalls {
		ch <- StreamDelta{ToolCalls: []ToolCallDelta{
			{Index: 0, ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments},
		}}
	}
	ch <- StreamDelta{Done: true}
	close(ch)
	return ch, nil
}

// multiTurnProvider returns tool calls on early calls and content on later ones.
type multiTurnToolProvider struct {
	mu        sync.Mutex
	callCount int
	toolNames []string
	responses []string
}

func newMultiTurnToolProvider(toolNames []string, responses ...string) *multiTurnToolProvider {
	if len(responses) == 0 {
		responses = []string{"done"}
	}
	return &multiTurnToolProvider{toolNames: toolNames, responses: responses}
}

func (p *multiTurnToolProvider) Complete(_ context.Context, req *ProviderRequest) (*ProviderResponse, error) {
	p.mu.Lock()
	call := p.callCount
	p.callCount++
	p.mu.Unlock()

	if call < len(p.toolNames) {
		return &ProviderResponse{
			ToolCalls: []ToolCall{
				{ID: fmt.Sprintf("call_%d", call), Name: p.toolNames[call], Arguments: `{"input":"test"}`},
			},
		}, nil
	}

	idx := call - len(p.toolNames)
	if idx >= len(p.responses) {
		idx = len(p.responses) - 1
	}
	return &ProviderResponse{Content: p.responses[idx]}, nil
}

func (p *multiTurnToolProvider) Stream(ctx context.Context, req *ProviderRequest) (<-chan StreamDelta, error) {
	resp, _ := p.Complete(ctx, req)
	ch := make(chan StreamDelta, 2)
	if resp.Content != "" {
		ch <- StreamDelta{Content: resp.Content}
	}
	for _, tc := range resp.ToolCalls {
		ch <- StreamDelta{ToolCalls: []ToolCallDelta{
			{Index: 0, ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments},
		}}
	}
	ch <- StreamDelta{Done: true}
	close(ch)
	return ch, nil
}

// --- Stub tools ---

// interruptTool returns ErrInterrupt with the given reason.
func interruptTool(reason string) *Tool {
	return &Tool{
		Name:        "interrupt_tool",
		Description: "Tool that interrupts the agent",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"input": map[string]any{"type": "string"},
			},
			"required": []any{"input"},
		},
		Func: func(_ context.Context, args json.RawMessage) (any, error) {
			return "interrupted: " + reason, NewInterruptError(reason)
		},
	}
}

// hangingTool blocks forever (until context cancel).
var hangingTool = &Tool{
	Name:        "hanging_tool",
	Description: "Tool that blocks forever",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"input": map[string]any{"type": "string"},
		},
		"required": []any{"input"},
	},
	Func: func(ctx context.Context, _ json.RawMessage) (any, error) {
		<-ctx.Done()
		return "", ctx.Err()
	},
}

// slowTool simulates a long-running tool by blocking on a channel.
func slowTool(done <-chan struct{}) *Tool {
	return &Tool{
		Name:        "slow_tool",
		Description: "Tool that blocks until signalled",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"input": map[string]any{"type": "string"},
			},
			"required": []any{"input"},
		},
		Func: func(ctx context.Context, _ json.RawMessage) (any, error) {
			select {
			case <-done:
				return "done", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		},
	}
}

// errTool returns a plain error.
func errTool(msg string) *Tool {
	return &Tool{
		Name:        "err_tool",
		Description: "Tool that returns an error",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"input": map[string]any{"type": "string"},
			},
			"required": []any{"input"},
		},
		Func: func(_ context.Context, _ json.RawMessage) (any, error) {
			return "", fmt.Errorf("%s", msg)
		},
	}
}

// --- Assert helpers ---

// assertStatus fails if the agent status is not the expected value.
func assertStatus(t testing.TB, a *Agent, want Status) {
	t.Helper()
	got := a.state.Status()
	if got != want {
		t.Fatalf("agent status = %q, want %q", got, want)
	}
}

// assertInterrupted fails if the agent is not interrupted with a matching reason prefix.
func assertInterrupted(t testing.TB, a *Agent, reasonPrefix string) {
	t.Helper()
	ir := a.Interrupted()
	if ir == nil {
		t.Fatal("expected agent to be interrupted, got nil")
	}
	if !strings.Contains(ir.Reason, reasonPrefix) {
		t.Fatalf("interrupt reason = %q, want prefix %q", ir.Reason, reasonPrefix)
	}
}

// assertNodeErrorPath fails if err is not a *NodeError with the given path prefix.
func assertNodeErrorPath(t testing.TB, err error, pathPrefix string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	ne, ok := err.(*NodeError)
	if !ok {
		t.Fatalf("error type = %T, want *NodeError", err)
	}
	pathStr := strings.Join(ne.Path, " → ")
	if !strings.Contains(pathStr, pathPrefix) {
		t.Fatalf("node error path = %q, want prefix %q", pathStr, pathPrefix)
	}
}

// --- Helpers ---

func lastUserContent(msgs []Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == RoleUser || msgs[i].Role == "human" {
			return msgs[i].Content
		}
	}
	return ""
}

// stubAgentConfig returns a minimal Config with the echo provider.
func stubAgentConfig(name string, tools []*Tool) Config {
	return Config{
		ModelConfig: ModelConfig{
			Name:  name,
			Model: "stub",
			Provider: &echoProvider{},
		},
		ExecutionConfig: ExecutionConfig{
			MaxTurns: 10,
		},
		Tools: tools,
	}
}
