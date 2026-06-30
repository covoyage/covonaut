package tui

import (
	core "github.com/covoyage/covonaut/tui/core"
)

// dimStyle values used by cell-level dimBackground. Palette lookups are
// function calls, so these can't be `const`; package-level vars are fine
// since they're only read (never mutated) after init.
var (
	dimTextAttr = core.AttrDim
	dimBgColor  = core.Palette(235)
)

// ---------------------------------------------------------------------------
// Overlay — a floating panel mounted on top of the root view.
//
// Positioning supports:
//   - Anchor points (corners / centre / cardinal mid-points) — OverlayAnchor*.
//   - Percentage offsets — the overlay is placed at (pctX, pctY) of the
//     viewport minus the overlay's own size × anchor ratio.
//   - Absolute rows/cols via Row/Col.
//
// Width/Height accept either fixed cells or percentages (use Percent = true).
// ---------------------------------------------------------------------------

// OverlayAnchor describes how (Row, Col) or (PercentX, PercentY) should be
// interpreted relative to the overlay itself.
type OverlayAnchor int64

const (
	AnchorTopLeft OverlayAnchor = iota
	AnchorTopCenter
	AnchorTopRight
	AnchorMiddleLeft
	AnchorCenter
	AnchorMiddleRight
	AnchorBottomLeft
	AnchorBottomCenter
	AnchorBottomRight
)

// OverlaySize specifies a dimension in cells or percent-of-viewport.
type OverlaySize struct {
	Value   int64
	Percent bool // if true, Value is 0..100 of the viewport
	Min     int64
	Max     int64
}

// resolve computes the effective cell count for a given viewport dimension.
func (s OverlaySize) resolve(viewport int64) int64 {
	v := s.Value
	if s.Percent {
		v = viewport * s.Value / 100
	}
	if s.Min > 0 && v < s.Min {
		v = s.Min
	}
	if s.Max > 0 && v > s.Max {
		v = s.Max
	}
	if v < 1 {
		v = 1
	}
	if v > viewport {
		v = viewport
	}
	return v
}

// Overlay is a floating panel positioned on top of the root view.
type Overlay struct {
	Content core.Component

	Anchor OverlayAnchor

	// Absolute positioning (takes precedence over Percent*).
	UseAbsolute bool
	Row         int64
	Col         int64

	// Percent positioning (only used if !UseAbsolute).
	PercentX int64 // 0..100
	PercentY int64 // 0..100

	Width  OverlaySize
	Height OverlaySize

	// Focus tells the TUI to push Content onto the focus stack when the
	// overlay is mounted, and pop it when removed.
	Focus bool

	// DimBackground dims the underlying lines and draws a drop shadow around
	// the overlay region (see dimBackgroundRows).
	DimBackground bool
}

// NewCenteredOverlay is a convenience constructor for a centred panel
// whose size is a percentage of the viewport.
func NewCenteredOverlay(c core.Component, widthPct, heightPct int64) *Overlay {
	return &Overlay{
		Content:  c,
		Anchor:   AnchorCenter,
		PercentX: 50, PercentY: 50,
		Width:  OverlaySize{Value: widthPct, Percent: true, Min: 10},
		Height: OverlaySize{Value: heightPct, Percent: true, Min: 3},
	}
}

// NewBottomRightOverlay is a convenience for a small toast at the bottom-right.
func NewBottomRightOverlay(c core.Component, width, height int64) *Overlay {
	return &Overlay{
		Content:  c,
		Anchor:   AnchorBottomRight,
		PercentX: 100, PercentY: 100,
		Width:  OverlaySize{Value: width},
		Height: OverlaySize{Value: height},
	}
}

// ---------------------------------------------------------------------------
// Composition (cell-level)
// ---------------------------------------------------------------------------

