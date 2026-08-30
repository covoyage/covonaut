package component

import (
	"net/url"
	"strings"
	"sync"
	"unicode"

	"github.com/covoyage/covonaut/tui/core"
	apitheme "github.com/covoyage/covonaut/tui/theme"
)

// RenderMarkdown parses source and renders it to width-padded terminal lines.
func RenderMarkdown(src string, width int64, theme MarkdownTheme) []string {
	return renderMarkdown(src, width, mergeMarkdownTheme(theme))
}

// ---------------------------------------------------------------------------
// Markdown — a block-level markdown renderer component.
//
// Parsing is CommonMark + GFM (tables, task lists, strikethrough, autolinks),
// plus footnotes, definition lists, and GitHub-style alerts.
// The parse tree is projected into a terminal-oriented block tree; rendering,
// syntax highlighting, mermaid fences, and images stay here.
// ---------------------------------------------------------------------------

// MarkdownTheme overrides the ANSI styling of rendered elements.
type MarkdownTheme struct {
	HeadingFn    [6]func(string) string // h1..h6
	EmphasisFn   func(string) string    // italic
	StrongFn     func(string) string    // bold
	StrikeFn     func(string) string
	MarkFn       func(string) string // ==highlight==
	CodeInlineFn func(string) string
	CodeBlockFn  func(string) string
	CodeFenceFn  func(string) string // language label line
	QuoteFn      func(string) string
	LinkLabelFn  func(string) string
	LinkURLFn    func(string) string
	// LinkRendererFn, when set, replaces the default label+URL rendering.
	// It receives the raw label and URL and returns the fully rendered link string.
	LinkRendererFn func(label, url string) string
	HRFn           func(string) string
	ListBulletFn   func(string) string
	TableBorderFn  func(string) string
	TableHeaderFn  func(string) string
	// Syntax, when set, is used to style fenced code blocks with a
	// language tag. A nil value falls back to CodeBlockFn.
	Syntax *SyntaxTheme
	// DisableSyntax turns off token-level syntax highlighting for fenced
	// code blocks (including diff fences), rendering them with the plain
	// code style. It is a caller-provided preference — the library default
	// keeps highlighting on.
	DisableSyntax bool
	// MathFn styles inline/block math after the Unicode rewrite.
	MathFn func(string) string
	// HighlightFence, when set, highlights a fenced body for `lang` and
	// returns ANSI text (newlines preserved). Empty string falls back to
	// the built-in tokenizer.
	HighlightFence func(source, lang string) string
	// FenceRenderer, when set, may fully replace a fenced block (e.g.
	// mermaid). Return nil to fall through to the default highlighter.
	FenceRenderer func(lang, source string, width int64) []string
	// ImageRenderer, when set, renders `![alt](url)` as terminal lines.
	// Return nil to fall through to a labelled OSC8 link.
	ImageRenderer func(alt, url string, width int64) []string
	// SkipIncomplete hides unclosed fences/math (headless streaming).
	// The interactive TUI leaves this false so partial blocks still show.
	SkipIncomplete bool
}

// syntaxThemeFromMarkdown bridges a MarkdownTheme into a SyntaxTheme so
// fenced code blocks can be highlighted by the Syntax tokenizer. Falls back
// to a palette derived from CodeBlockFn when Syntax is nil.
func syntaxThemeFromMarkdown(t MarkdownTheme) SyntaxTheme {
	var st SyntaxTheme
	if t.Syntax != nil {
		st = *t.Syntax
	} else {
		st = DefaultSyntaxTheme()
		if t.CodeBlockFn != nil {
			st.TextFn = t.CodeBlockFn
			st.PunctuationFn = t.CodeBlockFn
			st.OperatorFn = t.CodeBlockFn
		}
	}
	st.DisableSyntax = t.DisableSyntax
	return st
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
// Default theme
// ---------------------------------------------------------------------------

// DefaultMarkdownTheme returns the built-in markdown theme used when no
// custom theme is set.
func DefaultMarkdownTheme() MarkdownTheme { return defaultMarkdownTheme() }

// OSC8LinkRenderer returns a LinkRendererFn that wraps links with OSC8
// escape sequences, making them clickable in supported terminals
// (iTerm2, Hyper, Windows Terminal, kitty, etc.).
// Named links show the label only; autolinks (label == URL) stay visible.
// Network shares, automount paths, control/invisible characters, and
// script/data schemes stay plain text.
func OSC8LinkRenderer(labelFn, urlFn func(string) string) func(label, url string) string {
	return func(label, rawURL string) string {
		if rawURL == "" {
			return labelFn(label)
		}
		text := label
		if text == "" {
			text = rawURL
		}
		inner := labelFn(text)
		if urlFn != nil && text == rawURL {
			inner = urlFn(text)
		}
		if !safeHyperlinkURL(rawURL) {
			return inner
		}
		return "\x1b]8;;" + rawURL + "\x1b\\" + inner + "\x1b]8;;\x1b\\"
	}
}

func safeHyperlinkURL(raw string) bool {
	if raw == "" {
		return false
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f || unicode.Is(unicode.Cf, r) {
			return false
		}
	}
	if strings.HasPrefix(raw, `\\`) || strings.HasPrefix(raw, "//") {
		return false
	}
	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "/net/") || strings.HasPrefix(lower, "/smb/") || strings.HasPrefix(lower, "/afs/") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "smb", "afp", "nfs", "javascript", "vbscript", "data":
		return false
	case "file":
		host := strings.ToLower(u.Hostname())
		if host != "" && host != "localhost" && host != "127.0.0.1" && host != "::1" {
			return false
		}
	}
	return true
}

