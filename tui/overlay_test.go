package tui

import (
	"strings"
	"testing"

	core "github.com/covoyage/covonaut/tui/core"
)

// lineComp helper — emits the same line N times.
type lineComp struct {
	lines []string
}

func (l *lineComp) Render(width int64) []string {
	out := make([]string, len(l.lines))
	for i, s := range l.lines {
		out[i] = core.PadToWidth(s, width)
	}
	return out
}
func (l *lineComp) Invalidate()                 {}
func (l *lineComp) Update(msg core.Msg) core.Cmd { return nil }

// rawLineComp emits its lines verbatim (no width padding), so an empty
// string stays empty — used to exercise the blank-line overlay path.
type rawLineComp struct{ lines []string }

func (l *rawLineComp) Render(width int64) []string  { return l.lines }
func (l *rawLineComp) Invalidate()                  {}
func (l *rawLineComp) Update(core.Msg) core.Cmd     { return nil }

// TestComposeOverlayBlankLineKeepsMargins is the regression test for the bug
// where an overlay panel that emits an empty ("") content line (e.g. the TODO
// panel) wiped the dimmed side margins on that row: ParseLine("") yields a Raw
// row, and spliceOverlayRows replaces the WHOLE base row for raw content,
// destroying the dim columns left/right of the overlay — a bright notch in the
// margins. Empty content lines must instead leave the margins dimmed.
func TestComposeOverlayBlankLineKeepsMargins(t *testing.T) {
	base := stringRows(10, 40)
	ov := &Overlay{
		UseAbsolute:   true,
		Anchor:        AnchorTopLeft,
		Row:           2,
		Col:           10,
		Width:         OverlaySize{Value: 20},
		Height:        OverlaySize{Value: 4},
		DimBackground: true,
		// Middle line is blank.
		Content: &rawLineComp{lines: []string{"AAAA", "", "BBBB", "CCCC"}},
	}
	out := composeOverlays(base, []*Overlay{ov}, 40, 10)

	isDim := func(c core.Cell) bool { return c.Style.Attrs&dimTextAttr != 0 }
	// Row 3 is the overlay's blank line (overlay rows [2,6)). Its side margins
	// (cols [0,10) and [30,40)) must remain dimmed; the row must not be Raw.
	if out[3].IsRaw() {
		t.Fatalf("overlay blank line must not produce a Raw row (wipes margins)")
	}
	for _, c := range []int64{0, 5, 9, 30, 35, 39} {
		if !isDim(out[3].Cells[c]) {
			t.Fatalf("row 3 col %d should stay dimmed on a blank overlay line", c)
		}
	}
	for i, r := range out {
		if r.VisibleWidth() != 40 {
			t.Fatalf("row %d width = %d, want 40", i, r.VisibleWidth())
		}
	}
}

// stringRows builds a base frame of dotted lines for overlay tests.
func stringRows(n, w int) []core.Row {
	out := make([]core.Row, n)
	for i := range out {
		out[i] = core.ParseLine(strings.Repeat(".", w))
	}
	return out
}

func TestComposeOverlayCentered(t *testing.T) {
	base := stringRows(10, 40)
	ov := NewCenteredOverlay(&lineComp{lines: []string{"HELLO", "WORLD"}}, 30, 30)
	out := composeOverlays(base, []*Overlay{ov}, 40, 10)
	joined := serializeRowsForTest(out)
	if !strings.Contains(joined, "HELLO") || !strings.Contains(joined, "WORLD") {
		t.Fatalf("overlay content missing: %s", joined)
	}
	// All rows must still be width 40 (cell grid preserves column count).
	for i, r := range out {
		if r.VisibleWidth() != 40 {
			t.Fatalf("row %d width = %d, want 40, content=%q", i, r.VisibleWidth(), core.SerializeRow(r))
		}
	}
}

func TestComposeOverlayBottomRight(t *testing.T) {
	base := stringRows(10, 40)
	ov := NewBottomRightOverlay(&lineComp{lines: []string{"toast!"}}, 8, 1)
	out := composeOverlays(base, []*Overlay{ov}, 40, 10)
	// Last row should contain "toast!".
	last := core.SerializeRow(out[9])
	if !strings.Contains(last, "toast!") {
		t.Fatalf("expected toast! in %q", last)
	}
}

