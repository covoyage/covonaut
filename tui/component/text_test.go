package component

import (
	"strings"
	"testing"

	"github.com/covoyage/covonaut/tui/core"
)

func TestTextWrap(t *testing.T) {
	txt := NewText("hello world foo bar")
	lines := txt.Render(6)
	if len(lines) == 0 {
		t.Fatal("expected non-empty render")
	}
	for _, l := range lines {
		if core.VisibleWidth(l) != 6 {
			t.Fatalf("line width %d != 6 (line=%q)", core.VisibleWidth(l), l)
		}
	}
}

func TestTruncatedText(t *testing.T) {
	tt := NewTruncatedText("the quick brown fox jumps over the lazy dog")
	lines := tt.Render(10)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if core.VisibleWidth(lines[0]) != 10 {
		t.Fatalf("width %d != 10", core.VisibleWidth(lines[0]))
	}
	if !strings.Contains(lines[0], "…") {
		t.Fatalf("expected ellipsis, got %q", lines[0])
	}
}

func TestSpacer(t *testing.T) {
	s := NewSpacer(3)
	lines := s.Render(5)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	for _, l := range lines {
		if core.VisibleWidth(l) != 5 {
			t.Fatalf("bad width: %q", l)
		}
	}
}

func TestBoxWithBorder(t *testing.T) {
	b := NewBox()
	b.SetBorder(BorderRounded)
	b.SetTitle("Title")
	b.AddChild(NewText("hi"))
	lines := b.Render(10)
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "Title") {
		t.Fatalf("expected title in first line: %q", lines[0])
	}
	for _, l := range lines {
		if core.VisibleWidth(l) != 10 {
			t.Fatalf("width %d != 10 (line=%q)", core.VisibleWidth(l), l)
		}
	}
}
