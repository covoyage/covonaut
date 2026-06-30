package session

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
)

func TestAgentStore_SaveLoadRoundTrip(t *testing.T) {
	ctx := context.Background()
	fs, err := NewFileStore(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	store := NewAgentStore(fs, "/project")

	snap1 := agentcore.StateSnapshot{
		Status: agentcore.StatusFinished,
		Turn:   1,
		Messages: []agentcore.Message{
			{Role: agentcore.RoleUser, Content: "hello"},
			{Role: agentcore.RoleAssistant, Content: "hi"},
		},
		TotalUsage: agentcore.TokenUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}
	if err := store.Save(ctx, "thread-1", snap1); err != nil {
		t.Fatal(err)
	}

	got1, err := store.Load(ctx, "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	assertSnapshotEqual(t, got1, snap1)

	snap2 := agentcore.StateSnapshot{
		Status: agentcore.StatusFinished,
		Turn:   2,
		Messages: []agentcore.Message{
			{Role: agentcore.RoleUser, Content: "hello"},
			{Role: agentcore.RoleAssistant, Content: "hi"},
			{Role: agentcore.RoleUser, Content: "again"},
			{Role: agentcore.RoleAssistant, Content: "welcome back"},
		},
		TotalUsage: agentcore.TokenUsage{PromptTokens: 20, CompletionTokens: 9, TotalTokens: 29},
	}
	if err := store.Save(ctx, "thread-1", snap2); err != nil {
		t.Fatal(err)
	}

	got2, err := store.Load(ctx, "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	assertSnapshotEqual(t, got2, snap2)

	mgr, err := fs.Open(ctx, "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	msgs := mgr.MessagesOnPath()
	if len(msgs) != 4 {
		t.Fatalf("message count = %d", len(msgs))
	}
}

func TestAgentStore_SaveRewritesDivergedSession(t *testing.T) {
	ctx := context.Background()
	fs, err := NewFileStore(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	store := NewAgentStore(fs, "/project")

	first := agentcore.StateSnapshot{
		Status: agentcore.StatusFinished,
		Turn:   1,
		Messages: []agentcore.Message{
			{Role: agentcore.RoleUser, Content: "first"},
			{Role: agentcore.RoleAssistant, Content: "one"},
		},
	}
	second := agentcore.StateSnapshot{
		Status: agentcore.StatusFinished,
		Turn:   1,
		Messages: []agentcore.Message{
			{Role: agentcore.RoleUser, Content: "different"},
			{Role: agentcore.RoleAssistant, Content: "two"},
		},
	}

	if err := store.Save(ctx, "thread-1", first); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, "thread-1", second); err != nil {
		t.Fatal(err)
	}

	got, err := store.Load(ctx, "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	assertSnapshotEqual(t, got, second)
}

func TestAgentStore_ListDelete(t *testing.T) {
	ctx := context.Background()
	fs, err := NewFileStore(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	store := NewAgentStore(fs, "/project")

	if err := store.Save(ctx, "thread-a", agentcore.StateSnapshot{}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, "thread-b", agentcore.StateSnapshot{}); err != nil {
		t.Fatal(err)
	}

	keys, err := store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Fatalf("keys len = %d", len(keys))
	}

	if err := store.Delete(ctx, "thread-a"); err != nil {
		t.Fatal(err)
	}
	keys, err = store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0] != "thread-b" {
		t.Fatalf("keys = %#v", keys)
	}
}

func TestAgentStore_CreateThread(t *testing.T) {
	ctx := context.Background()
	fs, err := NewFileStore(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	store := NewAgentStore(fs, "/project")

	thread, err := store.CreateThread(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if thread.Info.ID == "" {
		t.Fatal("expected thread id")
	}
	if thread.Status != agentcore.StatusIdle {
		t.Fatalf("status = %q", thread.Status)
	}

	snap, err := store.Load(ctx, thread.Info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Status != agentcore.StatusIdle {
		t.Fatalf("loaded status = %q", snap.Status)
	}
	if len(snap.Messages) != 0 {
		t.Fatalf("messages len = %d", len(snap.Messages))
	}
}

func TestAgentStore_BranchThreadFromEntry(t *testing.T) {
	ctx := context.Background()
	fs, err := NewFileStore(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	store := NewAgentStore(fs, "/project")

	original := agentcore.StateSnapshot{
		Status: agentcore.StatusFinished,
		Turn:   2,
		Messages: []agentcore.Message{
			{Role: agentcore.RoleUser, Content: "hello"},
			{Role: agentcore.RoleAssistant, Content: "hi"},
			{Role: agentcore.RoleUser, Content: "again"},
			{Role: agentcore.RoleAssistant, Content: "welcome back"},
		},
	}
	if err := store.Save(ctx, "thread-a", original); err != nil {
		t.Fatal(err)
	}

	thread, err := store.GetThread(ctx, "thread-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(thread.Transcript) != 4 {
		t.Fatalf("transcript len = %d", len(thread.Transcript))
	}

	branch, err := store.BranchThread(ctx, "thread-a", thread.Transcript[1].EntryID)
	if err != nil {
		t.Fatal(err)
	}
	if branch.Info.ID == "thread-a" {
		t.Fatal("expected new branch id")
	}
	if branch.Info.ParentSession != "thread-a" {
		t.Fatalf("parent_session = %q", branch.Info.ParentSession)
	}
	if len(branch.Messages) != 2 {
		t.Fatalf("messages len = %d", len(branch.Messages))
	}
	if branch.Messages[1].Content != "hi" {
		t.Fatalf("last message = %#v", branch.Messages[1])
	}
}

func TestAgentStore_ThreadThinkingPersistsAcrossRewrite(t *testing.T) {
	ctx := context.Background()
	fs, err := NewFileStore(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	store := NewAgentStore(fs, "/project")

	if _, err := store.SetThreadThinking(ctx, "thread-a", &agentcore.ThinkingConfig{
		Display: agentcore.ThinkingDisplaySummarized,
		Effort:  agentcore.ThinkingEffortMedium,
		Budget:  1024,
	}); err != nil {
		t.Fatal(err)
	}
	if cfg, ok, err := store.GetThreadThinking(ctx, "thread-a"); err != nil {
		t.Fatal(err)
	} else if !ok || cfg == nil {
		t.Fatalf("thinking after set = %#v ok=%v", cfg, ok)
	}

	if err := store.Save(ctx, "thread-a", agentcore.StateSnapshot{
		Status: agentcore.StatusFinished,
		Turn:   1,
		Messages: []agentcore.Message{
			{Role: agentcore.RoleUser, Content: "hello"},
			{Role: agentcore.RoleAssistant, Content: "hi"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	if err := store.Save(ctx, "thread-a", agentcore.StateSnapshot{
		Status: agentcore.StatusFinished,
		Turn:   1,
		Messages: []agentcore.Message{
			{Role: agentcore.RoleUser, Content: "different"},
			{Role: agentcore.RoleAssistant, Content: "two"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	thread, err := store.GetThread(ctx, "thread-a")
	if err != nil {
		t.Fatal(err)
	}
	if thread.Thinking == nil {
		t.Fatal("expected thread thinking")
	}
	if thread.Thinking.Display != agentcore.ThinkingDisplaySummarized {
		t.Fatalf("display = %q", thread.Thinking.Display)
	}
	if thread.Thinking.Effort != agentcore.ThinkingEffortMedium {
		t.Fatalf("effort = %q", thread.Thinking.Effort)
	}
	if thread.Thinking.Budget != 1024 {
		t.Fatalf("budget = %d", thread.Thinking.Budget)
	}
}

func TestAgentStore_ThreadConfigPersistsAcrossRewrite(t *testing.T) {
	ctx := context.Background()
	fs, err := NewFileStore(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	store := NewAgentStore(fs, "/project")

	if _, err := store.SetThreadConfig(ctx, "thread-b", &agentcore.CallConfig{
		Model:          "thread-model",
		Skills:         []string{"planner", "writer"},
		ResponseFormat: agentcore.NewJSONObjectResponseFormat(),
		Thinking: &agentcore.ThinkingConfig{
			Display: agentcore.ThinkingDisplaySummarized,
			Budget:  1024,
		},
	}); err != nil {
		t.Fatal(err)
	}

	if cfg, ok, err := store.GetThreadConfig(ctx, "thread-b"); err != nil {
		t.Fatal(err)
	} else if !ok || cfg == nil || cfg.Model != "thread-model" {
		t.Fatalf("config after set = %#v ok=%v", cfg, ok)
	}

	if err := store.Save(ctx, "thread-b", agentcore.StateSnapshot{
		Status: agentcore.StatusFinished,
		Turn:   1,
		Messages: []agentcore.Message{
			{Role: agentcore.RoleUser, Content: "hello"},
			{Role: agentcore.RoleAssistant, Content: "hi"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	if err := store.Save(ctx, "thread-b", agentcore.StateSnapshot{
		Status: agentcore.StatusFinished,
		Turn:   1,
		Messages: []agentcore.Message{
			{Role: agentcore.RoleUser, Content: "different"},
			{Role: agentcore.RoleAssistant, Content: "two"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	thread, err := store.GetThread(ctx, "thread-b")
	if err != nil {
		t.Fatal(err)
	}
	if thread.Config == nil {
		t.Fatal("expected thread config")
	}
	if thread.Config.Model != "thread-model" {
		t.Fatalf("model = %q", thread.Config.Model)
	}
	if thread.Config.ResponseFormat == nil || thread.Config.ResponseFormat.Type != agentcore.ResponseFormatJSONObject {
		t.Fatalf("response format = %#v", thread.Config.ResponseFormat)
	}
	if len(thread.Config.Skills) != 2 || thread.Config.Skills[0] != "planner" || thread.Config.Skills[1] != "writer" {
		t.Fatalf("skills = %#v", thread.Config.Skills)
	}
	if thread.Thinking == nil || thread.Thinking.Display != agentcore.ThinkingDisplaySummarized {
		t.Fatalf("thinking = %#v", thread.Thinking)
	}
}

func assertSnapshotEqual(t *testing.T, got, want agentcore.StateSnapshot) {
	t.Helper()
	if got.Status != want.Status {
		t.Fatalf("status = %q want %q", got.Status, want.Status)
	}
	if got.Turn != want.Turn {
		t.Fatalf("turn = %d want %d", got.Turn, want.Turn)
	}
	if got.TotalUsage != want.TotalUsage {
		t.Fatalf("usage = %#v want %#v", got.TotalUsage, want.TotalUsage)
	}
	if len(got.Messages) != len(want.Messages) {
		t.Fatalf("messages len = %d want %d", len(got.Messages), len(want.Messages))
	}
	for i := range want.Messages {
		if got.Messages[i].Role != want.Messages[i].Role || got.Messages[i].Content != want.Messages[i].Content {
			t.Fatalf("message %d = %#v want %#v", i, got.Messages[i], want.Messages[i])
		}
	}
}
