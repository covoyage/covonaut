package component

import (
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/covoyage/covonaut/tui/core"
	apitheme "github.com/covoyage/covonaut/tui/theme"
)

// ---------------------------------------------------------------------------
// Markdown — a block-level markdown renderer component.
//
// Supported block types:
//   - ATX headings (# … ######).
//   - Fenced code blocks (``` / ~~~) with optional language label.
//   - Blockquotes (> ...).
//   - Bulleted lists (*, -, +) with nesting.
//   - Numbered lists (1. 2. 3.).
//   - Horizontal rule (---, ***, ___).
//   - Pipe tables.
//   - Paragraphs (word-wrapped).
//
// Supported inline styles:
//   - **bold** / __bold__
//   - *italic* / _italic_
//   - `code`
//   - ~~strike~~
//   - [label](url)
//
// The goal is readable terminal rendering; not a spec-compliant parser.
// ---------------------------------------------------------------------------

// MarkdownTheme overrides the ANSI styling of rendered elements.
type MarkdownTheme struct {
	HeadingFn     [6]func(string) string // h1..h6
	EmphasisFn    func(string) string    // italic
	StrongFn      func(string) string    // bold
	StrikeFn      func(string) string
	CodeInlineFn  func(string) string
	CodeBlockFn   func(string) string
	CodeFenceFn   func(string) string // language label line
	QuoteFn       func(string) string
	LinkLabelFn   func(string) string
	LinkURLFn     func(string) string
	HRFn          func(string) string
	ListBulletFn  func(string) string
	TableBorderFn func(string) string
	TableHeaderFn func(string) string
	// Syntax, when set, is used to style fenced code blocks with a
	// language tag. A nil value falls back to CodeBlockFn.
	Syntax *SyntaxTheme
}

// syntaxThemeFromMarkdown bridges a MarkdownTheme into a SyntaxTheme so
// fenced code blocks can be highlighted by the Syntax tokenizer. Falls back
// to a palette derived from CodeBlockFn when Syntax is nil.
func syntaxThemeFromMarkdown(t MarkdownTheme) SyntaxTheme {
	if t.Syntax != nil {
		return *t.Syntax
	}
	dflt := DefaultSyntaxTheme()
	if t.CodeBlockFn != nil {
		dflt.TextFn = t.CodeBlockFn
		dflt.PunctuationFn = t.CodeBlockFn
		dflt.OperatorFn = t.CodeBlockFn
	}
	return dflt
}

// Markdown is a Component that renders a markdown string.
type Markdown struct {
	mu sync.RWMutex

	source string
	theme  MarkdownTheme

	cacheWidth int64
	cacheLines []string
	dirty      bool
}

// NewMarkdown creates a Markdown component.
func NewMarkdown(source string) *Markdown {
	return &Markdown{source: source, dirty: true, theme: defaultMarkdownTheme()}
}

// SetSource replaces the markdown content.
func (m *Markdown) SetSource(s string) {
	m.mu.Lock()
	m.source = s
	m.dirty = true
	m.mu.Unlock()
}

// SetTheme installs a custom theme (missing fields fall back to defaults).
func (m *Markdown) SetTheme(t MarkdownTheme) {
	m.mu.Lock()
	m.theme = mergeMarkdownTheme(t)
	m.dirty = true
	m.mu.Unlock()
}

// Render produces lines wrapped to the given width.
func (m *Markdown) Render(width int64) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.dirty && m.cacheWidth == width && m.cacheLines != nil {
		return m.cacheLines
	}
	lines := renderMarkdown(m.source, width, m.theme)
	m.cacheLines = lines
	m.cacheWidth = width
	m.dirty = false
	return lines
}

func (m *Markdown) Invalidate() {
	m.mu.Lock()
	m.dirty = true
	m.cacheLines = nil
	m.mu.Unlock()
}

func (m *Markdown) Update(msg core.Msg) core.Cmd {
	switch msg.(type) {
	case core.WindowSizeMsg:
		m.Invalidate()
	}
	return nil
}

// ---------------------------------------------------------------------------
// Parser / renderer
// ---------------------------------------------------------------------------

