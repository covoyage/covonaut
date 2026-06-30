package component

import (
	"testing"

	"github.com/covoyage/covonaut/tui/core"
)

func TestEditorInsertAndNewline(t *testing.T) {
	e := NewEditor(nil)
	e.SetFocused(true)
	e.Update(core.KeyMsg{Data: "hello"})
	e.Update(core.KeyMsg{Data: "\x1b\r"})
	e.Update(core.KeyMsg{Data: "world"})
	if got := e.GetValue(); got != "hello\nworld" {
		t.Fatalf("want %q, got %q", "hello\nworld", got)
	}
}

func TestEditorUndoRedo(t *testing.T) {
	e := NewEditor(nil)
	e.SetFocused(true)
	e.Update(core.KeyMsg{Data: "abc"})
	if e.GetValue() != "abc" {
		t.Fatalf("initial: %q", e.GetValue())
	}
	e.Update(core.KeyMsg{Data: "\x1a"})
	v := e.GetValue()
	if v == "abc" {
		t.Fatalf("expected undo to shorten: %q", v)
	}
}

func TestEditorCursorRenderMarker(t *testing.T) {
	e := NewEditor(nil)
	e.SetFocused(true)
	e.Update(core.KeyMsg{Data: "hi"})
	lines := e.Render(20)
	if len(lines) == 0 {
		t.Fatalf("expected render output")
	}
	found := false
	for _, l := range lines {
		if containsMarker(l) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("cursor marker missing from %v", lines)
	}
}

func TestEditorSelectAllIsEditorScoped(t *testing.T) {
	e := NewEditor(nil)
	e.SetFocused(true)
	e.Update(core.KeyMsg{Data: "hello"})
	e.Update(core.KeyMsg{Data: "\x1b[97;9u"}) // Kitty CSI-u: super+a

	if got := e.GetSelectedText(); got != "hello" {
		t.Fatalf("selected text: want %q, got %q", "hello", got)
	}

	e.Update(core.KeyMsg{Data: "x"})
	if got := e.GetValue(); got != "x" {
		t.Fatalf("typing should replace selected editor text, got %q", got)
	}
	if got := e.GetSelectedText(); got != "" {
		t.Fatalf("selection should clear after replacement, got %q", got)
	}
}

func TestEditorSelectAllDelete(t *testing.T) {
	e := NewEditor(nil)
	e.SetFocused(true)
	e.Update(core.KeyMsg{Data: "hello"})
	e.Update(core.KeyMsg{Data: "\x1b\r"})
	e.Update(core.KeyMsg{Data: "world"})
	e.Update(core.KeyMsg{Data: "\x1b[97;9u"}) // Kitty CSI-u: super+a
	e.Update(core.KeyMsg{Data: "\x7f"})

	if got := e.GetValue(); got != "" {
		t.Fatalf("delete should clear selected editor text, got %q", got)
	}
}

func containsMarker(s string) bool {
	for i := 0; i+len(core.CURSOR_MARKER) <= len(s); i++ {
		if s[i:i+len(core.CURSOR_MARKER)] == core.CURSOR_MARKER {
			return true
		}
	}
	return false
}
