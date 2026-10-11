package agentcore

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type contextCheckingCheckpointSaver struct {
	*MemoryCheckpointSaver
	appendErr error
}

func (s *contextCheckingCheckpointSaver) Append(ctx context.Context, threadID string, snapshot StateSnapshot) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if s.appendErr != nil {
		return 0, s.appendErr
	}
	return s.MemoryCheckpointSaver.Append(ctx, threadID, snapshot)
}

func TestMemoryCheckpointSaver_AppendLatest(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryCheckpointSaver()
	s1 := StateSnapshot{Status: StatusRunning, Turn: 1, Messages: []Message{{Role: RoleUser, Content: "u"}}}
	seq1, err := m.Append(ctx, "th", s1)
	if err != nil || seq1 <= 0 {
		t.Fatalf("seq1=%d err=%v", seq1, err)
	}
	s2 := StateSnapshot{Status: StatusFinished, Turn: 2, Messages: []Message{{Role: RoleUser, Content: "u"}, {Role: RoleAssistant, Content: "ok"}}}
	seq2, err := m.Append(ctx, "th", s2)
	if err != nil || seq2 <= seq1 {
		t.Fatalf("seq2=%d seq1=%d err=%v", seq2, seq1, err)
	}
	got, seq, err := m.Latest(ctx, "th")
	if err != nil || seq != seq2 {
		t.Fatalf("latest seq=%d err=%v", seq, err)
	}
	if len(got.Messages) != 2 || got.Status != StatusFinished {
		t.Fatalf("%+v", got)
	}
}

func TestTokenUsageMergeCacheDetails(t *testing.T) {
	usage := TokenUsage{PromptTokens: 20, CacheReadTokens: 10, CacheWriteTokens: 4}
	usage.Merge(TokenUsage{CompletionTokens: 5, TotalTokens: 25})
	usage.Merge(TokenUsage{PromptTokens: 20, CompletionTokens: 3, CacheReadTokens: 8, CacheWriteTokens: 4})
	want := TokenUsage{PromptTokens: 20, CompletionTokens: 5, TotalTokens: 25, CacheReadTokens: 10, CacheWriteTokens: 4}
	if usage != want {
		t.Fatalf("merged usage = %#v, want %#v", usage, want)
	}
	state := NewState()
	state.AddUsage(usage)
	state.AddUsage(usage)
	if got := state.Snapshot().TotalUsage; got.CacheReadTokens != 20 || got.CacheWriteTokens != 8 {
		t.Fatalf("accumulated usage = %#v", got)
	}
}

func TestMemoryCheckpointSaver_EvictsOldest(t *testing.T) {
	ctx := context.Background()
	m := &MemoryCheckpointSaver{
		byThread:                make(map[string][]memoryCP),
		MaxCheckpointsPerThread: 2,
	}

	for i := 1; i <= 5; i++ {
		snap := StateSnapshot{Turn: int64(i), Messages: []Message{{Role: RoleUser, Content: string(rune('a' + i - 1))}}}
		seq, err := m.Append(ctx, "th", snap)
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		_ = seq
	}

	all := m.All("th")
	if len(all) != 2 {
		t.Fatalf("expected 2 checkpoints, got %d", len(all))
	}
	if all[0].Turn != 4 || all[1].Turn != 5 {
		t.Fatalf("expected turns 4,5 got %d,%d", all[0].Turn, all[1].Turn)
	}

	latest, seq, err := m.Latest(ctx, "th")
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if latest.Turn != 5 {
		t.Fatalf("latest turn = %d, want 5", latest.Turn)
	}
	if seq < 5 {
		t.Fatalf("latest seq = %d, want >= 5", seq)
	}
}

func TestMemoryCheckpointSaver_LatestMissing(t *testing.T) {
	m := NewMemoryCheckpointSaver()
	_, _, err := m.Latest(context.Background(), "nope")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestToolBatchInterruptCheckpointPreservesCompletedResults(t *testing.T) {
	for _, mode := range []ExecutionMode{ModeSerial, ModeParallel, ModeMixed} {
		t.Run(string(mode), func(t *testing.T) {
			saver := NewMemoryCheckpointSaver()
			agent, err := NewWithError(StubConfig(seqStubProvider{}, WithCheckpoint(&CheckpointSettings{
				Saver: saver, ThreadID: "batch",
			})))
			if err != nil {
				t.Fatal(err)
			}
			defer agent.Close()
			registry := NewRegistry()
			registry.Register(interruptTool("pause"))
			registry.Register(&Tool{
				Name: "completed",
				Func: func(context.Context, json.RawMessage) (any, error) { return "saved output", nil },
			})
			agent.executor = NewExecutor(registry, ExecutorConfig{Mode: mode})
			calls := []ToolCall{
				{ID: "pause-call", Name: "interrupt_tool", Arguments: "{}"},
				{ID: "done-call", Name: "completed", Arguments: "{}"},
			}
			if err := agent.executeToolCalls(context.Background(), calls); !IsInterrupt(err) {
				t.Fatalf("executeToolCalls error = %v, want interrupt", err)
			}
			snapshot, _, err := saver.Latest(context.Background(), "batch")
			if err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Messages) != 2 {
				t.Fatalf("checkpoint has %d messages, want both completed results", len(snapshot.Messages))
			}
			if snapshot.Messages[1].ToolCallID != "done-call" || snapshot.Messages[1].Content != "saved output" {
				t.Fatalf("completed result = %#v", snapshot.Messages[1])
			}
			if snapshot.InterruptReason == nil || snapshot.InterruptReason.ToolCallID != "pause-call" {
				t.Fatalf("interrupt reason = %#v", snapshot.InterruptReason)
			}
			if err := agent.RestoreLatestCheckpoint(context.Background(), "batch"); err != nil {
				t.Fatal(err)
			}
			if len(agent.State().Messages()) != 2 || agent.Interrupted() == nil {
				t.Fatal("restore lost completed results or interrupt reason")
			}
		})
	}
}

