package component

import (
	"testing"

	"github.com/covoyage/covonaut/tui/terminal"
)

func TestEditorInputHistory(t *testing.T) {
	km := terminal.NewKeybindingsManager(terminal.DefaultKeybindings())
	e := NewEditor(km)

	// Initially no history — up arrow should not consume.
	consumed := e.historyPrev()
	if consumed {
		t.Error("historyPrev should not consume when history is empty")
	}

	// Push some history entries.
	e.PushInputHistory("first message")
	e.PushInputHistory("second message")
	e.PushInputHistory("third message")

	// Simulate typing a new draft.
	e.SetValue("current draft")

	// Up arrow — should recall "third message".
	consumed = e.historyPrev()
	if !consumed {
		t.Error("historyPrev should consume after pushing history")
	}
	if got := e.GetValue(); got != "third message" {
		t.Errorf("historyPrev: got %q, want %q", got, "third message")
	}

	// Up again — "second message".
	consumed = e.historyPrev()
	if !consumed {
		t.Error("historyPrev should consume")
	}
	if got := e.GetValue(); got != "second message" {
		t.Errorf("historyPrev: got %q, want %q", got, "second message")
	}

	// Up again — "first message".
	consumed = e.historyPrev()
	if !consumed {
		t.Error("historyPrev should consume")
	}
	if got := e.GetValue(); got != "first message" {
		t.Errorf("historyPrev: got %q, want %q", got, "first message")
	}

	// Up at oldest — stays at "first message".
	consumed = e.historyPrev()
	if !consumed {
		t.Error("historyPrev should consume even at oldest")
	}
	if got := e.GetValue(); got != "first message" {
		t.Errorf("historyPrev at oldest: got %q, want %q", got, "first message")
	}

	// Down — "second message".
	consumed = e.historyNext()
	if !consumed {
		t.Error("historyNext should consume")
	}
	if got := e.GetValue(); got != "second message" {
		t.Errorf("historyNext: got %q, want %q", got, "second message")
	}

	// Down — "third message".
	consumed = e.historyNext()
	if !consumed {
		t.Error("historyNext should consume")
	}
	if got := e.GetValue(); got != "third message" {
		t.Errorf("historyNext: got %q, want %q", got, "third message")
	}

	// Down past newest — restore draft.
	consumed = e.historyNext()
	if !consumed {
		t.Error("historyNext should consume when restoring draft")
	}
	if got := e.GetValue(); got != "current draft" {
		t.Errorf("historyNext restore draft: got %q, want %q", got, "current draft")
	}

	// After restoring draft, down should not consume.
	consumed = e.historyNext()
	if consumed {
		t.Error("historyNext should not consume after restoring draft")
	}
}

func TestEditorInputHistoryDedup(t *testing.T) {
	e := NewEditor(nil)

	e.PushInputHistory("hello")
	e.PushInputHistory("hello") // duplicate — should be ignored.
	e.PushInputHistory("world")

	e.SetValue("")
	e.historyPrev()
	if got := e.GetValue(); got != "world" {
		t.Errorf("after dedup: got %q, want %q", got, "world")
	}
	e.historyPrev()
	if got := e.GetValue(); got != "hello" {
		t.Errorf("after dedup: got %q, want %q", got, "hello")
	}
}

func TestEditorInputHistoryEmptyDraft(t *testing.T) {
	e := NewEditor(nil)
	e.PushInputHistory("only entry")

	// Empty editor, up arrow.
	e.SetValue("")
	consumed := e.historyPrev()
	if !consumed {
		t.Error("historyPrev should consume")
	}
	if got := e.GetValue(); got != "only entry" {
		t.Errorf("got %q, want %q", got, "only entry")
	}

	// Down — restore empty draft.
	consumed = e.historyNext()
	if !consumed {
		t.Error("historyNext should consume")
	}
	if got := e.GetValue(); got != "" {
		t.Errorf("restore empty draft: got %q, want empty", got)
	}
}
