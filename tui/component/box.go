package component

import (
	"sync"

	"github.com/covoyage/covonaut/tui/core"
)

// ---------------------------------------------------------------------------
// Box — a container that pads and optionally frames its children.
//
// Two visual modes:
//   - Plain padding only (Border=nil): padding around children, optional bg.
//   - Framed (Border != nil): draws a Unicode border; title renders inline.
// ---------------------------------------------------------------------------

// BoxBorder selects the border decoration style.
type BoxBorder int64

const (
	BorderNone    BoxBorder = 0
	BorderRounded BoxBorder = 1 // ╭─╮ ╰─╯
	BorderSharp   BoxBorder = 2 // ┌─┐ └─┘
	BorderDouble  BoxBorder = 3 // ╔═╗ ╚═╝
)

// Box composes children vertically with padding and an optional border.
type Box struct {
	mu sync.RWMutex

	children []core.Component
	paddingX int64
	paddingY int64
	border   BoxBorder
	title    string
	bgFn     func(string) string
	borderFn func(string) string
}

// NewBox returns a new Box. Default: 1-cell padding, no border, no background.
func NewBox() *Box {
	return &Box{paddingX: 1, paddingY: 1}
}

// AddChild appends a component.
func (b *Box) AddChild(c core.Component) {
	if c == nil {
		return
	}
	b.mu.Lock()
	b.children = append(b.children, c)
	b.mu.Unlock()
}

// RemoveChild removes the first occurrence of c.
func (b *Box) RemoveChild(c core.Component) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, ch := range b.children {
		if ch == c {
			b.children = append(b.children[:i], b.children[i+1:]...)
			return true
		}
	}
	return false
}

// Clear removes all children.
func (b *Box) Clear() {
	b.mu.Lock()
	b.children = nil
	b.mu.Unlock()
}

// SetPadding sets inner padding in cells.
func (b *Box) SetPadding(x, y int64) {
	b.mu.Lock()
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	b.paddingX = x
	b.paddingY = y
	b.mu.Unlock()
}

// SetBorder selects the border style (BorderNone to remove).
func (b *Box) SetBorder(style BoxBorder) {
	b.mu.Lock()
	b.border = style
	b.mu.Unlock()
}

// SetTitle sets an optional title rendered inline with the top border.
func (b *Box) SetTitle(title string) {
	b.mu.Lock()
	b.title = title
	b.mu.Unlock()
}

// SetBgFn installs an optional background renderer for all lines.
func (b *Box) SetBgFn(fn func(string) string) {
	b.mu.Lock()
	b.bgFn = fn
	b.mu.Unlock()
}

// SetBorderFn installs an optional renderer applied to the border glyphs.
func (b *Box) SetBorderFn(fn func(string) string) {
	b.mu.Lock()
	b.borderFn = fn
	b.mu.Unlock()
}

// Render composes children inside padding/border/background.
func (b *Box) Render(width int64) []string {
	b.mu.RLock()
	children := make([]core.Component, len(b.children))
	copy(children, b.children)
	padX := b.paddingX
	padY := b.paddingY
	border := b.border
	title := b.title
	bgFn := b.bgFn
	bFn := b.borderFn
	b.mu.RUnlock()

	borderW := int64(0)
	if border != BorderNone {
		borderW = 1
	}
	inner := width - 2*(padX+borderW)
	if inner < 1 {
		inner = 1
	}

	var body []string
	for _, c := range children {
		body = append(body, c.Render(inner)...)
	}

	pad := repeatSpace(padX)

	var out []string

	// Top border (or padY blank lines).
	if border != BorderNone {
		out = append(out, renderTopBorder(width, border, title, bFn))
		for i := int64(0); i < padY; i++ {
			out = append(out, framedLine("", width, padX, border, bFn, bgFn))
		}
		for _, ln := range body {
			out = append(out, framedLine(ln, width, padX, border, bFn, bgFn))
		}
		for i := int64(0); i < padY; i++ {
			out = append(out, framedLine("", width, padX, border, bFn, bgFn))
		}
		out = append(out, renderBottomBorder(width, border, bFn))
	} else {
		for i := int64(0); i < padY; i++ {
			out = append(out, applyBg(core.PadToWidth("", width), bgFn))
		}
		for _, ln := range body {
			line := pad + ln
			line = core.PadToWidth(line, width)
			out = append(out, applyBg(line, bgFn))
		}
		for i := int64(0); i < padY; i++ {
			out = append(out, applyBg(core.PadToWidth("", width), bgFn))
		}
	}

	return out
}

