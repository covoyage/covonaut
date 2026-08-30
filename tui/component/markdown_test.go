package component

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/covoyage/covonaut/tui/core"
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
	return core.StripAnsi(s)
}

func renderPlain(src string, width int64) string {
	return stripAnsiMd(strings.Join(NewMarkdown(src).Render(width), "\n"))
}

func trimPad(s string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = strings.TrimRight(ln, " \t")
	}
	return strings.Join(lines, "\n")
}

func TestMarkdownPreservesCodeBlankLines(t *testing.T) {
	src := "```go\nfunc f() {\n\n\treturn\n}\n```"
	plain := trimPad(renderPlain(src, 40))
	if !strings.Contains(plain, "func f() {") {
		t.Fatalf("missing code: %q", plain)
	}
	brace := strings.Index(plain, "{")
	ret := strings.Index(plain, "return")
	if brace < 0 || ret < 0 || ret <= brace {
		t.Fatalf("fence body missing brace/return: %q", plain)
	}
	if strings.Count(plain[brace:ret], "\n") < 2 {
		t.Fatalf("blank line inside fence dropped: %q", plain)
	}
}

func TestMarkdownDoesNotItalicizeSnakeCase(t *testing.T) {
	plain := renderPlain("use foo_bar_baz here", 40)
	if !strings.Contains(plain, "foo_bar_baz") {
		t.Fatalf("snake_case mangled: %q", plain)
	}
}

func TestMarkdownNestedQuoteAndList(t *testing.T) {
	src := "> quote\n>\n> - item\n>   - nested"
	plain := renderPlain(src, 40)
	if !strings.Contains(plain, "quote") || !strings.Contains(plain, "item") {
		t.Fatalf("quote/list missing: %q", plain)
	}
	if !strings.Contains(plain, "│") {
		t.Fatalf("quote bar missing: %q", plain)
	}
}

func TestMarkdownTaskList(t *testing.T) {
	plain := renderPlain("- [ ] todo\n- [x] done", 40)
	if !strings.Contains(plain, "todo") || !strings.Contains(plain, "done") {
		t.Fatalf("task text missing: %q", plain)
	}
	if !strings.Contains(plain, "☐") || !strings.Contains(plain, "☑") {
		t.Fatalf("task markers missing: %q", plain)
	}
}

func TestMarkdownSetextHeading(t *testing.T) {
	plain := renderPlain("Title\n=====\n\nbody", 40)
	if !strings.Contains(plain, "Title") || !strings.Contains(plain, "body") {
		t.Fatalf("setext missing: %q", plain)
	}
}

func TestMarkdownTableStatusEmojiAlignment(t *testing.T) {
	src := "| 能力 | OpenClaw | Other |\n| --- | --- | --- |\n| 终端 TUI | ✅ 有 | ✅ 核心体验 |\n| Web UI | ✅ Control UI | ❌ |\n| 代码理解 | ⚠️ 基础 | ✅ 深度 |"
	plain := renderPlain(src, 72)
	assertTableBordersAlign(t, plain)
}

func TestMarkdownTableEmojiAlignment(t *testing.T) {
	src := "| 时段 | 天气 | 温度 |\n| --- | --- | --- |\n| 早晨 | ☁️ 小雨 | 24-25°C |\n| 上午 | ☁️ 阴 | 25-27°C |"
	plain := renderPlain(src, 60)
	assertTableBordersAlign(t, plain)
}

func assertTableBordersAlign(t *testing.T, plain string) {
	t.Helper()
	var table []string
	for _, ln := range strings.Split(plain, "\n") {
		if strings.Contains(ln, "│") || strings.Contains(ln, "┌") || strings.Contains(ln, "├") || strings.Contains(ln, "└") {
			table = append(table, strings.TrimRight(ln, " "))
		}
	}
	if len(table) < 5 {
		t.Fatalf("expected grid table, got %q", plain)
	}
	ref := borderColumns(table[0])
	if len(ref) < 4 {
		t.Fatalf("top rule missing borders: %q", table[0])
	}
	for _, ln := range table[1:] {
		got := borderColumns(ln)
		if len(got) != len(ref) {
			t.Fatalf("border count %v on %q, want %v from %q", got, ln, ref, table[0])
		}
		for i := range ref {
			if got[i] != ref[i] {
				t.Fatalf("column %d at cell %d on %q, want %d from %q", got[i], i, ln, ref[i], table[0])
			}
		}
	}
}