func TestCanceledToolRunSavesCheckpoint(t *testing.T) {
	saveErr := errors.New("checkpoint unavailable")
	for _, test := range []struct {
		name      string
		skipSave  bool
		appendErr error
	}{
		{name: "save"},
		{name: "skip", skipSave: true},
		{name: "save_failure", appendErr: saveErr},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			saver := &contextCheckingCheckpointSaver{MemoryCheckpointSaver: NewMemoryCheckpointSaver(), appendErr: test.appendErr}
			agent, err := NewWithError(StubConfig(newInterruptProvider("cancel_tool"), WithCheckpoint(&CheckpointSettings{
				Saver: saver, ThreadID: "canceled", SkipSaveOnTurnEnd: test.skipSave,
			})))
			if err != nil {
				t.Fatal(err)
			}
			defer agent.Close()
			agent.registry.Register(&Tool{
				Name: "cancel_tool",
				Func: func(context.Context, json.RawMessage) (any, error) {
					cancel()
					return "completed before cancellation", nil
				},
			})
			_, err = agent.Run(ctx, "start")
			if test.appendErr != nil {
				if !errors.Is(err, test.appendErr) || agent.State().Status() != StatusError {
					t.Fatalf("failed save: error=%v status=%s", err, agent.State().Status())
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if test.skipSave {
				if len(saver.All("canceled")) != 0 {
					t.Fatal("SkipSaveOnTurnEnd was ignored")
				}
				return
			}
			if err := agent.RestoreLatestCheckpoint(context.Background(), "canceled"); err != nil {
				t.Fatal(err)
			}
			messages := agent.State().Messages()
			if agent.State().Status() != StatusFinished || len(messages) != 3 {
				t.Fatalf("restored status=%s messages=%#v", agent.State().Status(), messages)
			}
			if messages[2].Content != "completed before cancellation" || messages[2].ToolCallID != "call_1" {
				t.Fatalf("restored tool result = %#v", messages[2])
			}
		})
	}
}

func TestAgent_RunCheckpointRestore(t *testing.T) {
	ctx := context.Background()
	saver := NewMemoryCheckpointSaver()
	cfg := Config{
		ModelConfig: ModelConfig{
			Name:      "cp",
			Model:     "stub",
			Provider:  seqStubProvider{},
			Streaming: false,
		},
		ExecutionConfig: ExecutionConfig{
			MaxTurns: 5,
		},
		SystemPrompt: "",
		Checkpoint: &CheckpointSettings{
			Saver:    saver,
			ThreadID: "thread-a",
		},
	}
	a1 := New(cfg)
	out, err := a1.Run(ctx, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if out == "" {
		t.Fatal("empty out")
	}
	if len(saver.All("thread-a")) == 0 {
		t.Fatal("expected at least one checkpoint")
	}

	cfg2 := cfg
	cfg2.Name = "cp2"
	a2 := New(cfg2)
	if err := a2.RestoreLatestCheckpoint(ctx, ""); err != nil {
		t.Fatal(err)
	}
	m1 := a1.State().Messages()
	m2 := a2.State().Messages()
	if len(m1) != len(m2) {
		t.Fatalf("len %d vs %d", len(m1), len(m2))
	}
	for i := range m1 {
		if m1[i].Role != m2[i].Role || m1[i].Content != m2[i].Content {
			t.Fatalf("diff at %d: %#v vs %#v", i, m1[i], m2[i])
		}
	}
}

func TestRestoreLatestCheckpointMigratesLegacySnapshot(t *testing.T) {
	ctx := context.Background()
	saver := NewMemoryCheckpointSaver()
	_, err := saver.Append(ctx, "legacy", StateSnapshot{
		Version:  0,
		Status:   StatusInterrupted,
		Messages: []Message{{Role: RoleUser, Content: "before"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	agent := New(StubConfig(seqStubProvider{}, WithCheckpoint(&CheckpointSettings{
		Saver:    saver,
		ThreadID: "legacy",
		Migrators: map[int]StateSnapshotMigrator{
			0: func(_ context.Context, snapshot StateSnapshot) (StateSnapshot, error) {
				snapshot.Messages = append(snapshot.Messages, Message{Role: RoleSystem, Content: "migrated"})
				return snapshot, nil
			},
		},
	})))
	if err := agent.RestoreLatestCheckpoint(ctx, ""); err != nil {
		t.Fatal(err)
	}
	got := agent.State().Snapshot()
	if got.Version != CurrentStateSnapshotVersion || len(got.Messages) != 2 || got.Messages[1].Content != "migrated" {
		t.Fatalf("migrated snapshot = %#v", got)
	}
}

func TestMigrateStateSnapshotRejectsVersionJump(t *testing.T) {
	_, err := MigrateStateSnapshot(context.Background(), StateSnapshot{}, map[int]StateSnapshotMigrator{
		0: func(_ context.Context, snapshot StateSnapshot) (StateSnapshot, error) {
			snapshot.Version = CurrentStateSnapshotVersion + 1
			return snapshot, nil
		},
	})
	if err == nil {
		t.Fatal("expected version jump error")
	}
}
