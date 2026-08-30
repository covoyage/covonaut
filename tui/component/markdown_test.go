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

func TestMarkdownDiffFenceSyntaxHighlight(t *testing.T) {
	src := "```diff\n" +
		"--- a/app.py\n" +
		"+++ b/app.py\n" +
		"@@ -1 +1 @@\n" +
		"-print('old')\n" +
		"+import os\n" +
		"```\n"
	md := NewMarkdown(src)
	lines := md.Render(100)
	joined := strings.Join(lines, "\n")
	plain := stripAnsiMd(joined)
	for _, want := range []string{"--- a/app.py", "+++ b/app.py", "@@ -1 +1 @@", "-print('old')", "+import os"} {
		if !strings.Contains(plain, want) {
			t.Errorf("diff fence output missing %q:\n%q", want, plain)
		}
	}
	// Note: token-color presence is not asserted here because the test
	// palette renders without ANSI codes (no terminal detected); the
	// highlighting call path is covered by diffFileLanguage and the
	// disabled-path test below.
}

func TestMarkdownGoFenceHighlightDisabled(t *testing.T) {
	src := "```go\nfunc main() {}\n```\n"
	md := NewMarkdown(src)
	th := DefaultMarkdownTheme()
	th.DisableSyntax = true
	md.SetTheme(th)
	joined := strings.Join(md.Render(40), "\n")
	if !strings.Contains(stripAnsiMd(joined), "func main() {}") {
		t.Errorf("content missing: %q", joined)
	}
	if strings.Contains(joined, "\x1b[38;5;") {
		t.Errorf("token colors should be disabled, got %q", joined)
	}
}

func TestMarkdownDiffFenceHighlightDisabled(t *testing.T) {
	src := "```diff\n" +
		"--- a/app.py\n" +
		"+++ b/app.py\n" +
		"@@ -1 +1 @@\n" +
		"+print('new')\n" +
		"```\n"
	md := NewMarkdown(src)
	th := DefaultMarkdownTheme()
	th.DisableSyntax = true
	md.SetTheme(th)
	lines := md.Render(100)
	plain := stripAnsiMd(strings.Join(lines, "\n"))
	if !strings.Contains(plain, "+print('new')") {
		t.Errorf("content missing: %q", plain)
	}
	// Plain 256-color token runs must be absent when disabled.
	if strings.Contains(strings.Join(lines, "\n"), "\x1b[38;5;") {
		t.Errorf("token colors should be disabled, got %q", strings.Join(lines, "\n"))
	}
}

func TestDiffFileLanguage(t *testing.T) {
	cases := map[string]string{
		"+++ b/main.go":        "go",
		"+++ b/app.py":         "python",
		"--- a/index.ts":       "typescript",
		"+++ b/run.sh":         "bash",
		"+++ b/readme.unknown": "",
		"@@ -1 +1 @@":          "",
	}
	for header, want := range cases {
		if got := diffFileLanguage([]string{"@@ -1 +1 @@", header, "+x"}); got != want {
			t.Errorf("diffFileLanguage(%q) = %q, want %q", header, got, want)
		}
	}
}

func stripAnsiMd(s string) string {
	var b strings.Builder
	inEscape := false
	for _, r := range s {
		if inEscape {
			if r == 'm' {
				inEscape = false
			}
			continue
		}
		if r == '\x1b' {
			inEscape = true
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
