package agentcore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// repetitionLoopThenOKProvider fails the first failCount calls with
// ErrRepetitionLoop (simulating a provider middleware like covo-agent's
// stream_health detector signaling a mid-stream degeneration loop), then
// succeeds with a final answer. Used to test runLoop's soft
// repetition-recovery ladder (steering nudge -> retry -> eventually succeed
// or give up).
type repetitionLoopThenOKProvider struct {
	failCount int
	calls     int
}

func (p *repetitionLoopThenOKProvider) Complete(ctx context.Context, req *ProviderRequest) (*ProviderResponse, error) {
	p.calls++
	if p.calls <= p.failCount {
		return nil, ErrRepetitionLoop
	}
	return &ProviderResponse{Content: "final answer"}, nil
}

func (p *repetitionLoopThenOKProvider) Stream(ctx context.Context, req *ProviderRequest) (<-chan StreamDelta, error) {
	return nil, errors.New("not implemented")
}

func TestRunLoop_RepetitionRecovery_NudgesThenSucceeds(t *testing.T) {
	provider := &repetitionLoopThenOKProvider{failCount: 1} // fails once, then succeeds (within default MaxAttempts=2)
	agent := New(Config{ModelConfig: ModelConfig{Provider: provider}})
	defer agent.Close()

	out, err := agent.Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("expected recovery to succeed, got err: %v", err)
	}
	if out != "final answer" {
		t.Fatalf("expected final answer, got %q", out)
	}

	// A corrective steering message should have been persisted into history
	// (the default mild stream-repetition prompt).
	found := false
	for _, m := range agent.State().Messages() {
		if m.Role == RoleSystem && strings.Contains(m.Content, "repeated phrases") {
			found = true
		}
	}
	if !found {
		t.Fatal("expected a repetition-recovery steering message to be persisted")
	}
}

func TestRunLoop_RepetitionRecovery_GivesUpAfterMaxAttempts(t *testing.T) {
	provider := &repetitionLoopThenOKProvider{failCount: 100} // always fails
	agent := New(Config{
		ModelConfig: ModelConfig{Provider: provider},
		ExecutionConfig: ExecutionConfig{
			RepetitionRecovery: &RepetitionRecoveryConfig{MaxAttempts: 2},
			MaxTurns:           50, // generous, so the recovery cap (not MaxTurns) is what ends the run
		},
	})
	defer agent.Close()

	_, err := agent.Run(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected an error after exceeding max repetition-recovery attempts")
	}
	if !IsRepetitionLoopError(err) {
		t.Fatalf("expected IsRepetitionLoopError(err) == true, got: %v", err)
	}
	// Should have given up quickly (MaxAttempts=2 -> at most 3 calls), well
	// before the generous MaxTurns budget would have kicked in instead.
	if provider.calls > 5 {
		t.Fatalf("expected to give up quickly (few calls), got %d calls", provider.calls)
	}
}

func TestRunLoop_CrossTurnTextRepetition_NormalizesBeforeComparing(t *testing.T) {
	// Four turns with slightly different narration ("Let me..." vs "I'll..."
	// vs "let's...") wrapping otherwise-identical text (and a tool call each
	// time, so the run keeps going) should still be recognized as the same
	// repeated content once normalized, trigger the recovery steering
	// message on the 4th, and the run should finish cleanly on the next turn.
	dummyTool := &Tool{
		Name:        "dummy_tool",
		Description: "A dummy tool",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			return "ok", nil
		},
	}
	provider := &narrationLoopProvider{narrations: []string{
		"Let me check the config file for issues",
		"I'll check the config file for issues",
		"let's check the config file for issues",
		"Let me check the config file for issues",
	}}
	agent := New(Config{
		ModelConfig: ModelConfig{Provider: provider},
		Tools:       []*Tool{dummyTool},
		ExecutionConfig: ExecutionConfig{
			MaxTurns: 20,
		},
	})
	defer agent.Close()

	out, err := agent.Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "final answer after recovery" {
		t.Fatalf("expected final answer after recovery, got %q", out)
	}

	found := false
	for _, m := range agent.State().Messages() {
		if m.Role == RoleSystem && strings.Contains(m.Content, "repeating the same response") {
			found = true
		}
	}
	if !found {
		t.Fatal("expected the normalized cross-turn text-repetition steering message to fire")
	}
}

// narrationLoopProvider emits `len(narrations)` turns, each with a slightly
// varied narration string (e.g. "Let me..." vs "I'll...") plus a dummy_tool
// call (so the run loop keeps going), then a final plain-text turn with no
// tool call to end the run.
type narrationLoopProvider struct {
	narrations []string
	calls      int
}

func (p *narrationLoopProvider) Complete(ctx context.Context, req *ProviderRequest) (*ProviderResponse, error) {
	idx := p.calls
	p.calls++
	if idx < len(p.narrations) {
		return &ProviderResponse{
			Content: p.narrations[idx],
			ToolCalls: []ToolCall{
				{ID: fmt.Sprintf("call_%d", idx), Name: "dummy_tool", Arguments: `{}`},
			},
		}, nil
	}
	return &ProviderResponse{Content: "final answer after recovery"}, nil
}

func (p *narrationLoopProvider) Stream(ctx context.Context, req *ProviderRequest) (<-chan StreamDelta, error) {
	return nil, errors.New("not implemented")
}

func TestToolCallSignature_DifferentArgumentsNotTreatedAsRepeat(t *testing.T) {
	sigA := toolCallSignature([]ToolCall{{Name: "read_file", Arguments: `{"path":"a.go"}`}})
	sigB := toolCallSignature([]ToolCall{{Name: "read_file", Arguments: `{"path":"b.go"}`}})
	if sigA == sigB {
		t.Fatalf("expected different arguments to produce different signatures, both were %q", sigA)
	}

	sigA2 := toolCallSignature([]ToolCall{{Name: "read_file", Arguments: `{"path":"a.go"}`}})
	if sigA != sigA2 {
		t.Fatalf("expected identical name+arguments to produce the same signature, got %q vs %q", sigA, sigA2)
	}
}