var (
	reHeading  = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	reFence    = regexp.MustCompile(`^(` + "```" + `|~~~)\s*(\S*)\s*$`)
	reHR       = regexp.MustCompile(`^\s*(-{3,}|\*{3,}|_{3,})\s*$`)
	reBullet   = regexp.MustCompile(`^(\s*)([\-*+])\s+(.*)$`)
	reOrdered  = regexp.MustCompile(`^(\s*)(\d+)\.\s+(.*)$`)
	reQuote    = regexp.MustCompile(`^>\s?(.*)$`)
	reTableSep = regexp.MustCompile(`^\s*\|?(\s*:?-+:?\s*\|)+\s*:?-+:?\s*\|?\s*$`)

	reInlineBold   = regexp.MustCompile(`\*\*([^*]+)\*\*|__([^_]+)__`)
	reInlineItalic = regexp.MustCompile(`\*([^*\s][^*]*[^*\s]|[^*\s])\*|_([^_\s][^_]*[^_\s]|[^_\s])_`)
	reInlineCode   = regexp.MustCompile("`([^`]+)`")
	reInlineStrike = regexp.MustCompile(`~~([^~]+)~~`)
	reInlineLink   = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
)

func renderMarkdown(src string, width int64, theme MarkdownTheme) []string {
	lines := strings.Split(src, "\n")
	var out []string
	i := 0
	for i < len(lines) {
		ln := lines[i]

		// Fenced code block
		if fm := reFence.FindStringSubmatch(ln); fm != nil {
			fence := fm[1]
			lang := fm[2]
			if lang != "" {
				out = append(out, theme.CodeFenceFn(core.PadToWidth("  "+lang, width)))
			}
			i++
			var codeLines []string
			for i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), fence) {
				codeLines = append(codeLines, lines[i])
				i++
			}
			if i < len(lines) {
				i++ // consume closing fence
			}

			// If a language is specified and a highlighter exists, run it
			// over the whole block so multi-line block comments/strings
			// resolve correctly.
			var rendered []string
			if lang == "diff" {
				rendered = make([]string, len(codeLines))
				var oldLine, newLine int
				for k, cl := range codeLines {
					switch {
					case strings.HasPrefix(cl, "@@ "):
						// Parse hunk header to extract line numbers.
						rendered[k] = apitheme.CurrentPalette().Accent.Render(theme.CodeBlockFn(cl))
						if _, err := fmt.Sscanf(cl, "@@ -%d", &oldLine); err == nil {
							newLine = oldLine
							if idx := strings.Index(cl, "+"); idx > 0 {
								if _, err2 := fmt.Sscanf(cl[idx:], "+%d", &newLine); err2 != nil {
									newLine = oldLine
								}
							}
						}
					case strings.HasPrefix(cl, "+++ ") || strings.HasPrefix(cl, "--- "):
						rendered[k] = theme.CodeBlockFn(cl)
					case strings.HasPrefix(cl, "+") && !strings.HasPrefix(cl, "++"):
						rendered[k] = apitheme.CurrentPalette().Success.Render(fmt.Sprintf("%4d %s", newLine, theme.CodeBlockFn(cl)))
						newLine++
					case strings.HasPrefix(cl, "-") && !strings.HasPrefix(cl, "--"):
						rendered[k] = apitheme.CurrentPalette().Error.Render(fmt.Sprintf("%4d %s", oldLine, theme.CodeBlockFn(cl)))
						oldLine++
					default:
						rendered[k] = fmt.Sprintf("     %s", theme.CodeBlockFn(cl))
						oldLine++
						newLine++
					}
				}
			} else if spec := LookupLanguage(lang); spec != nil {
				rendered = Highlight(strings.Join(codeLines, "\n"), lang, syntaxThemeFromMarkdown(theme))
			} else {
				rendered = make([]string, len(codeLines))
				for k, cl := range codeLines {
					rendered[k] = theme.CodeBlockFn(cl)
				}
			}
			for _, cl := range rendered {
				if cl == "" {
					continue
				}
				out = append(out, core.PadToWidth("  "+cl, width))
			}
			continue
		}

		if reHR.MatchString(ln) {
			out = append(out, theme.HRFn(core.PadToWidth(strings.Repeat("─", int(width)), width)))
			i++
			continue
		}

		if hm := reHeading.FindStringSubmatch(ln); hm != nil {
			level := len(hm[1]) - 1
			if level > 5 {
				level = 5
			}
			text := renderInline(hm[2], theme)
			fn := theme.HeadingFn[level]
			wrapped := core.WrapAnsi(fn(text), width)
			for _, w := range wrapped {
				out = append(out, core.PadToWidth(w, width))
			}
			i++
			continue
		}

		if qm := reQuote.FindStringSubmatch(ln); qm != nil {
			body := qm[1]
			text := renderInline(body, theme)
			for _, w := range core.WrapAnsi(text, width-2) {
				line := theme.QuoteFn("│ ") + w
				out = append(out, core.PadToWidth(line, width))
			}
			i++
			continue
		}

		// Detect a table: a header row followed by a separator row.
		if strings.Contains(ln, "|") && i+1 < len(lines) && reTableSep.MatchString(lines[i+1]) {
			end := i + 2
			for end < len(lines) && strings.Contains(lines[end], "|") {
				end++
			}
			rows := lines[i:end]
			out = append(out, renderTable(rows, width, theme)...)
			i = end
			continue
		}

		if bm := reBullet.FindStringSubmatch(ln); bm != nil {
			indent := len(bm[1])
			text := renderInline(bm[3], theme)
			bullet := theme.ListBulletFn("• ")
			indentStr := strings.Repeat(" ", indent+2)
			for k, w := range core.WrapAnsi(text, width-int64(indent)-3) {
				prefix := indentStr
				if k == 0 {
					prefix = strings.Repeat(" ", indent) + bullet
				}
				out = append(out, core.PadToWidth(prefix+w, width))
			}
			i++
			continue
		}
		if om := reOrdered.FindStringSubmatch(ln); om != nil {
			indent := len(om[1])
			num := om[2] + ". "
			text := renderInline(om[3], theme)
			indentStr := strings.Repeat(" ", indent+len(num))
			for k, w := range core.WrapAnsi(text, width-int64(indent)-int64(len(num))) {
				prefix := indentStr
				if k == 0 {
					prefix = strings.Repeat(" ", indent) + theme.ListBulletFn(num)
				}
				out = append(out, core.PadToWidth(prefix+w, width))
			}
			i++
			continue
		}

		if strings.TrimSpace(ln) == "" {
			out = append(out, core.PadToWidth("", width))
			i++
			continue
		}

		// Paragraph: join consecutive non-empty non-block lines.
		para := []string{ln}
		i++
		for i < len(lines) && strings.TrimSpace(lines[i]) != "" &&
			!reHeading.MatchString(lines[i]) &&
			!reFence.MatchString(lines[i]) &&
			!reHR.MatchString(lines[i]) &&
			!reBullet.MatchString(lines[i]) &&
			!reOrdered.MatchString(lines[i]) &&
			!reQuote.MatchString(lines[i]) {
			para = append(para, lines[i])
			i++
		}
		joined := strings.Join(para, " ")
		text := renderInline(joined, theme)
		for _, w := range core.WrapAnsi(text, width) {
			out = append(out, core.PadToWidth(w, width))
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Inline formatting
// ---------------------------------------------------------------------------

func renderInline(s string, t MarkdownTheme) string {
	s = reInlineCode.ReplaceAllStringFunc(s, func(m string) string {
		inner := strings.Trim(m, "`")
		return t.CodeInlineFn(inner)
	})
	s = reInlineBold.ReplaceAllStringFunc(s, func(m string) string {
		inner := strings.Trim(m, "*_")
		return t.StrongFn(inner)
	})
	s = reInlineStrike.ReplaceAllStringFunc(s, func(m string) string {
		inner := strings.Trim(m, "~")
		return t.StrikeFn(inner)
	})
	s = reInlineItalic.ReplaceAllStringFunc(s, func(m string) string {
		inner := strings.Trim(m, "*_")
		return t.EmphasisFn(inner)
	})
	s = reInlineLink.ReplaceAllStringFunc(s, func(m string) string {
		sub := reInlineLink.FindStringSubmatch(m)
		if len(sub) < 3 {
			return m
		}
		return t.LinkLabelFn(sub[1]) + " " + t.LinkURLFn("("+sub[2]+")")
	})
	return s
}

// ---------------------------------------------------------------------------
// Table rendering
// ---------------------------------------------------------------------------

func renderTable(rows []string, width int64, t MarkdownTheme) []string {
	if len(rows) < 2 {
		return nil
	}
	parse := func(r string) []string {
		r = strings.TrimSpace(r)
		r = strings.TrimPrefix(r, "|")
		r = strings.TrimSuffix(r, "|")
		parts := strings.Split(r, "|")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		return parts
	}
	header := parse(rows[0])
	body := make([][]string, 0, len(rows)-2)
	for _, r := range rows[2:] {
		body = append(body, parse(r))
	}
	cols := len(header)

	// Column widths
	colW := make([]int64, cols)
	for i, h := range header {
		colW[i] = core.VisibleWidth(h)
	}
	for _, r := range body {
		for i := 0; i < cols && i < len(r); i++ {
			if w := core.VisibleWidth(r[i]); w > colW[i] {
				colW[i] = w
			}
		}
	}
	// Squeeze to fit viewport.
	total := int64(cols)*3 + 1
	for _, w := range colW {
		total += w
	}
	if total > width {
		excess := total - width
		for excess > 0 {
			idx := 0
			for i := range colW {
				if colW[i] > colW[idx] {
					idx = i
				}
			}
			if colW[idx] <= 3 {
				break
			}
			colW[idx]--
			excess--
		}
	}

	sep := func() string {
		var b strings.Builder
		b.WriteString("+")
		for _, w := range colW {
			b.WriteString(strings.Repeat("-", int(w)+2))
			b.WriteString("+")
		}
		return t.TableBorderFn(b.String())
	}
	row := func(cells []string, headerRow bool) string {
		var b strings.Builder
		b.WriteString(t.TableBorderFn("|"))
		for i, w := range colW {
			cell := ""
			if i < len(cells) {
				cell = cells[i]
			}
			if !headerRow {
				cell = renderInline(cell, t)
			}
			padded := core.PadToWidth(core.TruncateToWidth(cell, w, "…"), w)
			if headerRow {
				padded = t.TableHeaderFn(padded)
			}
			b.WriteString(" ")
			b.WriteString(padded)
			b.WriteString(" ")
			b.WriteString(t.TableBorderFn("|"))
		}
		return b.String()
	}
	out := []string{
		core.PadToWidth(sep(), width),
		core.PadToWidth(row(header, true), width),
		core.PadToWidth(sep(), width),
	}
	for _, r := range body {
		out = append(out, core.PadToWidth(row(r, false), width))
	}
	out = append(out, core.PadToWidth(sep(), width))
	return out
}

// ---------------------------------------------------------------------------
// Default theme
// ---------------------------------------------------------------------------

// DefaultMarkdownTheme returns the built-in markdown theme used when no
// custom theme is set.
func DefaultMarkdownTheme() MarkdownTheme { return defaultMarkdownTheme() }

func defaultMarkdownTheme() MarkdownTheme {
	p := apitheme.CurrentPalette()
	sem := p.Semantic
	mode := p.Mode
	h := func(s string) string {
		return apitheme.SemStyle(sem.MdHeading, mode).Bold().Render(s)
	}
	return MarkdownTheme{
		HeadingFn:     [6]func(string) string{h, h, h, h, h, h},
		EmphasisFn:    apitheme.NewStyle().Italic().Render,
		StrongFn:      apitheme.NewStyle().Bold().Render,
		StrikeFn:      apitheme.NewStyle().Strike().Render,
		CodeInlineFn:  apitheme.SemStyle(sem.MdCode, mode).Render,
		CodeBlockFn:   apitheme.SemStyle(sem.MdCodeBlock, mode).Render,
		CodeFenceFn:   apitheme.SemStyle(sem.MdCodeBlockBorder, mode).Render,
		QuoteFn:       apitheme.SemStyle(sem.MdQuote, mode).Render,
		LinkLabelFn:   apitheme.SemStyle(sem.MdLink, mode).Underline().Render,
		LinkURLFn:     apitheme.SemStyle(sem.MdLinkUrl, mode).Render,
		HRFn:          apitheme.SemStyle(sem.MdHr, mode).Render,
		ListBulletFn:  apitheme.SemStyle(sem.MdListBullet, mode).Render,
		TableBorderFn: apitheme.SemStyle(sem.MdCodeBlockBorder, mode).Render,
		TableHeaderFn: apitheme.NewStyle().Bold().Render,
	}
}

func mergeMarkdownTheme(t MarkdownTheme) MarkdownTheme {
	d := defaultMarkdownTheme()
	if t.EmphasisFn != nil {
		d.EmphasisFn = t.EmphasisFn
	}
	if t.StrongFn != nil {
		d.StrongFn = t.StrongFn
	}
	if t.StrikeFn != nil {
		d.StrikeFn = t.StrikeFn
	}
	if t.CodeInlineFn != nil {
		d.CodeInlineFn = t.CodeInlineFn
	}
	if t.CodeBlockFn != nil {
		d.CodeBlockFn = t.CodeBlockFn
	}
	if t.CodeFenceFn != nil {
		d.CodeFenceFn = t.CodeFenceFn
	}
	if t.QuoteFn != nil {
		d.QuoteFn = t.QuoteFn
	}
	if t.LinkLabelFn != nil {
		d.LinkLabelFn = t.LinkLabelFn
	}
	if t.LinkURLFn != nil {
		d.LinkURLFn = t.LinkURLFn
	}
	if t.HRFn != nil {
		d.HRFn = t.HRFn
	}
	if t.ListBulletFn != nil {
		d.ListBulletFn = t.ListBulletFn
	}
	if t.TableBorderFn != nil {
		d.TableBorderFn = t.TableBorderFn
	}
	if t.TableHeaderFn != nil {
		d.TableHeaderFn = t.TableHeaderFn
	}
	for i, fn := range t.HeadingFn {
		if fn != nil {
			d.HeadingFn[i] = fn
		}
	}
	return d
}