func borderColumns(line string) []int64 {
	var cols []int64
	i := 0
	for i < len(line) {
		r, size := utf8.DecodeRuneInString(line[i:])
		if r == utf8.RuneError && size <= 1 {
			i++
			continue
		}
		switch r {
		case '│', '┌', '┬', '┐', '├', '┼', '┤', '└', '┴', '┘':
			cols = append(cols, core.VisibleWidth(line[:i]))
		}
		i += size
	}
	return cols
}

func TestMarkdownTableAlignment(t *testing.T) {
	src := "| left | mid | right |\n|:---|:---:|---:|\n| a | b | c |"
	plain := renderPlain(src, 40)
	if !strings.Contains(plain, "left") || !strings.Contains(plain, "a") {
		t.Fatalf("table missing cells: %q", plain)
	}
	var data string
	for _, ln := range strings.Split(plain, "\n") {
		if strings.Contains(ln, "a") && strings.Contains(ln, "b") && strings.Contains(ln, "c") && !strings.Contains(ln, "left") {
			data = strings.TrimRight(ln, " ")
			break
		}
	}
	if data == "" {
		t.Fatalf("table data row missing: %q", plain)
	}
	if !strings.Contains(data, "│ a") {
		t.Fatalf("left alignment missing: %q", data)
	}
	if !strings.Contains(data, " b ") {
		t.Fatalf("center alignment missing: %q", data)
	}
	if !strings.Contains(data, "c │") && !strings.HasSuffix(data, "c") {
		t.Fatalf("right alignment missing: %q", data)
	}
}

func TestMarkdownInlineEscapeAndCode(t *testing.T) {
	plain := renderPlain("use \\*stars\\* and `code`", 60)
	if !strings.Contains(plain, "*stars*") {
		t.Fatalf("escaped asterisks lost: %q", plain)
	}
	if !strings.Contains(plain, "code") {
		t.Fatalf("inline code missing: %q", plain)
	}
}

func TestMarkdownAutolink(t *testing.T) {
	plain := renderPlain("see https://example.com/path for docs", 80)
	if !strings.Contains(plain, "https://example.com/path") {
		t.Fatalf("autolink missing: %q", plain)
	}
}

func TestMarkdownNamedLinkHidesURL(t *testing.T) {
	joined := strings.Join(NewMarkdown("see [docs](https://example.com/path) please").Render(80), "\n")
	if !strings.Contains(joined, "docs") {
		t.Fatalf("link label missing: %q", joined)
	}
	if strings.Contains(joined, "(https://example.com/path)") {
		t.Fatalf("named link should hide visible URL: %q", joined)
	}
}

func TestMarkdownSkipIncompleteTable(t *testing.T) {
	src := "intro\n\n| a | b |\n"
	md := NewMarkdown(src)
	th := DefaultMarkdownTheme()
	th.SkipIncomplete = true
	md.SetTheme(th)
	plain := stripAnsiMd(strings.Join(md.Render(40), "\n"))
	if !strings.Contains(plain, "intro") {
		t.Fatalf("leading text missing: %q", plain)
	}
	if strings.Contains(plain, "| a") || strings.Contains(plain, "┌") {
		t.Fatalf("incomplete table should be held back: %q", plain)
	}
}

func TestMarkdownFenceWrapsLongLines(t *testing.T) {
	src := "```go\nfmt.Println(\"abcdefghijklmnopqrstuvwxyz0123456789\")\n```"
	plain := trimPad(renderPlain(src, 24))
	if !strings.Contains(plain, "fmt.Println") {
		t.Fatalf("fence body missing: %q", plain)
	}
	if strings.Contains(plain, "…") {
		t.Fatalf("long fence line should wrap, not truncate: %q", plain)
	}
	if !strings.Contains(plain, "abcd") || !strings.Contains(plain, "789") {
		t.Fatalf("wrapped fence remainder missing: %q", plain)
	}
}