// Invalidate fans out to children.
func (b *Box) Invalidate() {
	b.mu.RLock()
	children := make([]core.Component, len(b.children))
	copy(children, b.children)
	b.mu.RUnlock()
	for _, c := range children {
		c.Invalidate()
	}
}

func (b *Box) Update(msg core.Msg) core.Cmd {
	b.mu.RLock()
	children := make([]core.Component, len(b.children))
	copy(children, b.children)
	b.mu.RUnlock()
	for _, c := range children {
		if u, ok := c.(core.Updatable); ok {
			u.Update(msg)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Border helpers
// ---------------------------------------------------------------------------

type borderGlyphs struct {
	tl, tr, bl, br, h, v string
}

func glyphsFor(style BoxBorder) borderGlyphs {
	switch style {
	case BorderDouble:
		return borderGlyphs{tl: "╔", tr: "╗", bl: "╚", br: "╝", h: "═", v: "║"}
	case BorderSharp:
		return borderGlyphs{tl: "┌", tr: "┐", bl: "└", br: "┘", h: "─", v: "│"}
	default:
		return borderGlyphs{tl: "╭", tr: "╮", bl: "╰", br: "╯", h: "─", v: "│"}
	}
}

func renderTopBorder(width int64, style BoxBorder, title string, fn func(string) string) string {
	g := glyphsFor(style)
	if width < 2 {
		return applyBorderFn(g.tl, fn)
	}
	inner := width - 2
	body := ""
	if title != "" {
		titleStr := " " + title + " "
		tw := core.VisibleWidth(titleStr)
		if tw > inner {
			titleStr = core.TruncateToWidth(titleStr, inner, "…")
			tw = core.VisibleWidth(titleStr)
		}
		body = titleStr + repeatGlyph(g.h, inner-tw)
	} else {
		body = repeatGlyph(g.h, inner)
	}
	return applyBorderFn(g.tl, fn) + body + applyBorderFn(g.tr, fn)
}

func renderBottomBorder(width int64, style BoxBorder, fn func(string) string) string {
	g := glyphsFor(style)
	if width < 2 {
		return applyBorderFn(g.bl, fn)
	}
	return applyBorderFn(g.bl, fn) + repeatGlyph(g.h, width-2) + applyBorderFn(g.br, fn)
}

func framedLine(content string, width, padX int64, style BoxBorder, bFn, bgFn func(string) string) string {
	g := glyphsFor(style)
	inner := width - 2 - 2*padX
	if inner < 1 {
		inner = 1
	}
	trunc := core.TruncateToWidth(content, inner, "…")
	pad := repeatSpace(padX)
	body := core.PadToWidth(pad+trunc+pad, width-2)
	body = applyBg(body, bgFn)
	return applyBorderFn(g.v, bFn) + body + applyBorderFn(g.v, bFn)
}

func applyBorderFn(s string, fn func(string) string) string {
	if fn == nil {
		return s
	}
	return fn(s)
}

func repeatGlyph(glyph string, n int64) string {
	if n <= 0 {
		return ""
	}
	out := make([]byte, 0, len(glyph)*int(n))
	for i := int64(0); i < n; i++ {
		out = append(out, glyph...)
	}
	return string(out)
}
