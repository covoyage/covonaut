package core

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// SGR parser
// ---------------------------------------------------------------------------

func TestParseSGRBasicAttrs(t *testing.T) {
	s := ParseSGR("1", DefaultStyle)
	if s.Attrs&AttrBold == 0 {
		t.Fatal("bold not set")
	}
	s = ParseSGR("1;3;4", DefaultStyle)
	if s.Attrs&AttrBold == 0 || s.Attrs&AttrItalic == 0 || s.Attrs&AttrUnderline == 0 {
		t.Fatalf("attrs = %v, want bold|italic|underline", s.Attrs)
	}
}

func TestParseSGRReset(t *testing.T) {
	s := ParseSGR("1;31", DefaultStyle)
	s = ParseSGR("0", s)
	if !s.Equal(DefaultStyle) {
		t.Fatalf("after reset, style = %+v, want default", s)
	}
}

func TestParseSGRClear(t *testing.T) {
	s := ParseSGR("1;2;3;4", DefaultStyle)
	s = ParseSGR("22", s)
	if s.Attrs&AttrBold != 0 || s.Attrs&AttrDim != 0 {
		t.Fatalf("bold/dim should be cleared: %v", s.Attrs)
	}
	if s.Attrs&AttrItalic == 0 || s.Attrs&AttrUnderline == 0 {
		t.Fatalf("italic/underline should remain: %v", s.Attrs)
	}
}

func TestParseSGRStandardColors(t *testing.T) {
	s := ParseSGR("31;42", DefaultStyle)
	if !s.Fg.IsPalette() || s.Fg.PaletteIndex() != 1 {
		t.Fatalf("fg = %+v, want palette 1", s.Fg)
	}
	if !s.Bg.IsPalette() || s.Bg.PaletteIndex() != 2 {
		t.Fatalf("bg = %+v, want palette 2", s.Bg)
	}
}

func TestParseSGRBrightColors(t *testing.T) {
	s := ParseSGR("91;102", DefaultStyle)
	if s.Fg.PaletteIndex() != 9 {
		t.Fatalf("fg = %+v, want palette 9", s.Fg)
	}
	if s.Bg.PaletteIndex() != 10 {
		t.Fatalf("bg = %+v, want palette 10", s.Bg)
	}
}

func TestParseSGR256(t *testing.T) {
	s := ParseSGR("38;5;200", DefaultStyle)
	if !s.Fg.IsPalette() || s.Fg.PaletteIndex() != 200 {
		t.Fatalf("fg = %+v, want palette 200", s.Fg)
	}
}

func TestParseSGRTruecolor(t *testing.T) {
	s := ParseSGR("38;2;255;128;0", DefaultStyle)
	if !s.Fg.IsRGB() {
		t.Fatalf("fg = %+v, want RGB", s.Fg)
	}
	r, g, b := s.Fg.RGBComponents()
	if r != 255 || g != 128 || b != 0 {
		t.Fatalf("rgb = %d,%d,%d, want 255,128,0", r, g, b)
	}
}

func TestParseSGRTruecolorColon(t *testing.T) {
	// Colon form should parse identically to semicolon form.
	s := ParseSGR("38:2::255:128:0", DefaultStyle)
	if !s.Fg.IsRGB() {
		t.Fatalf("colon form fg = %+v, want RGB", s.Fg)
	}
	r, g, b := s.Fg.RGBComponents()
	if r != 255 || g != 128 || b != 0 {
		t.Fatalf("colon rgb = %d,%d,%d, want 255,128,0", r, g, b)
	}
}

func TestParseSGRDefaultColors(t *testing.T) {
	s := ParseSGR("31;42;1", DefaultStyle)
	s = ParseSGR("39;49", s)
	if !s.Fg.IsDefault() || !s.Bg.IsDefault() {
		t.Fatalf("expected default colors: %+v", s)
	}
	if s.Attrs&AttrBold == 0 {
		t.Fatal("bold should remain after color reset")
	}
}

func TestParseSGREmpty(t *testing.T) {
	// ESC[m is equivalent to ESC[0m.
	s := ParseSGR("1;31", DefaultStyle)
	s = ParseSGR("", s)
	if !s.Equal(DefaultStyle) {
		t.Fatalf("empty SGR should reset, got %+v", s)
	}
}

// ---------------------------------------------------------------------------
// SGR renderer
// ---------------------------------------------------------------------------

func TestRenderSGRNoChange(t *testing.T) {
	if got := RenderSGR(DefaultStyle, DefaultStyle); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
	s := ParseSGR("1;31", DefaultStyle)
	if got := RenderSGR(s, s); got != "" {
		t.Fatalf("expected empty for equal styles, got %q", got)
	}
}