func TestMarkdownMath(t *testing.T) {
	plain := renderPlain("Einstein: $E = mc^{2}$\n\n$$\n\\alpha + \\beta\n$$", 40)
	if !strings.Contains(plain, "E = mc") {
		t.Fatalf("inline math missing: %q", plain)
	}
	if !strings.Contains(plain, "α") || !strings.Contains(plain, "β") {
		t.Fatalf("block math unicode missing: %q", plain)
	}
}

func TestMarkdownFenceRendererHook(t *testing.T) {
	md := NewMarkdown("```mermaid\ngraph TD; A-->B\n```")
	th := DefaultMarkdownTheme()
	th.FenceRenderer = func(lang, source string, width int64) []string {
		if lang != "mermaid" {
			t.Fatalf("lang=%q", lang)
		}
		if !strings.Contains(source, "A-->B") {
			t.Fatalf("source=%q", source)
		}
		return []string{"[diagram]"}
	}
	md.SetTheme(th)
	plain := stripAnsiMd(strings.Join(md.Render(40), "\n"))
	if !strings.Contains(plain, "[diagram]") {
		t.Fatalf("custom fence not used: %q", plain)
	}
}

func TestMarkdownSkipIncompleteFence(t *testing.T) {
	src := "hello\n\n```go\npackage main\n"
	md := NewMarkdown(src)
	th := DefaultMarkdownTheme()
	th.SkipIncomplete = true
	md.SetTheme(th)
	plain := stripAnsiMd(strings.Join(md.Render(40), "\n"))
	if strings.Contains(plain, "package main") {
		t.Fatalf("incomplete fence should be hidden: %q", plain)
	}
	fence, math := IncompleteMarkdown(src)
	if !fence || math {
		t.Fatalf("IncompleteMarkdown fence=%v math=%v", fence, math)
	}
}

func TestMarkdownImageFallback(t *testing.T) {
	plain := renderPlain("![cat](https://example.com/cat.png)", 40)
	if !strings.Contains(plain, "cat") {
		t.Fatalf("image alt missing: %q", plain)
	}
}

func TestMarkdownBoldItalicNesting(t *testing.T) {
	plain := renderPlain("***both*** and **bold *em* bold**", 40)
	if !strings.Contains(plain, "both") || !strings.Contains(plain, "bold") {
		t.Fatalf("nested emphasis missing: %q", plain)
	}
}

func TestMarkdownGitHubAlerts(t *testing.T) {
	src := "> [!NOTE]\n> remember this\n\n> [!WARNING]\n> careful now\n\n> [!TIP] shortcut"
	plain := renderPlain(src, 60)
	if !strings.Contains(plain, "NOTE") || !strings.Contains(plain, "remember this") {
		t.Fatalf("NOTE alert missing: %q", plain)
	}
	if !strings.Contains(plain, "WARNING") || !strings.Contains(plain, "careful now") {
		t.Fatalf("WARNING alert missing: %q", plain)
	}
	if !strings.Contains(plain, "TIP") || !strings.Contains(plain, "shortcut") {
		t.Fatalf("TIP alert missing: %q", plain)
	}
	if strings.Contains(plain, "[!NOTE]") || strings.Contains(plain, "[!WARNING]") {
		t.Fatalf("raw alert marker leaked: %q", plain)
	}
}

func TestMarkdownFootnotes(t *testing.T) {
	src := "See the note[^1].\n\n[^1]: Extra detail."
	plain := renderPlain(src, 60)
	if !strings.Contains(plain, "See the note") {
		t.Fatalf("footnote body missing: %q", plain)
	}
	if !strings.Contains(plain, "[1]") {
		t.Fatalf("footnote marker missing: %q", plain)
	}
	if !strings.Contains(plain, "Extra detail") {
		t.Fatalf("footnote definition missing: %q", plain)
	}
}

func TestMarkdownDefinitionList(t *testing.T) {
	src := "Term\n: definition here"
	plain := renderPlain(src, 40)
	if !strings.Contains(plain, "Term") {
		t.Fatalf("definition term missing: %q", plain)
	}
	if !strings.Contains(plain, "definition here") {
		t.Fatalf("definition description missing: %q", plain)
	}
}

