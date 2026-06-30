package component

import (
	"strings"
	"testing"

	"github.com/covoyage/covonaut/tui/theme"
)

// flatten returns all tokens of all lines concatenated, useful for asserting
// that a given literal produced a specific kind regardless of layout.
func tokensFor(source, lang string) []Token {
	spec := LookupLanguage(lang)
	if spec == nil {
		return nil
	}
	var out []Token
	for _, line := range tokenize(source, spec) {
		out = append(out, line...)
	}
	return out
}

func hasToken(toks []Token, kind TokenKind, text string) bool {
	for _, t := range toks {
		if t.Kind == kind && t.Text == text {
			return true
		}
	}
	return false
}

func TestSyntaxGoKeywordsAndStrings(t *testing.T) {
	src := "package main\n\nfunc main() {\n\ts := \"hello\"\n\t_ = s\n}\n"
	toks := tokensFor(src, "go")
	if !hasToken(toks, TokKeyword, "package") {
		t.Fatalf("missing keyword 'package'")
	}
	if !hasToken(toks, TokKeyword, "func") {
		t.Fatalf("missing keyword 'func'")
	}
	if !hasToken(toks, TokFunction, "main") {
		t.Fatalf("missing function 'main'")
	}
	if !hasToken(toks, TokString, `"hello"`) {
		t.Fatalf("missing string literal")
	}
}

func TestSyntaxGoRawStringAndComments(t *testing.T) {
	src := "// line comment\n/* block\n still comment */\n`raw`\n"
	toks := tokensFor(src, "go")
	foundLineComment := false
	foundBlockComment := false
	foundRaw := false
	for _, tok := range toks {
		if tok.Kind == TokComment && strings.Contains(tok.Text, "line comment") {
			foundLineComment = true
		}
		if tok.Kind == TokComment && strings.Contains(tok.Text, "still comment") {
			foundBlockComment = true
		}
		if tok.Kind == TokString && tok.Text == "`raw`" {
			foundRaw = true
		}
	}
	if !foundLineComment || !foundBlockComment || !foundRaw {
		t.Fatalf("line=%v block=%v raw=%v", foundLineComment, foundBlockComment, foundRaw)
	}
}

func TestSyntaxPythonHashComment(t *testing.T) {
	src := "def f():\n    # greet\n    return 'hi'\n"
	toks := tokensFor(src, "py")
	if !hasToken(toks, TokKeyword, "def") {
		t.Fatalf("def not classified as keyword")
	}
	foundComment := false
	for _, tok := range toks {
		if tok.Kind == TokComment && strings.Contains(tok.Text, "greet") {
			foundComment = true
		}
	}
	if !foundComment {
		t.Fatalf("hash comment not classified")
	}
	if !hasToken(toks, TokString, "'hi'") {
		t.Fatalf("single-quoted string missing")
	}
}

func TestSyntaxJSONTypes(t *testing.T) {
	src := `{"a":true,"b":42}`
	toks := tokensFor(src, "json")
	if !hasToken(toks, TokKeyword, "true") {
		t.Fatalf("true should be a keyword")
	}
	if !hasToken(toks, TokNumber, "42") {
		t.Fatalf("42 should be a number")
	}
}

func TestSyntaxTypeScriptInterfaces(t *testing.T) {
	src := "interface User { name: string; age: number }"
	toks := tokensFor(src, "ts")
	if !hasToken(toks, TokKeyword, "interface") {
		t.Fatalf("interface not classified as keyword")
	}
	if !hasToken(toks, TokType, "string") {
		t.Fatalf("string not classified as type")
	}
	if !hasToken(toks, TokType, "number") {
		t.Fatalf("number not classified as type")
	}
}

func TestSyntaxHighlightRendersAnsi(t *testing.T) {
	theme.ForceColor(true)
	t.Cleanup(func() { theme.ForceColor(false) })
	lines := Highlight("if x { }", "go", DefaultSyntaxTheme())
	if len(lines) == 0 {
		t.Fatalf("no output")
	}
	if !strings.Contains(lines[0], "\x1b[") {
		t.Fatalf("expected ANSI escapes, got %q", lines[0])
	}
}

func TestSyntaxMarkdownIntegration(t *testing.T) {
	theme.ForceColor(true)
	t.Cleanup(func() { theme.ForceColor(false) })
	src := "```go\npackage main\n\nfunc main() {}\n```\n"
	lines := NewMarkdown(src).Render(40)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "package") {
		t.Fatalf("code block content missing: %q", joined)
	}
	if !strings.Contains(joined, "\x1b[") {
		t.Fatalf("code block should be highlighted (ansi missing): %q", joined)
	}
}

func TestSyntaxUnknownLanguageFallsThrough(t *testing.T) {
	lines := Highlight("whatever", "no-such-lang", DefaultSyntaxTheme())
	if len(lines) != 1 {
		t.Fatalf("unexpected line count: %d", len(lines))
	}
	if strings.Contains(lines[0], "\x1b[") {
		t.Fatalf("unknown lang should produce plain text, got %q", lines[0])
	}
}

func TestSyntaxComponentCachesAcrossRenders(t *testing.T) {
	s := NewSyntax("x := 1", "go")
	first := s.Render(40)
	second := s.Render(40)
	if len(first) != len(second) {
		t.Fatalf("cache mismatch: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("line %d differs after cache hit", i)
		}
	}
	s.SetLanguage("python")
	third := s.Render(40)
	if len(third) == 0 {
		t.Fatalf("re-render after language change failed")
	}
}
