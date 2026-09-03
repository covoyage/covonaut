package component

// ---------------------------------------------------------------------------
// Checkpoint streaming render (md_checkpoint.go).
//
// A long-streaming markdown document is re-rendered on every frame. Rendering
// the *whole* doc each time re-parses every completed block even though only
// the tail changed. MarkdownState instead splits the source at the "checkpoint"
// — the start of the current top-level unclosed fence / math / table — and
// memoises the rendered lines of the stable prefix. While the open block keeps
// growing the prefix is byte-identical, so the render is reused wholesale and
// only the (usually tiny) growing tail is dropped, matching the SkipIncomplete
// clipping semantics of renderMarkdown.
//
// When no block is open the checkpoint advances to end-of-source: the full
// document renders once per visible change, then stays cached until the next
// delta. Each close/open transition costs exactly one re-parse of the prefix.
// ---------------------------------------------------------------------------

// MarkdownState is the per-message incremental render memo. It is not safe
// for concurrent use; ChatHistory calls it under its own render lock.
type MarkdownState struct {
	src    string // full source rendered (used for SkipIncomplete=false path)
	cut    int    // prefix length last rendered
	pfxSrc string // src[:cut] at last render
	width  int64
	rev    uint64 // palette/theme revision that produced the cached lines
	skip   bool   // SkipIncomplete of the cached render
	lines  []string
}

// incompleteClipCut returns the prefix length after which all remaining input
// is an unclosed top-level table (or after the fence/math opener line), plus
// whether such an open block exists. It mirrors clipIncompleteMarkdown so the
// split render is byte-for-byte identical to the single-doc render.
func incompleteClipCut(src string) (cut int, open bool) {
	fmCut, fence, math := scanIncomplete(src)
	if fence || math {
		src = src[:fmCut]
	} else {
		fmCut = len(src)
	}
	if tCut, ok := incompleteTableCut(src); ok {
		return tCut, true
	}
	if fence || math {
		return fmCut, true
	}
	return len(src), false
}

// Render renders src at the given width, reusing the memo whenever the
// checkpoint prefix, width and revision are unchanged. The caller supplies
// the revision (palette snapshot across theme swaps) so a theme change
// cheaply invalidates every message memo. A partial theme is merged with the
// defaults exactly like component.Render, so missing style funcs never panic.
func (s *MarkdownState) Render(src string, width int64, theme MarkdownTheme, rev uint64) []string {
	theme = mergeMarkdownTheme(theme)
	skip := theme.SkipIncomplete
	if s.width == width && s.rev == rev && s.skip == skip {
		if !skip && s.src == src {
			return s.lines
		}
		if skip && s.cut == len(s.pfxSrc) {
			cut, open := incompleteClipCut(src)
			if open && cut == s.cut && windowStartsWith(src, s.pfxSrc) {
				return s.lines
			}
		}
	}
	s.width = width
	s.rev = rev
	s.skip = skip
	if !skip {
		s.src = src
		s.lines = renderMarkdown(src, width, theme)
		return s.lines
	}
	cut, _ := incompleteClipCut(src)
	s.cut = cut
	s.pfxSrc = src[:cut]
	s.lines = renderMarkdown(s.pfxSrc, width, theme)
	return s.lines
}

// windowStartsWith reports whether s starts with prefix. The common streaming
// case appends at the end, so this is cheap (a length check plus a memcmp on
// the shared prefix — Go's string == is a length+memcmp, so an explicit call
// only makes the reuse intent visible).
func windowStartsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// Reset drops all cached lines so the next Render is a fresh full render.
func (s *MarkdownState) Reset() {
	s.src, s.pfxSrc = "", ""
	s.cut = 0
	s.width = 0
	s.lines = nil
}