// composeOverlays splices overlay renders onto base rows in stack order.
//
// The composition operates on the cell model: each base row is parsed into
// cells, overlay content is copied cell-by-cell into the target region, and
// the result is left in cell form for the renderer to diff and serialize.
// This eliminates the wide-char truncation and style-loss bugs that the
// previous string-level spliceOverlay had (see cell.go commentary).
func composeOverlays(base []core.Row, overlays []*Overlay, cols, rows int64) []core.Row {
	if len(overlays) == 0 {
		return base
	}
	// Deep-copy the caller's base: dimBackgroundRows / spliceOverlayRows
	// mutate cell contents in place. Without this copy we'd corrupt the
	// caller's slice — and in tests, any reused base would accumulate
	// overlay artifacts across calls.
	clone := make([]core.Row, len(base))
	for i, r := range base {
		clone[i] = r
		if r.Cells != nil {
			clone[i].Cells = make([]core.Cell, len(r.Cells))
			copy(clone[i].Cells, r.Cells)
		}
	}
	base = clone
	// Ensure we have at least `rows` lines so bottom-anchored overlays land.
	for int64(len(base)) < rows {
		base = append(base, blankRow(cols))
	}

	// If any overlay dims the background, pad every (non-raw) base row out to
	// the full viewport width first. dimBackgroundRows / paintRowRange only
	// touch cells that already exist, so short rows would otherwise leave
	// their un-dimmed tail at the terminal's default background — producing
	// phantom rectangular "boxes" along the right/top of the screen where the
	// dimmed and un-dimmed regions meet. Padding to `cols` makes the frosted
	// glass effect uniform across the whole viewport.
	dims := false
	for _, ov := range overlays {
		if ov != nil && ov.DimBackground {
			dims = true
			break
		}
	}
	if dims {
		for i := range base {
			if base[i].IsRaw() {
				// Empty raw rows (Cells==nil, Raw=="") carry no opaque
				// payload — they're just blank lines. The dim layer skips
				// raw rows, so left as-is they stay at the terminal default
				// background and show up as bright horizontal strips that
				// bracket the dimmed content rows (phantom "box" outlines).
				// Convert them to real blank rows so they dim uniformly.
				// Rows with an actual Raw payload (e.g. Kitty graphics) are
				// left untouched.
				if base[i].Raw == "" {
					base[i] = blankRow(cols)
				}
				continue
			}
			base[i] = core.PadRow(base[i], cols, core.DefaultStyle)
		}
	}

	// Apply dimming for overlays that request it.
	for _, ov := range overlays {
		if ov == nil || !ov.DimBackground {
			continue
		}
		w := ov.Width.resolve(cols)
		h := ov.Height.resolve(rows)
		origin := resolveOverlayOrigin(ov, cols, rows, w, h)
		base = dimBackgroundRows(base, cols, rows, origin.row, origin.col, w, h)
	}

	for _, ov := range overlays {
		if ov == nil || ov.Content == nil {
			continue
		}
		w := ov.Width.resolve(cols)
		h := ov.Height.resolve(rows)
		rawLines := ov.Content.Render(w)
		// Normalise height: pad short, truncate tall.
		for int64(len(rawLines)) < h {
			rawLines = append(rawLines, core.PadToWidth("", w))
		}
		if int64(len(rawLines)) > h {
			rawLines = rawLines[:h]
		}
		// Parse each rendered line into a Row, then enforce the overlay's
		// width: truncate over-wide rows (a misbehaving component returning
		// more columns than requested would otherwise spill past the overlay
		// region and corrupt the cell grid's column alignment) and pad
		// short rows so the overlay is a solid rectangle.
		content := make([]core.Row, len(rawLines))
		for i, ln := range rawLines {
			r := core.ParseLine(ln)
			// An empty content line parses to a Raw "" row. spliceOverlayRows
			// replaces the WHOLE base row for a raw overlay row, which would
			// wipe out the dimmed side margins on that line — leaving a bright
			// notch in the left/right dim columns (a panel that emits blank
			// lines, e.g. the TODO panel, otherwise shows this). Turn empty
			// lines into a solid w-wide blank so only the overlay columns are
			// overwritten and the margins survive.
			if r.IsRaw() && r.Raw == "" {
				r = blankRow(w)
			}
			r = core.TruncateRow(r, w)
			content[i] = core.PadRow(r, w, core.DefaultStyle)
		}

		origin := resolveOverlayOrigin(ov, cols, rows, w, h)
		base = spliceOverlayRows(base, content, origin.row, origin.col, cols)
	}
	return base
}

// blankRow returns a Row of `cols` space cells in the default style.
func blankRow(cols int64) core.Row {
	cells := make([]core.Cell, cols)
	for i := range cells {
		cells[i] = core.Cell{Rune: ' ', Width: 1, Style: core.DefaultStyle}
	}
	return core.Row{Cells: cells, CursorCol: -1}
}