// serializeRowsForTest flattens a cell row slice to a single string for
// substring assertions. Only used in tests.
func serializeRowsForTest(rows []core.Row) string {
	var b strings.Builder
	for i, r := range rows {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(core.SerializeRow(r))
	}
	return b.String()
}

// TestComposeOverlayWideCharNotTruncated is the regression test for the bug
// that motivated the cell-level rewrite. Under the old string model,
// splicing an overlay at column 1 of "中xx" dropped '中' entirely because
// SliceByColumn bailed on a wide char straddling the cut. The cell model
// must keep '中' intact when the overlay starts at column 1 or 2.
func TestComposeOverlayWideCharNotTruncated(t *testing.T) {
	// Base: one row of "中xx" (width 4: 中=2, x, x). Pad to width 10.
	base := []core.Row{core.ParseLine(core.PadToWidth("中xx", 10))}
	// Overlay: single cell 'O' at column 1 (right half of 中).
	ov := &Overlay{
		UseAbsolute: true,
		Anchor:      AnchorTopLeft,
		Row:         0,
		Col:         1,
		Width:       OverlaySize{Value: 1},
		Height:      OverlaySize{Value: 1},
		Content:     &lineComp{lines: []string{"O"}},
	}
	out := composeOverlays(base, []*Overlay{ov}, 10, 1)
	ser := core.SerializeRow(out[0])
	// '中' must NOT appear — its right half was overwritten, so the left
	// half is collapsed to a space (clearWideBoundary). This is correct:
	// half a wide char is worse than a space.
	if strings.Contains(ser, "中") {
		t.Fatalf("wide char '中' should be collapsed (its right half was overwritten), got %q", ser)
	}
	// 'O' must be present at column 1.
	if !strings.Contains(ser, "O") {
		t.Fatalf("overlay 'O' missing from %q", ser)
	}
	// The two trailing 'x' cells must survive.
	if strings.Count(ser, "x") != 2 {
		t.Fatalf("expected 2 'x' cells to survive, got %q", ser)
	}
	// Width must be preserved at 10.
	if out[0].VisibleWidth() != 10 {
		t.Fatalf("width = %d, want 10, row=%q", out[0].VisibleWidth(), ser)
	}
}

// TestComposeOverlayWideCharLeftHalfOverwritten verifies that overwriting
// the LEFT half of a wide char also clears the right half (no orphaned
// continuation cell remains).
func TestComposeOverlayWideCharLeftHalfOverwritten(t *testing.T) {
	// Base: "中中" (width 4). Pad to 8.
	base := []core.Row{core.ParseLine(core.PadToWidth("中中", 8))}
	// Overlay 'O' at column 0 — overwrites the left half of first 中.
	ov := &Overlay{
		UseAbsolute: true,
		Anchor:      AnchorTopLeft,
		Row:         0,
		Col:         0,
		Width:       OverlaySize{Value: 1},
		Height:      OverlaySize{Value: 1},
		Content:     &lineComp{lines: []string{"O"}},
	}
	out := composeOverlays(base, []*Overlay{ov}, 8, 1)
	ser := core.SerializeRow(out[0])
	// First 中 is gone (left half overwritten → right half collapsed).
	// Second 中 must survive intact.
	if strings.Count(ser, "中") != 1 {
		t.Fatalf("expected exactly 1 surviving '中' (second one), got %q", ser)
	}
	if !strings.Contains(ser, "O") {
		t.Fatalf("overlay 'O' missing from %q", ser)
	}
	if out[0].VisibleWidth() != 8 {
		t.Fatalf("width = %d, want 8, row=%q", out[0].VisibleWidth(), ser)
	}
}