func TestRenderSGRResetToDefault(t *testing.T) {
	s := ParseSGR("1;31", DefaultStyle)
	got := RenderSGR(s, DefaultStyle)
	if got != "\x1b[0m" {
		t.Fatalf("reset to default = %q, want %q", got, "\x1b[0m")
	}
}

func TestRenderSGRAttrOn(t *testing.T) {
	got := RenderSGR(DefaultStyle, ParseSGR("1;3", DefaultStyle))
	if !strings.Contains(got, "1") || !strings.Contains(got, "3") {
		t.Fatalf("expected 1 and 3 in %q", got)
	}
	if strings.Contains(got, "22") {
		t.Fatalf("should not contain clear codes: %q", got)
	}
}

func TestRenderSGRAttrOff(t *testing.T) {
	from := ParseSGR("1;3", DefaultStyle)
	to := ParseSGR("1", DefaultStyle) // italic cleared
	got := RenderSGR(from, to)
	if !strings.Contains(got, "23") {
		t.Fatalf("should contain italic-clear (23): %q", got)
	}
	if strings.Contains(got, "22") {
		t.Fatalf("should not contain bold-clear (22): %q", got)
	}
}

// ---------------------------------------------------------------------------
// ParseLine — the bridge between string API and cell model
// ---------------------------------------------------------------------------

func TestParseLinePlain(t *testing.T) {
	row := ParseLine("hello")
	if row.IsRaw() {
		t.Fatal("plain line should not be raw")
	}
	if row.VisibleWidth() != 5 {
		t.Fatalf("width = %d, want 5", row.VisibleWidth())
	}
	if len(row.Cells) != 5 {
		t.Fatalf("cell count = %d, want 5", len(row.Cells))
	}
	if row.Cells[0].Rune != 'h' {
		t.Fatalf("first rune = %q, want 'h'", row.Cells[0].Rune)
	}
}

func TestParseLineWideChar(t *testing.T) {
	row := ParseLine("中")
	if row.VisibleWidth() != 2 {
		t.Fatalf("width = %d, want 2", row.VisibleWidth())
	}
	if len(row.Cells) != 2 {
		t.Fatalf("cell count = %d, want 2 (primary + continuation)", len(row.Cells))
	}
	if row.Cells[0].Rune != '中' || row.Cells[0].Width != 2 {
		t.Fatalf("primary cell = %+v, want 中/width2", row.Cells[0])
	}
	if !row.Cells[1].IsContinuation() {
		t.Fatalf("second cell = %+v, want continuation", row.Cells[1])
	}
}

func TestParseLineWideCharMixed(t *testing.T) {
	// "中a文" → 中(2) + cont(0) + a(1) + 文(2) + cont(0) = 5 cells, width 5
	row := ParseLine("中a文")
	if row.VisibleWidth() != 5 {
		t.Fatalf("width = %d, want 5", row.VisibleWidth())
	}
	if len(row.Cells) != 5 {
		t.Fatalf("cell count = %d, want 5", len(row.Cells))
	}
}

func TestParseLineCombiningMark(t *testing.T) {
	// "e" + combining acute (U+0301) → one cell, combining attached.
	row := ParseLine("e\u0301")
	if len(row.Cells) != 1 {
		t.Fatalf("cell count = %d, want 1", len(row.Cells))
	}
	if row.Cells[0].Rune != 'e' {
		t.Fatalf("base rune = %q, want 'e'", row.Cells[0].Rune)
	}
	if len(row.Cells[0].Combining) != 1 || row.Cells[0].Combining[0] != 0x0301 {
		t.Fatalf("combining = %v, want [U+0301]", row.Cells[0].Combining)
	}
	if row.VisibleWidth() != 1 {
		t.Fatalf("width = %d, want 1 (combining is zero-width)", row.VisibleWidth())
	}
}

func TestParseLineSGR(t *testing.T) {
	row := ParseLine("\x1b[31mred\x1b[0mplain")
	if len(row.Cells) != 8 {
		t.Fatalf("cell count = %d, want 8 (3 red + 5 plain)", len(row.Cells))
	}
	// First 3 cells should have red fg.
	if !row.Cells[0].Style.Fg.IsPalette() || row.Cells[0].Style.Fg.PaletteIndex() != 1 {
		t.Fatalf("cell 0 fg = %+v, want palette 1 (red)", row.Cells[0].Style.Fg)
	}
	// Last 5 cells should be default.
	if !row.Cells[3].Style.Fg.IsDefault() {
		t.Fatalf("cell 3 fg = %+v, want default", row.Cells[3].Style.Fg)
	}
}

