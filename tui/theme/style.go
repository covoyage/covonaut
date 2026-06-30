package theme

import (
	"fmt"
	"os"
	"strings"

	"github.com/covoyage/covonaut/tui/terminal"
)

// ---------------------------------------------------------------------------
// ANSI escape codes — delegated to terminal package
// ---------------------------------------------------------------------------

// Esc and Reset re-export terminal constants for backward compatibility.
// New code should use terminal.Esc and terminal.Reset directly.
const (
	Esc   = terminal.Esc
	Reset = terminal.Reset
)

type Color int64

const (
	Black   Color = 30
	Red     Color = 31
	Green   Color = 32
	Yellow  Color = 33
	Blue    Color = 34
	Magenta Color = 35
	Cyan    Color = 36
	White   Color = 37
	Default Color = 39

	BrightBlack   Color = 90
	BrightRed     Color = 91
	BrightGreen   Color = 92
	BrightYellow  Color = 93
	BrightBlue    Color = 94
	BrightMagenta Color = 95
	BrightCyan    Color = 96
	BrightWhite   Color = 97
)

type Attr int64

const (
	Bold      Attr = 1
	Dim       Attr = 2
	Italic    Attr = 3
	Underline Attr = 4
	Blink     Attr = 5
	Reverse   Attr = 7
	Strike    Attr = 9
)

// Style describes a text style with foreground color, background color, and attributes.
type Style struct {
	fg       Color
	bg       Color
	attrs    []Attr
	fgParams string // CSI parameter segment, e.g. "38;2;r;g;b" or "38;5;n" (wins over fg)
	bgParams string // e.g. "48;2;r;g;b"
}

func NewStyle() Style { return Style{fg: Default, bg: Default} }

func (s Style) Fg(c Color) Style {
	s.fg = c
	s.fgParams = ""
	return s
}

// WithFgParams sets a truecolor / 256-color foreground (SGR parameter list without ESC/[).
func (s Style) WithFgParams(csiParams string) Style {
	s.fgParams = csiParams
	s.fg = Default
	return s
}

func (s Style) Bg(c Color) Style {
	s.bg = c + 10
	s.bgParams = ""
	return s
}

// WithBgParams sets a truecolor / 256-color background.
func (s Style) WithBgParams(csiParams string) Style {
	s.bgParams = csiParams
	s.bg = Default
	return s
}
func (s Style) Bold() Style      { s.attrs = append(s.attrs, Bold); return s }
func (s Style) Dim() Style       { s.attrs = append(s.attrs, Dim); return s }
func (s Style) Italic() Style    { s.attrs = append(s.attrs, Italic); return s }
func (s Style) Underline() Style { s.attrs = append(s.attrs, Underline); return s }
func (s Style) Strike() Style    { s.attrs = append(s.attrs, Strike); return s }

func (s Style) Render(text string) string {
	if !ColorEnabled() {
		return text
	}
	var parts []string
	for _, a := range s.attrs {
		parts = append(parts, fmt.Sprintf("%d", a))
	}
	if s.fgParams != "" {
		parts = append(parts, strings.Split(s.fgParams, ";")...)
	} else if s.fg != Default {
		parts = append(parts, fmt.Sprintf("%d", s.fg))
	}
	if s.bgParams != "" {
		parts = append(parts, strings.Split(s.bgParams, ";")...)
	} else if s.bg != Default {
		parts = append(parts, fmt.Sprintf("%d", s.bg))
	}
	if len(parts) == 0 {
		return text
	}
	return Esc + strings.Join(parts, ";") + "m" + text + Reset
}

// ---------------------------------------------------------------------------
// Predefined styles for agent UI (theme-aware via SyncPaletteGlobals)
//
// Deprecated: these global variables are not concurrency-safe and cannot
// support independent theme instances. Use CurrentPalette() instead.
// ---------------------------------------------------------------------------

var (
	StyleUser      Style
	StyleAssistant Style
	StyleSystem    Style
	StyleTool      Style
	StyleToolName  Style
	StyleError     Style
	StyleSuccess   Style
	StyleDim       Style
	StyleBold      Style
	StyleHandoff   Style
	StyleCode      Style
	StyleCodeBlock Style
	StyleUsage     Style
	StyleThinking  Style
)

// ---------------------------------------------------------------------------
// Color detection
// ---------------------------------------------------------------------------

var colorOverride *bool

func ForceColor(enabled bool) { colorOverride = &enabled }

func ColorEnabled() bool {
	if colorOverride != nil {
		return *colorOverride
	}
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("FORCE_COLOR") != "" {
		return true
	}
	if term := os.Getenv("TERM"); term == "dumb" {
		return false
	}
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// ---------------------------------------------------------------------------
// Unicode symbols
// ---------------------------------------------------------------------------

const (
	SymbolCheck    = "✓"
	SymbolCross    = "✗"
	SymbolArrow    = "→"
	SymbolBullet   = "•"
	SymbolDot      = "·"
	SymbolStar     = "★"
	SymbolWarning  = "⚠"
	SymbolInfo     = "ℹ"
	SymbolThinking = "◐"
	SymbolRight    = "▸"
	SymbolDown     = "▾"
)
