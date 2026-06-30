package core

// ---------------------------------------------------------------------------
// Cell-level rendering model.
//
// The cell model converts a rendered string into a 2D grid of Cells, where
// each Cell carries an absolute Style (fg/bg/attrs). This eliminates two
// classes of bugs the string model has:
//
//  1. Wide-char truncation: splicing an overlay onto "中xx" at column 1 used
//     to drop '中' entirely (SliceByColumn bails on a wide char straddling
//     the boundary). A Cell grid knows (0,1) is the right half of '中' and
//     can either keep or overwrite it as a unit.
//  2. Style state loss: SGR codes are a stream, so slicing a styled region
//     used to lose the active color/bold. Cells carry absolute styles, so
//     slicing is just a copy.
//
// Component authors keep the []string API. The engine parses strings into
// cells internally, composes overlays at cell level, diffs at cell level,
// and re-serializes with SGR optimization (only emit the SGR bits that
// differ from the previous cell).
// ---------------------------------------------------------------------------

// Attrs is a bitmask of SGR text attributes.
type Attrs uint8

const (
	AttrBold          Attrs = 1 << iota
	AttrDim                // 2 / faint
	AttrItalic             // 3
	AttrUnderline          // 4
	AttrReverse            // 7
	AttrHidden             // 8 (concealed)
	AttrStrike             // 9
)

// Color is a 24-bit RGB color packed into uint32. The high byte is a tag:
//
//	0x00_RR_GG_BB  RGB truecolor (tag 0)
//	0x01_00_00_NN  256-color palette index (tag 1)
//	0xFF_00_00_00  default color (no SGR emitted for this channel)
//
// This packing lets Serialize emit `38;5;n` vs `38;2;r;g;b` vs nothing.
type Color uint32

const (
	ColorDefault      Color = 0xFF000000
	colorPaletteTag   Color = 0x01000000
	colorRGBMask      Color = 0x00FFFFFF
	paletteIndexMask  Color = 0x000000FF
)

// RGB constructs a truecolor Color from r/g/b in [0,255].
func RGB(r, g, b uint8) Color {
	return Color(uint32(r)<<16 | uint32(g)<<8 | uint32(b))
}

// Palette constructs a 256-color palette index Color.
func Palette(n uint8) Color {
	return colorPaletteTag | Color(n)
}

// IsDefault reports whether c is the terminal default for that channel.
func (c Color) IsDefault() bool { return c == ColorDefault }

// IsPalette reports whether c is a 256-color palette entry.
func (c Color) IsPalette() bool { return c&0xFF000000 == colorPaletteTag }

// IsRGB reports whether c is a 24-bit truecolor value.
func (c Color) IsRGB() bool { return c != ColorDefault && c&0xFF000000 == 0 }

// RGBComponents returns r/g/b. Only valid when IsRGB().
func (c Color) RGBComponents() (r, g, b uint8) {
	return uint8(c >> 16), uint8(c >> 8), uint8(c)
}

// PaletteIndex returns the 256-color index. Only valid when IsPalette().
func (c Color) PaletteIndex() uint8 { return uint8(c & paletteIndexMask) }

// Style is the absolute rendering state of a cell.
type Style struct {
	Fg    Color
	Bg    Color
	Attrs Attrs
}

// DefaultStyle is the terminal's default style (no SGR active).
var DefaultStyle = Style{Fg: ColorDefault, Bg: ColorDefault}

// Equal reports whether two styles are identical.
func (s Style) Equal(o Style) bool {
	return s.Fg == o.Fg && s.Bg == o.Bg && s.Attrs == o.Attrs
}

// Cell is a single terminal cell.
//
// A wide rune (width 2) occupies two Cells: the left cell carries the rune
// with Width=2; the right cell is a continuation with Rune=0 and Width=0.
// This layout makes column access O(1): row[col] is always the cell at that
// visible column.
type Cell struct {
	Rune      rune   // primary rune; 0 if this is a wide-char continuation
	Combining []rune // trailing combining marks (width 0) attached to Rune
	Width     int8   // 1 (narrow), 2 (wide), 0 (continuation of a wide char)
	Style     Style
}

// IsContinuation reports whether this cell is the right half of a wide char.
func (c Cell) IsContinuation() bool { return c.Width == 0 && c.Rune == 0 }

// EqualCell compares two cells for rendering equivalence (style + content).
func EqualCell(a, b Cell) bool {
	if a.Width != b.Width || a.Rune != b.Rune || !a.Style.Equal(b.Style) {
		return false
	}
	if len(a.Combining) != len(b.Combining) {
		return false
	}
	for i := range a.Combining {
		if a.Combining[i] != b.Combining[i] {
			return false
		}
	}
	return true
}

// Row is one terminal line in the cell model.
//
// A line is represented either by Cells (the common case) or by Raw (a
// fallback string emitted verbatim). Raw is used for lines the cell model
// cannot represent, such as Kitty graphics APC sequences: those carry
// binary-ish payloads that have no meaningful cell representation, so the
// engine passes them through untouched.
//
// CursorCol, when >= 0, marks the visible column at which an IME cursor
// marker (CURSOR_MARKER APC) was found. The marker itself is stripped from
// the cell content; the renderer uses CursorCol to drive the hardware cursor.
type Row struct {
	Cells     []Cell
	Raw       string // populated iff Cells is nil
	CursorCol int    // -1 when no IME cursor marker; column otherwise
}

// IsRaw reports whether the row is the opaque-string fallback form.
func (r Row) IsRaw() bool { return r.Cells == nil }

// VisibleWidth returns the number of terminal columns the row occupies.
// For raw rows it falls back to VisibleWidth on the string.
func (r Row) VisibleWidth() int64 {
	if r.IsRaw() {
		return VisibleWidth(r.Raw)
	}
	var w int64
	for _, c := range r.Cells {
		if c.Width > 0 {
			w += int64(c.Width)
		}
	}
	return w
}

// RowsEqual reports whether two Rows are rendering-equivalent.
// Two raw rows are equal iff their strings match; two cell rows are equal
// iff every cell matches; a raw row and a cell row are never equal.
func RowsEqual(a, b Row) bool {
	if a.IsRaw() || b.IsRaw() {
		return a.IsRaw() && b.IsRaw() && a.Raw == b.Raw && a.CursorCol == b.CursorCol
	}
	if a.CursorCol != b.CursorCol {
		return false
	}
	if len(a.Cells) != len(b.Cells) {
		return false
	}
	for i := range a.Cells {
		if !EqualCell(a.Cells[i], b.Cells[i]) {
			return false
		}
	}
	return true
}