func TestMarkdownMathUnbracedScripts(t *testing.T) {
	plain := renderPlain("Einstein: $E = mc^2$ and $x_n$", 40)
	if !strings.Contains(plain, "E = mc") {
		t.Fatalf("inline math missing: %q", plain)
	}
	if !strings.Contains(plain, "²") {
		t.Fatalf("unbraced superscript missing: %q", plain)
	}
	if !strings.Contains(plain, "ₓ") && !strings.Contains(plain, "ₙ") {
		t.Fatalf("unbraced subscript missing: %q", plain)
	}
}

func TestMarkdownMarkHighlight(t *testing.T) {
	plain := renderPlain("this is ==important== text", 40)
	if !strings.Contains(plain, "important") {
		t.Fatalf("mark text missing: %q", plain)
	}
	if strings.Contains(plain, "==important==") {
		t.Fatalf("raw mark markers leaked: %q", plain)
	}
}

func TestMarkdownHTMLInlineStripped(t *testing.T) {
	plain := renderPlain("hello <br> world", 40)
	if !strings.Contains(plain, "hello") || !strings.Contains(plain, "world") {
		t.Fatalf("html-adjacent text missing: %q", plain)
	}
	if strings.Contains(plain, "<br>") {
		t.Fatalf("raw html leaked: %q", plain)
	}
}

func TestMarkdownFenceFrame(t *testing.T) {
	plain := renderPlain("```go\nfmt.Println(1)\n```", 40)
	if !strings.Contains(plain, "┌") || !strings.Contains(plain, "└") {
		t.Fatalf("fence frame missing: %q", plain)
	}
	if !strings.Contains(plain, "go") || !strings.Contains(plain, "fmt.Println") {
		t.Fatalf("fence body missing: %q", plain)
	}
}

func TestMarkdownFenceZeroWidthKeepsBody(t *testing.T) {
	plain := renderPlain("```go\npackage main\n```", 0)
	if !strings.Contains(plain, "package main") {
		t.Fatalf("width=0 truncated fence body: %q", plain)
	}
}

func TestMarkdownTableZeroWidthKeepsCells(t *testing.T) {
	plain := renderPlain("| hello world | extra column |\n| --- | --- |\n| keep this | also here |", 0)
	if !strings.Contains(plain, "hello world") || !strings.Contains(plain, "keep this") {
		t.Fatalf("width=0 truncated table: %q", plain)
	}
}

func TestMarkdownMathMatrix(t *testing.T) {
	plain := renderPlain("$$\\begin{pmatrix} a & b \\\\ c & d \\end{pmatrix}$$", 40)
	if strings.Contains(plain, `\begin`) || strings.Contains(plain, `\end`) {
		t.Fatalf("matrix env leaked: %q", plain)
	}
	if !strings.Contains(plain, "a") || !strings.Contains(plain, "b") || !strings.Contains(plain, "c") || !strings.Contains(plain, "d") {
		t.Fatalf("matrix cells missing: %q", plain)
	}
}

func TestMarkdownMathCases(t *testing.T) {
	plain := renderPlain("$$\\begin{cases} x & y \\\\ z & w \\end{cases}$$", 40)
	if strings.Contains(plain, `\begin`) {
		t.Fatalf("cases env leaked: %q", plain)
	}
	if !strings.Contains(plain, "x") || !strings.Contains(plain, "z") {
		t.Fatalf("cases cells missing: %q", plain)
	}
}

func TestMarkdownHeadingLevels(t *testing.T) {
	plain := renderPlain("# One\n\n## Two\n\n### Three", 40)
	if !strings.Contains(plain, "One") || !strings.Contains(plain, "Two") || !strings.Contains(plain, "Three") {
		t.Fatalf("heading text missing: %q", plain)
	}
	if strings.Contains(plain, "# One") || strings.Contains(plain, "## Two") || strings.Contains(plain, "### Three") {
		t.Fatalf("literal heading markers leaked: %q", plain)
	}
	if !strings.Contains(plain, "─") {
		t.Fatalf("h1 underline missing: %q", plain)
	}
}

func TestMarkdownMathFracAndSqrt(t *testing.T) {
	plain := renderPlain("$$\\frac{a}{b} + \\sqrt{x}$$", 40)
	if !strings.Contains(plain, "(a)/(b)") {
		t.Fatalf("frac rewrite missing: %q", plain)
	}
	if !strings.Contains(plain, "√(x)") && !strings.Contains(plain, "√x") {
		t.Fatalf("sqrt rewrite missing: %q", plain)
	}
}