// dimBackgroundRows applies the frosted-glass effect to base rows at the cell
// level: every cell OUTSIDE the overlay region gets a dim attribute and the
// dark glass background, so the backdrop reads as a uniform dimmed layer.
// Cells under the overlay itself are left untouched (the overlay is spliced
// on top afterwards).
//
// No drop shadow is drawn: the shadow ring (a slightly darker bg on the
// overlay's right column and bottom band) rendered as faint rectangular
// "box" edges against the dim backdrop — visually indistinguishable from an
// artifact — so the backdrop is kept perfectly uniform instead.
func dimBackgroundRows(base []core.Row, cols, rows, oRow, oCol, oW, oH int64) []core.Row {
	_ = rows
	// Overlay rectangle: rows [ovTop, ovBot), cols [ovLeft, ovRight).
	ovTop, ovBot := oRow, oRow+oH
	ovLeft, ovRight := oCol, oCol+oW

	clamp := func(v int64) int64 {
		if v < 0 {
			return 0
		}
		if v > cols {
			return cols
		}
		return v
	}

	for i := range base {
		r := int64(i)
		dim := func(a, b int64) {
			if a, b = clamp(a), clamp(b); b > a {
				base[i] = applyDimToRow(base[i], a, b, true)
			}
		}

		if r >= ovTop && r < ovBot {
			// Overlay row: dim the side margins, leave the overlay region.
			dim(0, ovLeft)
			dim(ovRight, cols)
		} else {
			// Fully outside the overlay: dim the whole row.
			dim(0, cols)
		}
	}
	return base
}


// applyDimToRow adds the dim attribute and a dark glass background to cells
// in the column range [start, end) of row. If row is Raw it is left as-is
// (dim doesn't compose cleanly with opaque content).
func applyDimToRow(row core.Row, start, end int64, withBg bool) core.Row {
	if row.IsRaw() {
		return row
	}
	for c := start; c < end && c < int64(len(row.Cells)); c++ {
		cell := &row.Cells[c]
		if cell.IsContinuation() {
			// Continuation cell (right half of a wide char). Dim the primary
			// cell to the left so the glyph itself dims, but ALSO dim this
			// cell's own background. Otherwise the continuation half keeps
			// its original (often default) background and shows up as a
			// bright half-cell speckle across the dimmed region — most
			// visible with CJK text, where every wide char leaves a gap.
			if withBg {
				cell.Style.Bg = dimBgColor
			}
			cell.Style.Attrs |= dimTextAttr
			if c > 0 {
				cell = &row.Cells[c-1]
			} else {
				continue
			}
		}
		cell.Style.Attrs |= dimTextAttr
		if withBg {
			cell.Style.Bg = dimBgColor
		}
	}
	return row
}

// spliceOverlayRows copies overlay cells onto base at (row, col), handling
// wide-char boundaries correctly. When an overlay cell lands on the left
// half of a wide base cell, the right-half continuation is also cleared
// (replaced with a narrow space). When it lands on a continuation cell,
// the left-half primary is cleared too — preventing orphaned half-glyphs.
//
// Raw overlay rows (e.g. Kitty graphics APC) replace the corresponding base
// row entirely — an overlay is supposed to cover the base, not be hidden by
// it. Raw rows can't be cell-spliced because they carry opaque escape
// payloads with no column structure, so whole-row replacement is the only
// sound semantics.
func spliceOverlayRows(base []core.Row, content []core.Row, row, col, cols int64) []core.Row {
	// Grow base vertically if needed.
	for int64(len(base)) < row+int64(len(content)) {
		base = append(base, blankRow(cols))
	}
	for i, srcRow := range content {
		targetIdx := row + int64(i)
		if targetIdx >= int64(len(base)) {
			break
		}
		target := &base[targetIdx]

		// Raw overlay row: replace the base row wholesale. Overlay semantics
		// say the overlay covers the base, so base-wins was wrong here.
		if srcRow.IsRaw() {
			base[targetIdx] = srcRow
			continue
		}

		if target.IsRaw() {
			// Base is raw (opaque) but overlay is cells. The overlay can't
			// be cell-spliced onto an opaque row, so the overlay wins by
			// replacing the row with a blank cell row first, then splicing.
			base[targetIdx] = blankRow(cols)
			target = &base[targetIdx]
		}

		// Ensure the target row has at least `cols` cells. Base rows
		// produced by component Render may be shorter than the viewport
		// width (short content lines, trailing blanks, recently-truncated
		// rows). Splicing overlay cells past the end of target.Cells would
		// panic with index-out-of-range; pad with default-style spaces so
		// the overlay can be placed anywhere within the viewport.
		if n := int64(len(target.Cells)); n < cols {
			padded := make([]core.Cell, cols)
			copy(padded, target.Cells)
			for i := n; i < cols; i++ {
				padded[i] = core.Cell{Rune: ' ', Width: 1, Style: core.DefaultStyle}
			}
			target.Cells = padded
		}

		colIdx := col
		for _, src := range srcRow.Cells {
			if src.IsContinuation() {
				// Continuation cells are emitted by the primary; skip.
				continue
			}
			if colIdx >= cols {
				break
			}
			// Make room: if we're about to overwrite a wide primary or its
			// continuation, normalise the affected base cells first.
			clearWideBoundary(target, colIdx, src.Width, cols)
			// Place the source cell.
			target.Cells[colIdx] = src
			if src.Width == 2 {
				// Place a continuation placeholder.
				if colIdx+1 < int64(len(target.Cells)) {
					target.Cells[colIdx+1] = core.Cell{Width: 0, Style: src.Style}
				}
				colIdx += 2
			} else {
				colIdx++
			}
		}
		// Preserve cursor marker from the source row if present.
		if srcRow.CursorCol >= 0 && target.CursorCol < 0 {
			target.CursorCol = srcRow.CursorCol + int(col)
		}
	}
	return base
}

