package agentcore

import (
	"context"
	"testing"
)

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
