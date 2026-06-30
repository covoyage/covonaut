package component

import (
	"strings"
	"testing"
)

func TestMarkdownHeadingsAndCode(t *testing.T) {
	md := NewMarkdown("# Title\n\nSome **bold** and `code`.\n\n```go\nfmt.Println(\"hi\")\n```")
	lines := md.Render(40)
	if len(lines) == 0 {
		t.Fatal("empty render")
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Title") {
		t.Fatalf("missing title: %s", joined)
	}
	if !strings.Contains(joined, "fmt.Println") {
		t.Fatalf("missing code body: %s", joined)
	}
}

func TestMarkdownTable(t *testing.T) {
	md := NewMarkdown("| a | b |\n| --- | --- |\n| 1 | 2 |")
	lines := md.Render(20)
	if len(lines) < 4 {
		t.Fatalf("expected >=4 rows, got %d", len(lines))
	}
}

func TestMarkdownList(t *testing.T) {
	md := NewMarkdown("- one\n- two\n  - nested")
	lines := md.Render(20)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "one") || !strings.Contains(joined, "nested") {
		t.Fatalf("missing bullets: %s", joined)
	}
}