// clearWideBoundary normalises base cells at colIdx so that placing a cell
// of width `incoming` there doesn't leave an orphaned half of a wide char.
// Specifically:
//   - If base[colIdx] is a wide primary (Width=2), its continuation at
//     colIdx+1 is replaced with a narrow space (the overlay cell overwrites
//     the primary; the continuation is no longer valid).
//   - If base[colIdx] is a continuation (Width=0), the primary at colIdx-1
//     is replaced with a narrow space (the overlay cell overwrites the
//     right half; the left half must yield).
//   - If incoming width is 2 and base[colIdx+1] is a wide primary, that
//     primary's continuation at colIdx+2 is replaced with a narrow space.
func clearWideBoundary(row *core.Row, colIdx int64, incoming int8, cols int64) {
	if row.IsRaw() || len(row.Cells) == 0 {
		return
	}
	n := int64(len(row.Cells))
	// Overwrite target: handle wide primary at colIdx.
	if colIdx < n && row.Cells[colIdx].Width == 2 {
		// Right-half continuation at colIdx+1 will be orphaned — replace it
		// with a narrow space so the row stays well-formed.
		if colIdx+1 < n {
			row.Cells[colIdx+1] = core.Cell{Rune: ' ', Width: 1, Style: row.Cells[colIdx].Style}
		}
	}
	// Overwrite target: handle continuation at colIdx (orphaning left half).
	if colIdx < n && row.Cells[colIdx].IsContinuation() && colIdx > 0 {
		// Left-half primary at colIdx-1 is now orphaned — collapse it to a
		// narrow space so we don't render a half-wide glyph.
		row.Cells[colIdx-1] = core.Cell{Rune: ' ', Width: 1, Style: row.Cells[colIdx].Style}
	}
	// Incoming wide char: if its continuation (colIdx+1) lands on a wide
	// primary, that primary's own continuation at colIdx+2 is orphaned.
	if incoming == 2 && colIdx+1 < n && row.Cells[colIdx+1].Width == 2 {
		if colIdx+2 < n {
			row.Cells[colIdx+2] = core.Cell{Rune: ' ', Width: 1, Style: row.Cells[colIdx+1].Style}
		}
	}
}

type overlayOrigin struct{ row, col int64 }

func resolveOverlayOrigin(ov *Overlay, cols, rows, w, h int64) overlayOrigin {
	var r, c int64
	if ov.UseAbsolute {
		r = ov.Row
		c = ov.Col
	} else {
		r = rows * ov.PercentY / 100
		c = cols * ov.PercentX / 100
	}
	// Apply anchor offsets (how much of the overlay sits to the left/above).
	switch ov.Anchor {
	case AnchorTopLeft:
		// no offset
	case AnchorTopCenter:
		c -= w / 2
	case AnchorTopRight:
		c -= w
	case AnchorMiddleLeft:
		r -= h / 2
	case AnchorCenter:
		r -= h / 2
		c -= w / 2
	case AnchorMiddleRight:
		r -= h / 2
		c -= w
	case AnchorBottomLeft:
		r -= h
	case AnchorBottomCenter:
		r -= h
		c -= w / 2
	case AnchorBottomRight:
		r -= h
		c -= w
	}
	if r < 0 {
		r = 0
	}
	if c < 0 {
		c = 0
	}
	if r+h > rows {
		r = rows - h
	}
	if c+w > cols {
		c = cols - w
	}
	if r < 0 {
		r = 0
	}
	if c < 0 {
		c = 0
	}
	return overlayOrigin{row: r, col: c}
}