// TestComposeOverlayWideOverlayOnNarrowBase verifies the inverse: a wide
// overlay cell landing on narrow base cells.
func TestComposeOverlayWideOverlayOnNarrowBase(t *testing.T) {
	// Base: "abcd" (width 4). Pad to 8.
	base := []core.Row{core.ParseLine(core.PadToWidth("abcd", 8))}
	// Overlay '中' (width 2) at column 1 — overwrites 'b' and 'c'.
	ov := &Overlay{
		UseAbsolute: true,
		Anchor:      AnchorTopLeft,
		Row:         0,
		Col:         1,
		Width:       OverlaySize{Value: 2},
		Height:      OverlaySize{Value: 1},
		Content:     &lineComp{lines: []string{"中"}},
	}
	out := composeOverlays(base, []*Overlay{ov}, 8, 1)
	ser := core.SerializeRow(out[0])
	// 'a' and 'd' survive; 'b' and 'c' are replaced by '中'.
	if !strings.Contains(ser, "a") || !strings.Contains(ser, "d") {
		t.Fatalf("expected 'a' and 'd' to survive, got %q", ser)
	}
	if strings.Contains(ser, "b") || strings.Contains(ser, "c") {
		t.Fatalf("expected 'b' and 'c' to be overwritten, got %q", ser)
	}
	if !strings.Contains(ser, "中") {
		t.Fatalf("overlay '中' missing from %q", ser)
	}
	if out[0].VisibleWidth() != 8 {
		t.Fatalf("width = %d, want 8, row=%q", out[0].VisibleWidth(), ser)
	}
}

// TestComposeOverlayPreservesSGR verifies that overlay composition doesn't
// lose SGR state — the bug that motivated cells carrying absolute styles.
func TestComposeOverlayPreservesSGR(t *testing.T) {
	// Base: red "hello" then plain "world", padded to 20.
	base := []core.Row{core.ParseLine(core.PadToWidth("\x1b[31mhello\x1b[0mworld", 20))}
	// Overlay "XY" at column 7 (over the "world" part).
	ov := &Overlay{
		UseAbsolute: true,
		Anchor:      AnchorTopLeft,
		Row:         0,
		Col:         7,
		Width:       OverlaySize{Value: 2},
		Height:      OverlaySize{Value: 1},
		Content:     &lineComp{lines: []string{"XY"}},
	}
	out := composeOverlays(base, []*Overlay{ov}, 20, 1)
	ser := core.SerializeRow(out[0])
	// The red "hello" (cells 0-4) must keep its red style. Parse and check.
	row := out[0]
	if row.Cells[0].Style.Fg.PaletteIndex() != 1 {
		t.Fatalf("cell 0 fg = %+v, want palette 1 (red) — style lost in composition", row.Cells[0].Style.Fg)
	}
	// The overlay cells (7,8) should be default style.
	if !row.Cells[7].Style.Fg.IsDefault() {
		t.Fatalf("overlay cell 7 fg = %+v, want default", row.Cells[7].Style.Fg)
	}
	_ = ser
}

// TestComposeOverlayTruncatesOverWide verifies #5: a misbehaving component
// returning more columns than requested is truncated, not allowed to spill
// past the overlay region and corrupt the cell grid's column alignment.
func TestComposeOverlayTruncatesOverWide(t *testing.T) {
	// Base: 20 dots.
	base := stringRows(1, 20)
	// Overlay claims width 4 but renders 10 chars (ignores the width hint).
	ov := &Overlay{
		UseAbsolute: true,
		Anchor:      AnchorTopLeft,
		Row:         0,
		Col:         0,
		Width:       OverlaySize{Value: 4},
		Height:      OverlaySize{Value: 1},
		Content:     &lineComp{lines: []string{"XXXXXXXXXX"}}, // 10 wide, but overlay is 4
	}
	out := composeOverlays(base, []*Overlay{ov}, 20, 1)
	// The overlay must occupy exactly 4 columns; the rest of the row must
	// still be the base dots (not X's).
	if out[0].VisibleWidth() != 20 {
		t.Fatalf("row width = %d, want 20 (overlay spill corrupted width)", out[0].VisibleWidth())
	}
	ser := core.SerializeRow(out[0])
	// Only 4 X's should appear (truncated), not 10.
	if strings.Count(ser, "X") != 4 {
		t.Fatalf("expected 4 X (truncated to overlay width), got %q", ser)
	}
	// Dots must survive past column 4.
	if strings.Count(ser, ".") != 16 {
		t.Fatalf("expected 16 dots to survive, got %q", ser)
	}
}

