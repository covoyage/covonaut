package agentcore

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

// ──────────────────────────────────────────────
// Cancel chain tests
// ──────────────────────────────────────────────

func TestCancelDuringToolExecution(t *testing.T) {
	done := make(chan struct{})
	agent := New(Config{
		ModelConfig: ModelConfig{
			Name:     "cancel_tool",
			Model:    "stub",
			Provider: newMultiTurnToolProvider([]string{"slow_tool"}),
		},
		ExecutionConfig: ExecutionConfig{
			MaxTurns: 10,
		},
		Tools: []*Tool{slowTool(done)},
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	out, err := agent.Run(ctx, "run slow tool")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "" {
		t.Fatalf("expected empty output on cancel, got %q", out)
	}
	assertStatus(t, agent, StatusFinished)
}

func TestCancelDuringHandoff(t *testing.T) {
	childDone := make(chan struct{})

	parent := New(Config{
		ModelConfig: ModelConfig{
			Name:     "parent",
			Model:    "stub",
			Provider: newMultiTurnToolProvider([]string{"transfer_to_child"}),
		},
		ExecutionConfig: ExecutionConfig{
			MaxTurns: 10,
		},
		Handoffs: []HandoffConfig{
			{
				Name:        "child",
				Description: "child agent",
				Mode:        HandoffTransfer,
				AgentConfig: Config{
					ModelConfig: ModelConfig{
						Name:     "child",
						Model:    "stub",
						Provider: newMultiTurnToolProvider([]string{"slow_tool"}),
					},
					ExecutionConfig: ExecutionConfig{
						MaxTurns: 10,
					},
					Tools: []*Tool{slowTool(childDone)},
				},
			},
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	out, err := parent.Run(ctx, "transfer to child")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "" {
		t.Fatalf("expected empty output on cancel, got %q", out)
	}
	assertStatus(t, parent, StatusFinished)
}

func TestCancelThenRerun_SameAgent(t *testing.T) {
	done := make(chan struct{})
	agent := New(Config{
		ModelConfig: ModelConfig{
			Name:     "rerun_test",
			Model:    "stub",
			Provider: newMultiTurnToolProvider([]string{"slow_tool"}, "done"),
		},
		ExecutionConfig: ExecutionConfig{
			MaxTurns: 10,
		},
		Tools: []*Tool{slowTool(done)},
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	out, err := agent.Run(ctx, "first run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "" {
		t.Fatalf("expected empty, got %q", out)
	}
	assertStatus(t, agent, StatusFinished)

	// Second run with new input — close done so the slow tool completes immediately.
	close(done)
	out, err = agent.Run(context.Background(), "second run")
	if err != nil {
		t.Fatalf("second run error: %v", err)
	}
	if out != "done" {
		t.Fatalf("second run output = %q, want %q", out, "echo:second run")
	}
}

// ──────────────────────────────────────────────
// Multi-agent recursion tests
// ──────────────────────────────────────────────

func TestTwoLevelDelegate(t *testing.T) {
	childCfg := Config{
		ModelConfig: ModelConfig{
			Name:     "child",
			Model:    "stub",
			Provider: &echoProvider{},
		},
	}
	parentCfg := Config{
		ModelConfig: ModelConfig{
			Name:     "parent",
			Model:    "stub",
			Provider: newMultiTurnToolProvider([]string{"child"}, "done"),
		},
		ExecutionConfig: ExecutionConfig{
			MaxTurns: 10,
		},
		Tools: []*Tool{AgentAsTool(childCfg)},
	}

	agent := New(parentCfg)
	out, err := agent.Run(context.Background(), "delegate to child")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "done" {
		t.Fatalf("output = %q, want %q", out, "done")
	}
	assertStatus(t, agent, StatusFinished)
}

func TestDelegateErrorPropagation(t *testing.T) {
	// Child exhausted by repeated tool calls → exceeds max turns.
	// The error is propagated inside the parent's conversation as a tool result.
	childCfg := Config{
		ModelConfig: ModelConfig{
			Name:     "child",
			Model:    "stub",
			Provider: newMultiTurnToolProvider([]string{"err_tool", "err_tool", "err_tool", "err_tool", "err_tool"}),
		},
		ExecutionConfig: ExecutionConfig{
			MaxTurns: 3,
		},
		Tools: []*Tool{errTool("boom")},
	}
	parentCfg := Config{
		ModelConfig: ModelConfig{
			Name:     "parent",
			Model:    "stub",
			Provider: newMultiTurnToolProvider([]string{"child"}),
		},
		ExecutionConfig: ExecutionConfig{
			MaxTurns: 10,
		},
		Tools: []*Tool{AgentAsTool(childCfg)},
	}

	agent := New(parentCfg)
	_, err := agent.Run(context.Background(), "delegate")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the child agent's error was persisted in the parent's conversation.
	msgs := agent.state.Messages()
	found := false
	for _, m := range msgs {
		if m.Role == RoleTool && strings.Contains(m.Content, "agent_tool:child") {
			found = true
			break
		}
	}
	if !found {
		for i, m := range msgs {
			preview := m.Content
			if len(preview) > 100 {
				preview = preview[:100]
			}
			t.Logf("msg[%d] role=%q content=%q", i, m.Role, preview)
		}
		t.Fatal("expected child error in parent conversation (RoleTool + agent_tool:child)")
	}
}

func TestHandoffTransferThenFinish(t *testing.T) {
	childCfg := Config{
		ModelConfig: ModelConfig{
			Name:     "child",
			Model:    "stub",
			Provider: &echoProvider{},
		},
	}
	parent := New(Config{
		ModelConfig: ModelConfig{
			Name:     "parent",
			Model:    "stub",
			Provider: newMultiTurnToolProvider([]string{"transfer_to_child"}),
		},
		ExecutionConfig: ExecutionConfig{
			MaxTurns: 10,
		},
		Handoffs: []HandoffConfig{
			{
				Name:        "child",
				Description: "child agent",
				Mode:        HandoffTransfer,
				AgentConfig: childCfg,
			},
		},
	})

	out, err := parent.Run(context.Background(), "handoff to child")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "echo:handoff to child" {
		t.Fatalf("output = %q, want %q", out, "echo:handoff to child")
	}
	assertStatus(t, parent, StatusFinished)
}

// ──────────────────────────────────────────────
// Checkpoint enhanced tests
// ──────────────────────────────────────────────

func TestCheckpointContent(t *testing.T) {
	saver := NewMemoryCheckpointSaver()
	agent := New(Config{
		ModelConfig: ModelConfig{
			Name:     "cp_test",
			Model:    "stub",
			Provider: &echoProvider{},
		},
		ExecutionConfig: ExecutionConfig{
			MaxTurns: 10,
		},
		Checkpoint: &CheckpointSettings{
			Saver:    saver,
			ThreadID: "test-thread",
		},
	})

	out, err := agent.Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "echo:hello" {
		t.Fatalf("output = %q, want %q", out, "echo:hello")
	}

	// Check snapshot contents.
	snaps := saver.All("test-thread")
	if len(snaps) == 0 {
		t.Fatal("expected at least 1 checkpoint")
	}

	last := snaps[len(snaps)-1]
	if last.Status != StatusFinished {
		t.Fatalf("snapshot status = %q, want %q", last.Status, StatusFinished)
	}
	if len(last.Messages) < 2 {
		t.Fatalf("snapshot messages = %d, want >= 2", len(last.Messages))
	}

	// Verify user message preserved.
	hasUser := false
	for _, m := range last.Messages {
		if m.Role == RoleUser && strings.Contains(m.Content, "hello") {
			hasUser = true
			break
		}
	}
	if !hasUser {
		t.Fatal("checkpoint missing user message")
	}

	// Verify assistant response preserved.
	hasAssistant := false
	for _, m := range last.Messages {
		if m.Role == RoleAssistant && strings.Contains(m.Content, "echo:hello") {
			hasAssistant = true
			break
		}
	}
	if !hasAssistant {
		t.Fatal("checkpoint missing assistant response")
	}
}

func TestCheckpointConcurrentThreads(t *testing.T) {
	saver := NewMemoryCheckpointSaver()
	var wg sync.WaitGroup

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			agent := New(Config{
				ModelConfig: ModelConfig{
					Name:     "cp_concurrent",
					Model:    "stub",
					Provider: &echoProvider{},
				},
				ExecutionConfig: ExecutionConfig{
					MaxTurns: 10,
				},
				Checkpoint: &CheckpointSettings{
					Saver:    saver,
					ThreadID: "cp_concurrent",
				},
			})
			input := "hello"
			out, err := agent.Run(context.Background(), input)
			if err != nil {
				t.Errorf("agent %d error: %v", id, err)
				return
			}
			if out != "echo:"+input {
				t.Errorf("agent %d output = %q, want %q", id, out, "echo:"+input)
			}
		}(i)
	}
	wg.Wait()

	// Verify each thread got its own checkpoint.
	snaps := saver.All("cp_concurrent")
	if len(snaps) < 5 {
		t.Fatalf("expected >= 5 checkpoints, got %d", len(snaps))
	}
}

func TestCheckpointRestoreContinue(t *testing.T) {
	saver := NewMemoryCheckpointSaver()
	agent := New(Config{
		ModelConfig: ModelConfig{
			Name:     "cp_restore",
			Model:    "stub",
			Provider: &echoProvider{},
		},
		ExecutionConfig: ExecutionConfig{
			MaxTurns: 10,
		},
		Checkpoint: &CheckpointSettings{
			Saver:    saver,
			ThreadID: "restore-thread",
		},
	})

	_, err := agent.Run(context.Background(), "first turn")
	if err != nil {
		t.Fatalf("first run error: %v", err)
	}

	// Restore into new agent and continue.
	agent2 := New(Config{
		ModelConfig: ModelConfig{
			Name:     "cp_restore_2",
			Model:    "stub",
			Provider: &echoProvider{},
		},
		ExecutionConfig: ExecutionConfig{
			MaxTurns: 10,
		},
		Checkpoint: &CheckpointSettings{
			Saver:    saver,
			ThreadID: "restore-thread",
		},
	})
	if err := agent2.RestoreLatestCheckpoint(context.Background(), ""); err != nil {
		t.Fatalf("restore error: %v", err)
	}

	// Run again with new input (Run persists a new user message).
	out, err := agent2.Run(context.Background(), "second turn")
	if err != nil {
		t.Fatalf("continue error: %v", err)
	}
	if out != "echo:second turn" {
		t.Fatalf("output = %q, want %q", out, "echo:second turn")
	}
}

// ──────────────────────────────────────────────
// Interrupt / Resume tests
// ──────────────────────────────────────────────

func TestToolInterruptAndResume(t *testing.T) {
	agent := New(Config{
		ModelConfig: ModelConfig{
			Name:     "interrupt_test",
			Model:    "stub",
			Provider: newInterruptProvider("interrupt_tool", "resumed"),
		},
		ExecutionConfig: ExecutionConfig{
			MaxTurns: 10,
		},
		Tools: []*Tool{interruptTool("user pause")},
	})

	out, err := agent.Run(context.Background(), "test interrupt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "" {
		t.Fatalf("expected empty output on interrupt, got %q", out)
	}
	assertInterrupted(t, agent, "user pause")

	// Resume the interrupt.
	out, err = agent.Resume(context.Background())
	if err != nil {
		t.Fatalf("resume error: %v", err)
	}
	if out != "resumed" {
		t.Fatalf("resume output = %q, want %q", out, "resumed")
	}
	assertStatus(t, agent, StatusFinished)
}

func TestInterruptPersistsAndRestoresViaCheckpoint(t *testing.T) {
	saver := NewMemoryCheckpointSaver()
	agent := New(Config{
		ModelConfig: ModelConfig{
			Name:     "cp_interrupt",
			Model:    "stub",
			Provider: newInterruptProvider("interrupt_tool", "resumed"),
		},
		ExecutionConfig: ExecutionConfig{
			MaxTurns: 10,
		},
		Checkpoint: &CheckpointSettings{
			Saver:    saver,
			ThreadID: "interrupt-thread",
		},
		Tools: []*Tool{interruptTool("checkpoint pause")},
	})

	out, err := agent.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "" {
		t.Fatalf("expected empty on interrupt, got %q", out)
	}
	assertInterrupted(t, agent, "checkpoint pause")

	// Verify checkpoint contains the interrupt reason.
	snaps := saver.All("interrupt-thread")
	last := snaps[len(snaps)-1]
	if last.InterruptReason == nil {
		t.Fatal("expected InterruptReason in snapshot, got nil")
	}
	if !strings.Contains(last.InterruptReason.Reason, "checkpoint pause") {
		t.Fatalf("InterruptReason = %q, want 'checkpoint pause'", last.InterruptReason.Reason)
	}

	// Restore into a new agent and resume.
	agent2 := New(Config{
		ModelConfig: ModelConfig{
			Name:     "cp_interrupt_2",
			Model:    "stub",
			Provider: &constantProvider{content: "resumed"},
		},
		ExecutionConfig: ExecutionConfig{
			MaxTurns: 10,
		},
		Checkpoint: &CheckpointSettings{
			Saver:    saver,
			ThreadID: "interrupt-thread",
		},
	})
	if err := agent2.RestoreLatestCheckpoint(context.Background(), ""); err != nil {
		t.Fatalf("restore error: %v", err)
	}
	assertInterrupted(t, agent2, "checkpoint pause")

	out, err = agent2.Resume(context.Background())
	if err != nil {
		t.Fatalf("resume error: %v", err)
	}
	if out != "resumed" {
		t.Fatalf("resume output = %q, want %q", out, "resumed")
	}
	assertStatus(t, agent2, StatusFinished)
}

func TestResumeOnNonInterruptedAgent_ReturnsError(t *testing.T) {
	agent := New(stubAgentConfig("no_interrupt", nil))
	_, err := agent.Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertStatus(t, agent, StatusFinished)

	_, err = agent.Resume(context.Background())
	if err == nil {
		t.Fatal("expected error on Resume without interrupt, got nil")
	}
}

func TestInterruptReason_NoPrefix(t *testing.T) {
	agent := New(Config{
		ModelConfig: ModelConfig{
			Name:     "reason_test",
			Model:    "stub",
			Provider: newInterruptProvider("interrupt_tool"),
		},
		ExecutionConfig: ExecutionConfig{
			MaxTurns: 10,
		},
		Tools: []*Tool{interruptTool("pause for user")},
	})

	_, err := agent.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ir := agent.Interrupted()
	if ir == nil {
		t.Fatal("expected interrupt")
	}
	// Reason should not contain "interrupt:" prefix.
	if strings.HasPrefix(ir.Reason, "interrupt:") {
		t.Fatalf("reason = %q, should not contain 'interrupt:' prefix", ir.Reason)
	}
	if !strings.Contains(ir.Reason, "pause for user") {
		t.Fatalf("reason = %q, want 'pause for user'", ir.Reason)
	}
}

func TestResumeEmitsAgentStartEvent(t *testing.T) {
	agent := New(Config{
		ModelConfig: ModelConfig{
			Name:     "event_resume",
			Model:    "stub",
			Provider: newInterruptProvider("interrupt_tool", "resumed"),
		},
		ExecutionConfig: ExecutionConfig{
			MaxTurns: 10,
		},
		Tools: []*Tool{interruptTool("pause")},
	})

	seenStart := false
	agent.On(EventAgentStart, func(e Event) {
		if _, ok := e.(*AgentStartEvent); ok {
			seenStart = true
		}
	})

	_, err := agent.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	assertInterrupted(t, agent, "pause")

	// Reset for resume.
	seenStart = false
	_, err = agent.Resume(context.Background())
	if err != nil {
		t.Fatalf("resume error: %v", err)
	}

	if !seenStart {
		t.Fatal("expected AgentStartEvent during resume")
	}
}

func TestInterruptData(t *testing.T) {
	dataTool := &Tool{
		Name:        "data_tool",
		Description: "Tool with interrupt data",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"input": map[string]any{"type": "string"},
			},
			"required": []any{"input"},
		},
		Func: func(_ context.Context, _ json.RawMessage) (any, error) {
			return "paused", NewInterruptErrorWithData("data pause", map[string]any{
				"user_id": 42,
				"reason":  "approval needed",
			})
		},
	}
	agent := New(Config{
		ModelConfig: ModelConfig{
			Name:     "data_test",
			Model:    "stub",
			Provider: newInterruptProvider("data_tool"),
		},
		ExecutionConfig: ExecutionConfig{
			MaxTurns: 10,
		},
		Tools: []*Tool{dataTool},
	})

	_, err := agent.Run(context.Background(), "test data")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ir := agent.Interrupted()
	if ir == nil {
		t.Fatal("expected interrupt")
	}
	if ir.Data == nil {
		t.Fatal("expected interrupt data, got nil")
	}
	uid, ok := ir.Data["user_id"]
	if !ok || uid != 42 {
		t.Fatalf("user_id = %v (%T), want 42", uid, uid)
	}
	reason, ok := ir.Data["reason"]
	if !ok || reason != "approval needed" {
		t.Fatalf("reason = %v, want 'approval needed'", reason)
	}
}

func TestInterruptThenCancelDuringResume(t *testing.T) {
	done := make(chan struct{})
	tool := slowTool(done)
	agent := New(Config{
		ModelConfig: ModelConfig{
			Name:     "cancel_resume",
			Model:    "stub",
			Provider: newInterruptProvider("interrupt_tool"),
		},
		ExecutionConfig: ExecutionConfig{
			MaxTurns: 10,
		},
		Tools: []*Tool{interruptTool("pause")},
	})

	// First interrupt tool.
	_, err := agent.Run(context.Background(), "interrupt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertInterrupted(t, agent, "pause")

	// Now change the provider so resume triggers a slow tool, then cancel.
	agent.config.Provider = newMultiTurnToolProvider([]string{"slow_tool"}, "done")
	agent.config.Tools = []*Tool{tool}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err = agent.Resume(ctx)
	if err != nil {
		t.Fatalf("unexpected error during resume cancel: %v", err)
	}
	assertStatus(t, agent, StatusFinished)
}