func TestParseLineCursorMarker(t *testing.T) {
	row := ParseLine("ab" + CURSOR_MARKER + "cd")
	if row.CursorCol != 2 {
		t.Fatalf("cursor col = %d, want 2", row.CursorCol)
	}
	// Marker should be stripped from cell content.
	if row.VisibleWidth() != 4 {
		t.Fatalf("width = %d, want 4 (marker stripped)", row.VisibleWidth())
	}
}

func TestParseLineRawFallbackForCSI(t *testing.T) {
	// Non-SGR CSI sequences (cursor positioning) can't be cell-represented.
	row := ParseLine("\x1b[5Amove")
	if !row.IsRaw() {
		t.Fatal("expected raw fallback for non-SGR CSI")
	}
}

func TestParseLineRawFallbackForAPC(t *testing.T) {
	// Kitty graphics APC — must fall back to Raw.
	row := ParseLine("\x1b_Ga=q\x1b\\trailing")
	if !row.IsRaw() {
		t.Fatal("expected raw fallback for APC")
	}
	if !strings.Contains(row.Raw, "_G") {
		t.Fatalf("raw should preserve APC content: %q", row.Raw)
	}
}

// ---------------------------------------------------------------------------
// SerializeRow — round-trip and optimisation
// ---------------------------------------------------------------------------

func TestSerializeRowPlain(t *testing.T) {
	row := ParseLine("hello")
	if got := SerializeRow(row); got != "hello" {
		t.Fatalf("serialize plain = %q, want %q", got, "hello")
	}
}

func TestSerializeRowWideChar(t *testing.T) {
	row := ParseLine("中a文")
	got := SerializeRow(row)
	if got != "中a文" {
		t.Fatalf("serialize wide = %q, want %q", got, "中a文")
	}
}

func TestSerializeRowCombining(t *testing.T) {
	row := ParseLine("e\u0301")
	got := SerializeRow(row)
	if got != "e\u0301" {
		t.Fatalf("serialize combining = %q, want %q", got, "e\u0301")
	}
}

func TestSerializeRowSGR(t *testing.T) {
	// Round-trip: parse a styled string, serialize, parse again — cells match.
	orig := "\x1b[31mred\x1b[0mplain"
	row1 := ParseLine(orig)
	ser := SerializeRow(row1)
	row2 := ParseLine(ser)
	if !RowsEqual(row1, row2) {
		t.Fatalf("round-trip mismatch:\n orig=%q\n ser =%q\n row1=%+v\n row2=%+v", orig, ser, row1, row2)
	}
}

func TestSerializeRowSGROptimiser(t *testing.T) {
	// Two adjacent cells with the same style should only emit SGR once.
	row := ParseLine("\x1b[31mabc\x1b[0m")
	ser := SerializeRow(row)
	// ParseSGR canonicalises 31 → Palette(1); fgCode emits it as "38;5;1".
	// Both forms are semantically identical; we only assert that the SGR
	// is emitted exactly once (not repeated per-cell) and reset once.
	if strings.Count(ser, "38;5;1") != 1 {
		t.Fatalf("expected one SGR-on (38;5;1), got %q", ser)
	}
	if strings.Count(ser, "\x1b[0m") != 1 {
		t.Fatalf("expected one reset, got %q", ser)
	}
}

func TestSerializeRowResetsTrailingStyle(t *testing.T) {
	// A row ending in non-default style must emit a trailing reset to avoid
	// leaking into the next line.
	row := ParseLine("\x1b[31mred")
	ser := SerializeRow(row)
	if !strings.HasSuffix(ser, "\x1b[0m") {
		t.Fatalf("expected trailing reset, got %q", ser)
	}
}

// ---------------------------------------------------------------------------
// DiffRows
// ---------------------------------------------------------------------------

func TestDiffRowsNoChange(t *testing.T) {
	old := []Row{ParseLine("a"), ParseLine("b")}
	new := []Row{ParseLine("a"), ParseLine("b")}
	if diff := DiffRows(old, new); len(diff) != 0 {
		t.Fatalf("expected no diff, got %d rows", len(diff))
	}
}

func TestDiffRowsChange(t *testing.T) {
	old := []Row{ParseLine("a"), ParseLine("b"), ParseLine("c")}
	new := []Row{ParseLine("a"), ParseLine("B"), ParseLine("c")}
	diff := DiffRows(old, new)
	if len(diff) != 1 {
		t.Fatalf("expected 1 diff, got %d", len(diff))
	}
	if diff[0].Row != 1 {
		t.Fatalf("diff row = %d, want 1", diff[0].Row)
	}
}

func TestDiffRowsGrown(t *testing.T) {
	old := []Row{ParseLine("a")}
	new := []Row{ParseLine("a"), ParseLine("b"), ParseLine("c")}
	diff := DiffRows(old, new)
	if len(diff) != 2 {
		t.Fatalf("expected 2 diffs (new rows), got %d", len(diff))
	}
}