func defaultMarkdownTheme() MarkdownTheme {
	p := apitheme.CurrentPalette()
	sem := p.Semantic
	mode := p.Mode
	hColor := sem.MdHeading
	textColor := sem.Text
	if textColor == "" {
		textColor = sem.AssistantText
	}
	if textColor == "" {
		textColor = "#e0e0e0"
	}
	mutedColor := sem.Muted
	if mutedColor == "" {
		mutedColor = sem.Dim
	}
	h1 := apitheme.SemStyle(hColor, mode).Bold().Render
	h2 := apitheme.SemStyle(apitheme.MixHex(hColor, textColor, 0.22), mode).Bold().Render
	h3 := apitheme.SemStyle(apitheme.MixHex(hColor, textColor, 0.45), mode).Bold().Render
	h4 := apitheme.SemStyle(apitheme.MixHex(hColor, mutedColor, 0.35), mode).Render
	h5 := apitheme.SemStyle(apitheme.MixHex(hColor, mutedColor, 0.55), mode).Dim().Render
	h6 := apitheme.SemStyle(apitheme.MixHex(hColor, mutedColor, 0.72), mode).Dim().Italic().Render
	linkLabelFn := apitheme.SemStyle(sem.MdLink, mode).Underline().Render
	linkURLFn := apitheme.SemStyle(sem.MdLinkUrl, mode).Render
	mathFn := apitheme.SemStyle(sem.MdQuote, mode).Italic().Render
	return MarkdownTheme{
		HeadingFn:      [6]func(string) string{h1, h2, h3, h4, h5, h6},
		EmphasisFn:     apitheme.NewStyle().Italic().Render,
		StrongFn:       apitheme.NewStyle().Bold().Render,
		StrikeFn:       apitheme.NewStyle().Strike().Render,
		MarkFn:         apitheme.NewStyle().Reverse().Render,
		CodeInlineFn:   apitheme.SemStyle(sem.MdCode, mode).Render,
		CodeBlockFn:    apitheme.SemStyle(sem.MdCodeBlock, mode).Render,
		CodeFenceFn:    apitheme.SemStyle(sem.MdCodeBlockBorder, mode).Render,
		QuoteFn:        apitheme.SemStyle(sem.MdQuote, mode).Render,
		LinkLabelFn:    linkLabelFn,
		LinkURLFn:      linkURLFn,
		LinkRendererFn: OSC8LinkRenderer(linkLabelFn, linkURLFn),
		HRFn:           apitheme.SemStyle(sem.MdHr, mode).Render,
		ListBulletFn:   apitheme.SemStyle(sem.MdListBullet, mode).Render,
		TableBorderFn:  apitheme.SemStyle(sem.MdCodeBlockBorder, mode).Render,
		TableHeaderFn:  apitheme.NewStyle().Bold().Render,
		MathFn:         mathFn,
		ImageRenderer:  defaultImageRenderer,
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
	if t.MarkFn != nil {
		d.MarkFn = t.MarkFn
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
	if t.LinkRendererFn != nil {
		d.LinkRendererFn = t.LinkRendererFn
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
	if t.MathFn != nil {
		d.MathFn = t.MathFn
	}
	if t.HighlightFence != nil {
		d.HighlightFence = t.HighlightFence
	}
	if t.FenceRenderer != nil {
		d.FenceRenderer = t.FenceRenderer
	}
	if t.ImageRenderer != nil {
		d.ImageRenderer = t.ImageRenderer
	}
	for i, fn := range t.HeadingFn {
		if fn != nil {
			d.HeadingFn[i] = fn
		}
	}
	d.DisableSyntax = t.DisableSyntax
	d.SkipIncomplete = t.SkipIncomplete
	if t.Syntax != nil {
		d.Syntax = t.Syntax
	}
	return d
}

// defaultImageRenderer inlines local files / data URIs. Remote http(s) URLs
// stay as labelled links — the renderer never fetches the network.
func defaultImageRenderer(alt, url string, width int64) []string {
	if url == "" {
		return nil
	}
	lower := strings.ToLower(url)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return nil
	}
	var (
		img *Image
		err error
	)
	switch {
	case strings.HasPrefix(lower, "data:image/"):
		img, err = imageFromDataURI(url)
	case strings.HasPrefix(lower, "file://"):
		img, err = NewImageFromFile(strings.TrimPrefix(url, "file://"))
	default:
		img, err = NewImageFromFile(url)
	}
	if err != nil || img == nil {
		return nil
	}
	if width <= 0 {
		width = 40
	}
	maxH := int64(12)
	if width < 20 {
		maxH = 6
	}
	img.SetMaxSize(width, maxH)
	return img.Render(width)
}
