package component

import (
	"strings"
	"testing"
)

// defaultTestTheme mirrors the default markdown theme with SkipIncomplete
// configurable per call; revisions exercise the memo invalidation path.
func testCheckpointState() (*MarkdownState, MarkdownTheme) {
	return &MarkdownState{}, defaultMarkdownTheme()
}

// TestMarkdownStateSkipIncompleteEquivalence proves the checkpoint split
// (prefix cut + memo) renders byte-identically to the single-doc clip path
// across a growing streaming document: plain text, growing fences, growing
// tables, math blocks, and close/open transitions.
func TestMarkdownStateSkipIncompleteEquivalence(t *testing.T) {
	state, theme := testCheckpointState()
	theme.SkipIncomplete = true
	docs := []string{
		"",
		"head",
		"head\n\npara",
		"head\n\n```go\n",
		"head\n\n```go\nfmt.Println(1)\n",
		"head\n\n```go\nfmt.Println(1)\nfmt.Println(2)\n",
		"head\n\n```go\nfmt.Println(1)\nfmt.Println(2)\n```",
		"head\n\nafter fence",
		"head\n\n| a | b |\n",
		"head\n\n| a | b |\n| --- | --- |\n",
		"head\n\n| a | b |\n| --- | --- |\n| 1 | 2 |\n",
		"head\n\n$$\nx",
		"head\n\n$$\nx\ny\n$$",
		"head\n\n```go\nx\n```\n\nanother ```\nmid",
	}
	for i, src := range docs {
		got := state.Render(src, 40, theme, 1)
		want := renderMarkdown(clipIncompleteMarkdown(src), 40, theme)
		if len(got) != len(want) {
			t.Fatalf("doc %d: line count = %d, want %d\nsrc=%q", i, len(got), len(want), src)
		}
		for l := 0; l < len(got); l++ {
			if got[l] != want[l] {
				t.Fatalf("doc %d line %d mismatch:\n got %q\nwant %q\nsrc=%q",
					i, l, got[l], want[l], src)
			}
		}
	}
}

// TestMarkdownStateReusesStablePrefix verifies the checkpoint actually
// short-circuits while an open block grows: rendering the same stream twice
// must return identical output, and appending inside the fence must keep the
// previously rendered prefix (before the fence opener) byte-identical.
func TestMarkdownStateReusesStablePrefix(t *testing.T) {
	state, theme := testCheckpointState()
	theme.SkipIncomplete = true
	base := func(src string) []string {
		return renderMarkdown(clipIncompleteMarkdown(src), 40, theme)
	}

	src1 := "intro line\n\n```go\nfirst\n"
	got1 := state.Render(src1, 40, theme, 1)
	if !sameStrings(got1, base(src1)) {
		t.Fatal("frame 1 mismatch")
	}

	// Identical call must be served from the memo.
	got1b := state.Render(src1, 40, theme, 1)
	if !sameStrings(got1b, got1) {
		t.Fatal("memoised re-render changed output")
	}

	// Growing the fence body leaves the frozen prefix intact.
	src2 := src1 + "second\nthird\n"
	got2 := state.Render(src2, 40, theme, 1)
	if !sameStrings(got2, base(src2)) {
		t.Fatal("frame 2 mismatch")
	}
	if !sameStrings(got2, got1) {
		t.Fatalf("fence growth should not change rendered lines:\n got %q\nwant %q", got2, got1)
	}

	// Table growth behaves the same way.
	state.Reset()
	srcT1 := "| a | b |\n| 1 | 2 |\n"
	gotT1 := state.Render(srcT1, 40, theme, 1)
	if !sameStrings(gotT1, base(srcT1)) {
		t.Fatalf("table frame 1 mismatch: %q vs %q", gotT1, base(srcT1))
	}
	srcT2 := srcT1 + "| 3 | 4 |\n"
	gotT2 := state.Render(srcT2, 40, theme, 1)
	if !sameStrings(gotT2, gotT1) {
		t.Fatalf("table growth should be clipped identically:\n got %q\nwant %q", gotT2, gotT1)
	}
	if !sameStrings(gotT2, base(srcT2)) {
		t.Fatalf("table frame 2 mismatch vs clip: %q vs %q", gotT2, base(srcT2))
	}
}

// TestMarkdownStateRevisionInvalidation verifies that a palette/theme revision
// bump forces a fresh render (content may be identical, but the memo must not
// be trusted across theme swaps).
func TestMarkdownStateRevisionInvalidation(t *testing.T) {
	state, theme := testCheckpointState()
	theme.SkipIncomplete = true
	src := "plain\n\n```go\nx\n```\n\ntail"
	for rev := uint64(0); rev < 3; rev++ {
		got := state.Render(src, 40, theme, rev)
		want := renderMarkdown(src, 40, theme)
		if !sameStrings(got, want) {
			t.Fatalf("rev %d mismatch", rev)
		}
	}
}

// TestMarkdownStateNonSkipPath verifies the full-document path (interactive
// preview, SkipIncomplete=false) equals the plain renderer and memoises.
func TestMarkdownStateNonSkipPath(t *testing.T) {
	state, theme := testCheckpointState()
	theme.SkipIncomplete = false
	src := "open ```go\nfmt.Println(1)\n"
	got := state.Render(src, 40, theme, 1)
	want := renderMarkdown(src, 40, theme)
	if !sameStrings(got, want) {
		t.Fatalf("non-skip mismatch:\n got %q\nwant %q", got, want)
	}
	again := state.Render(src, 40, theme, 1)
	if !sameStrings(again, got) {
		t.Fatal("non-skip memo not serving identical call")
	}
	if len(got) == 0 {
		t.Fatal("non-skip render of an open fence should produce content, got nothing")
	}
	if !strings.Contains(renderPlain(src, 40), "fmt.Println") {
		t.Fatal("sanity: plain render should include fence body")
	}
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