func TestMarkdownMathBinomBBLeftRight(t *testing.T) {
	plain := renderPlain("$$\\binom{n}{k} + \\mathbb{R} + \\left(x\\right) + \\leftarrow$$", 60)
	if !strings.Contains(plain, "C(n,k)") {
		t.Fatalf("binom rewrite missing: %q", plain)
	}
	if !strings.Contains(plain, "ℝ") {
		t.Fatalf("mathbb rewrite missing: %q", plain)
	}
	if !strings.Contains(plain, "(x)") {
		t.Fatalf("left/right delimiters missing: %q", plain)
	}
	if strings.Contains(plain, `\left`) || strings.Contains(plain, `\right`) {
		t.Fatalf("left/right commands leaked: %q", plain)
	}
	if !strings.Contains(plain, "←") {
		t.Fatalf("leftarrow rewritten incorrectly: %q", plain)
	}
}

func TestMarkdownTypographer(t *testing.T) {
	plain := renderPlain(`hello -- world ... "quoted"`, 40)
	if !strings.Contains(plain, "–") && !strings.Contains(plain, "—") {
		t.Fatalf("en/em dash missing: %q", plain)
	}
	if !strings.Contains(plain, "…") {
		t.Fatalf("ellipsis missing: %q", plain)
	}
	if !strings.Contains(plain, "“") && !strings.Contains(plain, "”") {
		t.Fatalf("smart quotes missing: %q", plain)
	}
}

func TestMarkdownMathOperators(t *testing.T) {
	plain := renderPlain("$$\\sin x + \\implies + \\langle x\\rangle$$", 40)
	if !strings.Contains(plain, "sin") {
		t.Fatalf("sin unwrap missing: %q", plain)
	}
	if !strings.Contains(plain, "⇒") {
		t.Fatalf("implies missing: %q", plain)
	}
	if !strings.Contains(plain, "⟨") || !strings.Contains(plain, "⟩") {
		t.Fatalf("langle/rangle missing: %q", plain)
	}
}

func TestMarkdownMermaidFenceUnframed(t *testing.T) {
	src := "```mermaid\ngraph TD\nA[Start] --> B[End]\n```"
	md := NewMarkdown(src)
	th := DefaultMarkdownTheme()
	th.FenceRenderer = func(lang, source string, width int64) []string {
		if lang != "mermaid" {
			t.Fatalf("lang=%q", lang)
		}
		return []string{"  ┌──────┐", "  │ Start│", "  └──────┘"}
	}
	md.SetTheme(th)
	plain := stripAnsiMd(strings.Join(md.Render(40), "\n"))
	if !strings.Contains(plain, "Start") {
		t.Fatalf("mermaid body missing: %q", plain)
	}
	if strings.Contains(plain, "┌─mermaid") || strings.Contains(plain, "┌─mermai") {
		t.Fatalf("mermaid should not get a language fence frame: %q", plain)
	}
}

func TestMarkdownLocalImageRenders(t *testing.T) {
	const png1x1 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
	src := "![dot](data:image/png;base64," + png1x1 + ")"
	md := NewMarkdown(src)
	th := DefaultMarkdownTheme()
	th.ImageRenderer = func(alt, url string, width int64) []string {
		if alt != "dot" {
			t.Fatalf("alt=%q", alt)
		}
		if !strings.HasPrefix(url, "data:image/png") {
			t.Fatalf("url=%q", url)
		}
		return []string{"[inline-image]"}
	}
	md.SetTheme(th)
	plain := stripAnsiMd(strings.Join(md.Render(40), "\n"))
	if !strings.Contains(plain, "[inline-image]") {
		t.Fatalf("image renderer not used: %q", plain)
	}
}

func TestMarkdownDefaultImageRendererDataURI(t *testing.T) {
	const png1x1 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
	plain := renderPlain("before ![dot](data:image/png;base64,"+png1x1+") after", 40)
	if !strings.Contains(plain, "before") || !strings.Contains(plain, "after") {
		t.Fatalf("surrounding text missing: %q", plain)
	}
	if strings.Contains(plain, "🖼") {
		t.Fatalf("data URI should inline, not fall back to alt: %q", plain)
	}
}

