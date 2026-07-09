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

func TestEditorMouseDragSelection(t *testing.T) {
	e := NewEditor(nil)
	e.SetFocused(true)
	e.Update(core.KeyMsg{Data: "hello world"})
	e.Render(40) // populate lastVisuals; default prompt "> " is 2 cols wide

	e.Update(core.MouseMsg{Action: core.MousePress, Row: 0, Col: 2})  // buffer col 0
	e.Update(core.MouseMsg{Action: core.MouseMotion, Row: 0, Col: 7}) // buffer col 5
	e.Update(core.MouseMsg{Action: core.MouseRelease, Row: 0, Col: 7})

	if got := e.GetSelectedText(); got != "hello" {
		t.Fatalf("want %q, got %q", "hello", got)
	}
}

func TestEditorMouseDragSelectionMultiline(t *testing.T) {
	e := NewEditor(nil)
	e.SetFocused(true)
	e.Update(core.KeyMsg{Data: "foo"})
	e.Update(core.KeyMsg{Data: "\x1b\r"}) // hard newline
	e.Update(core.KeyMsg{Data: "bar"})
	e.Render(40)

	// Row 0: "> foo" (prompt 2 cols); Row 1: "  bar" (continuation prompt 2 cols).
	e.Update(core.MouseMsg{Action: core.MousePress, Row: 0, Col: 3})  // after 'f' in "foo"
	e.Update(core.MouseMsg{Action: core.MouseMotion, Row: 1, Col: 4}) // after "ba" in "bar"
	e.Update(core.MouseMsg{Action: core.MouseRelease, Row: 1, Col: 4})

	if want, got := "oo\nba", e.GetSelectedText(); got != want {
		t.Fatalf("want %q, got %q", want, got)
	}
}

func TestEditorMouseClickWithoutDragNoSelection(t *testing.T) {
	e := NewEditor(nil)
	e.SetFocused(true)
	e.Update(core.KeyMsg{Data: "hello"})
	e.Render(40)

	e.Update(core.MouseMsg{Action: core.MousePress, Row: 0, Col: 4})
	e.Update(core.MouseMsg{Action: core.MouseRelease, Row: 0, Col: 4})

	if got := e.GetSelectedText(); got != "" {
		t.Fatalf("expected empty selection for a plain click, got %q", got)
	}
}

func TestEditorMouseSelectionClearsOnKeystroke(t *testing.T) {
	e := NewEditor(nil)
	e.SetFocused(true)
	e.Update(core.KeyMsg{Data: "hello"})
	e.Render(40)

	e.Update(core.MouseMsg{Action: core.MousePress, Row: 0, Col: 2})
	e.Update(core.MouseMsg{Action: core.MouseMotion, Row: 0, Col: 5})
	e.Update(core.MouseMsg{Action: core.MouseRelease, Row: 0, Col: 5})
	if e.GetSelectedText() == "" {
		t.Fatalf("expected non-empty selection before keystroke")
	}

	e.Update(core.KeyMsg{Data: "!"})
	if got := e.GetSelectedText(); got != "" {
		t.Fatalf("expected selection cleared after keystroke, got %q", got)
	}
}

func TestEditorClearSelectionResetsMouseDrag(t *testing.T) {
	e := NewEditor(nil)
	e.SetFocused(true)
	e.Update(core.KeyMsg{Data: "hello"})
	e.Render(40)

	e.Update(core.MouseMsg{Action: core.MousePress, Row: 0, Col: 2})
	e.Update(core.MouseMsg{Action: core.MouseMotion, Row: 0, Col: 5})
	e.Update(core.MouseMsg{Action: core.MouseRelease, Row: 0, Col: 5})
	e.ClearSelection()

	if got := e.GetSelectedText(); got != "" {
		t.Fatalf("expected empty selection after ClearSelection, got %q", got)
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
