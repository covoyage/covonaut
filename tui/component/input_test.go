package component

import (
	"strings"
	"testing"

	"github.com/covoyage/covonaut/tui/core"
)

func TestInputInsertAndSubmit(t *testing.T) {
	var submitted string
	in := NewInput(nil)
	in.SetFocused(true)
	in.OnSubmit(func(v string) { submitted = v })

	in.Update(core.KeyMsg{Data: "hello"})
	if got := in.GetValue(); got != "hello" {
		t.Fatalf("want hello, got %q", got)
	}
	lines := in.Render(40)
	if !strings.Contains(lines[0], core.CURSOR_MARKER) {
		t.Fatalf("expected cursor marker in render")
	}
	in.Update(core.KeyMsg{Data: "\r"})
	if submitted != "hello" {
		t.Fatalf("want hello, got %q", submitted)
	}
}

func TestInputBackspaceAndWordDelete(t *testing.T) {
	in := NewInput(nil)
	in.SetFocused(true)
	in.Update(core.KeyMsg{Data: "foo bar"})
	if in.GetValue() != "foo bar" {
		t.Fatalf("unexpected value: %q", in.GetValue())
	}
	in.Update(core.KeyMsg{Data: "\x17"})
	if in.GetValue() != "foo " {
		t.Fatalf("after ctrl+w want %q, got %q", "foo ", in.GetValue())
	}
	in.Update(core.KeyMsg{Data: "\x7f"})
	if in.GetValue() != "foo" {
		t.Fatalf("after backspace want %q, got %q", "foo", in.GetValue())
	}
}

func TestInputCursorMoves(t *testing.T) {
	in := NewInput(nil)
	in.SetFocused(true)
	in.Update(core.KeyMsg{Data: "abcd"})
	in.Update(core.KeyMsg{Data: "\x01"})
	in.Update(core.KeyMsg{Data: "X"})
	if in.GetValue() != "Xabcd" {
		t.Fatalf("expected Xabcd, got %q", in.GetValue())
	}
	in.Update(core.KeyMsg{Data: "\x05"})
	in.Update(core.KeyMsg{Data: "Y"})
	if in.GetValue() != "XabcdY" {
		t.Fatalf("expected XabcdY, got %q", in.GetValue())
	}
}

func TestInputSelectAllReplacesOnlyInputText(t *testing.T) {
	in := NewInput(nil)
	in.SetFocused(true)
	in.Update(core.KeyMsg{Data: "hello"})
	in.Update(core.KeyMsg{Data: "\x1b[97;9u"}) // Kitty CSI-u: super+a

	if got := in.GetSelectedText(); got != "hello" {
		t.Fatalf("selected text: want %q, got %q", "hello", got)
	}

	in.Update(core.KeyMsg{Data: "x"})
	if got := in.GetValue(); got != "x" {
		t.Fatalf("typing should replace selected input text, got %q", got)
	}
}