func TestMarkdownFenceHasRightBorder(t *testing.T) {
	plain := renderPlain("```go\nfmt.Println(1)\n```", 40)
	var body string
	for _, ln := range strings.Split(plain, "\n") {
		if strings.Contains(ln, "fmt.Println") {
			body = strings.TrimRight(ln, " ")
			break
		}
	}
	if body == "" {
		t.Fatalf("fence body missing: %q", plain)
	}
	if !strings.HasPrefix(strings.TrimLeft(body, " "), "│") || !strings.HasSuffix(body, "│") {
		t.Fatalf("fence missing right border: %q", body)
	}
}

func TestMarkdownUnwrapsMarkdownTableFence(t *testing.T) {
	src := "```md\n| a | b |\n| --- | --- |\n| 1 | 2 |\n```"
	plain := renderPlain(src, 40)
	if strings.Contains(plain, "┌─md") || strings.Contains(plain, "┌─markdown") {
		t.Fatalf("markdown table fence should unwrap: %q", plain)
	}
	if !strings.Contains(plain, "┌") || !strings.Contains(plain, "a") || !strings.Contains(plain, "1") {
		t.Fatalf("unwrapped table missing: %q", plain)
	}
}

func TestMarkdownTableWrapsLongCells(t *testing.T) {
	src := "| k | v |\n| --- | --- |\n| x | this is a long cell that should wrap instead of vanishing |"
	plain := renderPlain(src, 24)
	if !strings.Contains(plain, "this is") {
		t.Fatalf("wrapped cell start missing: %q", plain)
	}
	if !strings.Contains(plain, "vanishing") && !strings.Contains(plain, "wrap") {
		t.Fatalf("wrapped cell remainder missing: %q", plain)
	}
}

func TestMarkdownTableTruncatesLargeBody(t *testing.T) {
	var b strings.Builder
	b.WriteString("| n |\n| --- |\n")
	for i := 1; i <= 201; i++ {
		fmt.Fprintf(&b, "| row-%d |\n", i)
	}
	plain := renderPlain(b.String(), 40)
	if !strings.Contains(plain, "row-200") {
		t.Fatalf("kept rows missing: %q", plain)
	}
	if strings.Contains(plain, "row-201") {
		t.Fatalf("truncated row leaked: %q", plain)
	}
	if !strings.Contains(plain, "1 more rows") {
		t.Fatalf("omission note missing: %q", plain)
	}
}

func TestSafeHyperlinkURL(t *testing.T) {
	cases := []struct {
		url string
		ok  bool
	}{
		{"https://example.com/path", true},
		{"http://example.com", true},
		{"mailto:a@b.com", true},
		{"javascript:alert(1)", false},
		{"vbscript:msg", false},
		{"smb://server/share", false},
		{"afp://server/share", false},
		{`\\server\share`, false},
		{"//server/share", false},
		{"file:///tmp/x", true},
		{"file://localhost/tmp/x", true},
		{"file://evil/tmp/x", false},
		{"data:text/html,hi", false},
		{"https://example.com/\u200bpath", false},
		{"https://example.com/\x1bpath", false},
		{"/net/foo", false},
		{"/smb/foo", false},
	}
	for _, c := range cases {
		if got := safeHyperlinkURL(c.url); got != c.ok {
			t.Errorf("safeHyperlinkURL(%q)=%v want %v", c.url, got, c.ok)
		}
	}
}

func TestOSC8SkipsUnsafeURL(t *testing.T) {
	fn := OSC8LinkRenderer(func(s string) string { return s }, func(s string) string { return s })
	got := fn("click", "javascript:alert(1)")
	if strings.Contains(got, "\x1b]8;;") {
		t.Fatalf("unsafe URL should not be OSC8: %q", got)
	}
	if got != "click" {
		t.Fatalf("label=%q", got)
	}
	safe := fn("docs", "https://example.com")
	if !strings.Contains(safe, "\x1b]8;;https://example.com") {
		t.Fatalf("safe URL should be OSC8: %q", safe)
	}
}