func TestDiffRowsSGRCanonicalised(t *testing.T) {
	// Two strings that differ in SGR encoding but resolve to the same cell
	// style should be considered equal (no diff). This is the cell model's
	// key advantage over string diff.
	old := []Row{ParseLine("\x1b[31mred\x1b[0m")}
	new := []Row{ParseLine("\x1b[38;5;1mred\x1b[0m")}
	if diff := DiffRows(old, new); len(diff) != 0 {
		t.Fatalf("expected no diff for SGR-equivalent rows, got %d", len(diff))
	}
}

// ---------------------------------------------------------------------------
// PadRow
// ---------------------------------------------------------------------------

func TestPadRowShort(t *testing.T) {
	row := ParseLine("hi")
	padded := PadRow(row, 5, DefaultStyle)
	if padded.VisibleWidth() != 5 {
		t.Fatalf("width = %d, want 5", padded.VisibleWidth())
	}
	if len(padded.Cells) != 5 {
		t.Fatalf("cell count = %d, want 5", len(padded.Cells))
	}
	if padded.Cells[2].Rune != ' ' {
		t.Fatalf("pad cell rune = %q, want space", padded.Cells[2].Rune)
	}
}

func TestPadRowAlreadyWide(t *testing.T) {
	row := ParseLine("hello")
	padded := PadRow(row, 3, DefaultStyle)
	if padded.VisibleWidth() != 5 {
		t.Fatalf("width = %d, want 5 (no truncation)", padded.VisibleWidth())
	}
}

func TestPadRowWideChar(t *testing.T) {
	// "中" is 2 wide; pad to 4 → 2 trailing spaces.
	row := ParseLine("中")
	padded := PadRow(row, 4, DefaultStyle)
	if padded.VisibleWidth() != 4 {
		t.Fatalf("width = %d, want 4", padded.VisibleWidth())
	}
	if len(padded.Cells) != 4 {
		t.Fatalf("cell count = %d, want 4 (primary + cont + 2 spaces)", len(padded.Cells))
	}
}

// ---------------------------------------------------------------------------
// TruncateRow
// ---------------------------------------------------------------------------

func TestTruncateRowNoChange(t *testing.T) {
	row := ParseLine("hello")
	if got := TruncateRow(row, 10); !RowsEqual(got, row) {
		t.Fatalf("truncating a short row should be no-op")
	}
}

func TestTruncateRowNarrow(t *testing.T) {
	row := ParseLine("hello")
	got := TruncateRow(row, 3)
	if got.VisibleWidth() != 3 {
		t.Fatalf("width = %d, want 3", got.VisibleWidth())
	}
	if got.Cells[0].Rune != 'h' || got.Cells[2].Rune != 'l' {
		t.Fatalf("truncated content = %v, want h..l", got.Cells)
	}
}

func TestTruncateRowWideCharBoundary(t *testing.T) {
	// "中a" width 3. Truncate to 2: '中' fits exactly (width 2), 'a' dropped.
	row := ParseLine("中a")
	got := TruncateRow(row, 2)
	if got.VisibleWidth() != 2 {
		t.Fatalf("width = %d, want 2", got.VisibleWidth())
	}
	if got.Cells[0].Rune != '中' {
		t.Fatalf("expected 中 to survive, got %q", got.Cells[0].Rune)
	}
}

func TestTruncateRowWideCharSplit(t *testing.T) {
	// "ab中" width 4. Truncate to 3: 'a','b' fit (2 cols), '中' (width 2)
	// would overflow by 1 → replaced with a space to fill the column.
	row := ParseLine("ab中")
	got := TruncateRow(row, 3)
	if got.VisibleWidth() != 3 {
		t.Fatalf("width = %d, want 3", got.VisibleWidth())
	}
	// The third cell must be a space (the wide char couldn't fit).
	if got.Cells[2].Rune != ' ' {
		t.Fatalf("expected space filler for split wide char, got %q", got.Cells[2].Rune)
	}
	// '中' must NOT appear (dropped, not split).
	ser := SerializeRow(got)
	if strings.Contains(ser, "中") {
		t.Fatalf("wide char should be dropped, not split: %q", ser)
	}
}

func TestTruncateRowCursorColClamped(t *testing.T) {
	// Cursor at col 5; truncate to 3 → cursor clamped to 2.
	row := ParseLine("ab" + CURSOR_MARKER + "cd")
	row = TruncateRow(row, 3)
	if row.CursorCol != 2 {
		t.Fatalf("cursor col = %d, want 2 (clamped)", row.CursorCol)
	}
}
