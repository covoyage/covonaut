package component

import (
	"sync"

	"github.com/covoyage/covonaut/tui/core"
)

// ---------------------------------------------------------------------------
// Text — multi-line text with word wrapping.
// ---------------------------------------------------------------------------

// Text displays multi-line text, word-wrapped to the viewport width.
// Optional BgFn lets callers apply a background colour (e.g. chalk.bgGray).
type Text struct {
	mu sync.RWMutex

	text     string
	paddingX int64
	paddingY int64
	bgFn     func(string) string

	cacheWidth int64
	cacheLines []string
	dirty      bool
}

// NewText creates a Text component.
func NewText(text string) *Text {
	return &Text{text: text, paddingX: 0, paddingY: 0, dirty: true}
}

// SetText replaces the content and invalidates the cache.
func (t *Text) SetText(s string) {
	t.mu.Lock()
	t.text = s
	t.dirty = true
	t.mu.Unlock()
}

// GetText returns the current content.
func (t *Text) GetText() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.text
}

// SetPadding sets horizontal/vertical padding.
func (t *Text) SetPadding(x, y int64) {
	t.mu.Lock()
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	t.paddingX = x
	t.paddingY = y
	t.dirty = true
	t.mu.Unlock()
}

// SetBgFn installs an optional background renderer.
func (t *Text) SetBgFn(fn func(string) string) {
	t.mu.Lock()
	t.bgFn = fn
	t.dirty = true
	t.mu.Unlock()
}

// Render wraps the text to fit within (width - 2*paddingX) and applies
// padding/background.
func (t *Text) Render(width int64) []string {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.dirty && t.cacheWidth == width && t.cacheLines != nil {
		return t.cacheLines
	}

	inner := width - 2*t.paddingX
	if inner < 1 {
		inner = 1
	}

	wrapped := core.WrapAnsi(t.text, inner)

	padX := ""
	if t.paddingX > 0 {
		padX = repeatSpace(t.paddingX)
	}

	out := make([]string, 0, int(t.paddingY*2)+len(wrapped))
	for i := int64(0); i < t.paddingY; i++ {
		out = append(out, applyBg(core.PadToWidth("", width), t.bgFn))
	}
	for _, ln := range wrapped {
		line := padX + ln
		line = core.PadToWidth(line, width)
		out = append(out, applyBg(line, t.bgFn))
	}
	for i := int64(0); i < t.paddingY; i++ {
		out = append(out, applyBg(core.PadToWidth("", width), t.bgFn))
	}

	t.cacheLines = out
	t.cacheWidth = width
	t.dirty = false
	return out
}

func (t *Text) Invalidate() {
	t.mu.Lock()
	t.dirty = true
	t.cacheLines = nil
	t.mu.Unlock()
}

func (t *Text) Update(msg core.Msg) core.Cmd { return nil }

// ---------------------------------------------------------------------------
// TruncatedText — single-line text that truncates to width.
// ---------------------------------------------------------------------------

// TruncatedText renders a single line, truncating with ellipsis to fit.
type TruncatedText struct {
	mu       sync.RWMutex
	text     string
	paddingX int64
	paddingY int64
	ellipsis string
}

// NewTruncatedText creates a single-line truncated text component.
func NewTruncatedText(text string) *TruncatedText {
	return &TruncatedText{text: text, ellipsis: "…"}
}

// SetText replaces the content.
func (t *TruncatedText) SetText(s string) {
	t.mu.Lock()
	t.text = s
	t.mu.Unlock()
}

// SetPadding sets horizontal/vertical padding.
func (t *TruncatedText) SetPadding(x, y int64) {
	t.mu.Lock()
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	t.paddingX = x
	t.paddingY = y
	t.mu.Unlock()
}

// SetEllipsis changes the truncation marker (default "…"). Pass "" to disable.
func (t *TruncatedText) SetEllipsis(s string) {
	t.mu.Lock()
	t.ellipsis = s
	t.mu.Unlock()
}

// Render truncates to (width - 2*paddingX) and applies padding.
func (t *TruncatedText) Render(width int64) []string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	inner := width - 2*t.paddingX
	if inner < 1 {
		inner = 1
	}

	padX := ""
	if t.paddingX > 0 {
		padX = repeatSpace(t.paddingX)
	}

	trunc := core.TruncateToWidth(t.text, inner, t.ellipsis)
	line := core.PadToWidth(padX+trunc, width)

	out := make([]string, 0, int(t.paddingY*2)+1)
	for i := int64(0); i < t.paddingY; i++ {
		out = append(out, core.PadToWidth("", width))
	}
	out = append(out, line)
	for i := int64(0); i < t.paddingY; i++ {
		out = append(out, core.PadToWidth("", width))
	}
	return out
}

func (t *TruncatedText) Invalidate() {}

func (t *TruncatedText) Update(msg core.Msg) core.Cmd { return nil }

// ---------------------------------------------------------------------------
// Spacer — empty lines for vertical spacing.
// ---------------------------------------------------------------------------

// Spacer emits N empty lines.
type Spacer struct {
	mu   sync.RWMutex
	rows int64
}

// NewSpacer returns a spacer of `rows` empty lines (minimum 1).
func NewSpacer(rows int64) *Spacer {
	if rows < 1 {
		rows = 1
	}
	return &Spacer{rows: rows}
}

// SetRows updates the spacer height.
func (s *Spacer) SetRows(n int64) {
	s.mu.Lock()
	if n < 0 {
		n = 0
	}
	s.rows = n
	s.mu.Unlock()
}

// Render emits s.rows blank lines of the given width.
func (s *Spacer) Render(width int64) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return core.FillLines(s.rows, width)
}

func (s *Spacer) Invalidate() {}

func (s *Spacer) Update(msg core.Msg) core.Cmd { return nil }

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func applyBg(line string, fn func(string) string) string {
	if fn == nil {
		return line
	}
	return fn(line)
}

func repeatSpace(n int64) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = ' '
	}
	return string(b)
}