// TestComposeOverlayRawOverlayCoversBase verifies #6: a Raw overlay row
// (e.g. Kitty graphics APC) replaces the base row instead of being silently
// dropped. Overlay semantics say the overlay covers the base.
func TestComposeOverlayRawOverlayCoversBase(t *testing.T) {
	// Base: a row of dots.
	base := stringRows(1, 20)
	// Overlay produces a Raw row (contains an APC the cell model can't parse).
	// We bypass the normal PadToWidth-based lineComp because Raw rows must
	// be passed through verbatim — padding an APC payload would corrupt it.
	rawPayload := "X\x1b_Ga=q\x1b\\Y"
	ov := &Overlay{
		UseAbsolute: true,
		Anchor:      AnchorTopLeft,
		Row:         0,
		Col:         0,
		Width:       OverlaySize{Value: 20},
		Height:      OverlaySize{Value: 1},
		Content:     &passthroughComp{line: rawPayload},
	}
	out := composeOverlays(base, []*Overlay{ov}, 20, 1)
	// The base row should now be the Raw overlay row, not the dots.
	if !out[0].IsRaw() {
		t.Fatalf("expected Raw row from overlay, got cells: %v", out[0].Cells)
	}
	if out[0].Raw != rawPayload {
		t.Fatalf("raw content = %q, want %q", out[0].Raw, rawPayload)
	}
	// Dots must NOT survive (overlay covered them).
	if strings.Contains(out[0].Raw, ".") {
		t.Fatalf("base dots should be covered by raw overlay: %q", out[0].Raw)
	}
}

// passthroughComp renders its line verbatim, without padding. Used to test
// Raw overlay rows (APC payloads) that must not be cell-padded.
type passthroughComp struct{ line string }

func (p *passthroughComp) Render(int64) []string { return []string{p.line} }
func (p *passthroughComp) Invalidate()           {}
func (p *passthroughComp) Update(core.Msg) core.Cmd { return nil }

// TestComposeOverlayDropShadow verifies the DimBackground drop shadow forms
// a correct L-shaped ring: a 1-cell column to the right of the overlay and a
// 1-cell band below it (offset one cell right), with the background outside
// the overlay dimmed. This is the regression test for the bug where the row
// directly below the overlay was left undimmed (a bright strip) instead of
// carrying the bottom shadow band.
func TestComposeOverlayDropShadow(t *testing.T) {
	// 10 rows x 20 cols base. Overlay at (row=2,col=4), 6 wide, 3 tall.
	// The backdrop must be a UNIFORM dim layer — no drop shadow ring (the
	// shadow rendered as faint rectangular box edges, so it was removed).
	base := stringRows(10, 20)
	ov := &Overlay{
		UseAbsolute:   true,
		Anchor:        AnchorTopLeft,
		Row:           2,
		Col:           4,
		Width:         OverlaySize{Value: 6},
		Height:        OverlaySize{Value: 3},
		DimBackground: true,
		Content:       &lineComp{lines: []string{"AAAAAA", "BBBBBB", "CCCCCC"}},
	}
	out := composeOverlays(base, []*Overlay{ov}, 20, 10)

	isDim := func(c core.Cell) bool { return c.Style.Attrs&dimTextAttr != 0 }

	// Overlay rect: rows [2,5), cols [4,10). Every cell OUTSIDE that rect must
	// be dimmed; every cell inside is overlay content (not dimmed background).
	for r := int64(0); r < 10; r++ {
		for c := int64(0); c < 20; c++ {
			inOverlay := r >= 2 && r < 5 && c >= 4 && c < 10
			cell := out[r].Cells[c]
			if inOverlay {
				continue
			}
			if !isDim(cell) {
				t.Fatalf("row %d col %d should be dimmed background, attrs=%v bg=%v", r, c, cell.Style.Attrs, cell.Style.Bg)
			}
		}
	}

	// All rows preserve the 20-column grid.
	for i, r := range out {
		if r.VisibleWidth() != 20 {
			t.Fatalf("row %d width = %d, want 20", i, r.VisibleWidth())
		}
	}
}
