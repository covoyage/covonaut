package core

import (
	"strings"
	"unicode/utf8"
)

// ---------------------------------------------------------------------------
// string → Row parser.
//
// ParseLine converts a rendered string into a cell Row. The parser handles:
//
//   - ANSI CSI sequences: SGR (CSI ... m) updates the running style; all
//     other CSI sequences are zero-width and silently dropped (they position
//     the cursor or set modes, which the cell model doesn't represent — the
//     renderer owns cursor positioning).
//   - OSC / APC / DCS / PM string sequences: zero-width. CURSOR_MARKER is
//     detected specifically and recorded as CursorCol; other APCs (Kitty
//     graphics, OSC titles, etc.) cause the line to fall back to Raw form
//     so they pass through verbatim.
//   - Runes: each visible rune becomes a Cell. Wide runes (RuneWidth==2)
//     produce a Cell followed by a continuation Cell. Combining marks
//     (RuneWidth==0) attach to the preceding Cell's Combining slice.
//
// Grapheme clusters (UAX #29) are handled at a pragmatic level:
//   - Combining diacritical marks attach to the preceding base rune.
//   - ZWJ-joined emoji sequences (e.g. 👨‍👩‍👧) are stored as separate cells
//     per rune — the cell grid records the sum of individual rune widths
//     even though the terminal renders the cluster as one glyph. This is a
//     known approximation; the renderer accounts for it by emitting the
//     runes back as-is. A full cluster segmenter would collapse these to a
//     single 2-wide cell, but the work vs payoff is low for a TUI that
//     rarely emits composed emoji.
// ---------------------------------------------------------------------------

// ParseLine parses a rendered string into a Row.
//
// Returns a Raw Row when the line contains escapes the cell model cannot
// represent (Kitty graphics APC, OSC titles, etc.). CURSOR_MARKER is the
// only inline APC that does NOT trigger Raw fallback — it is extracted into
// CursorCol and stripped from the cell content.
func ParseLine(s string) Row {
	// First pass: scan for any escape that forces Raw fallback. If found,
	// return a Raw row but still extract CURSOR_MARKER for cursor placement.
	if hasUnrepresentableEscape(s) {
		raw := s
		cursorCol := -1
		if idx := strings.Index(s, CURSOR_MARKER); idx >= 0 {
			cursorCol = int(VisibleWidth(s[:idx]))
			raw = strings.Replace(s, CURSOR_MARKER, "", 1)
		}
		return Row{Raw: raw, CursorCol: cursorCol}
	}

	// Cell-parseable. Walk the string maintaining the running style.
	var cells []Cell
	var cursorCol int = -1
	style := DefaultStyle

	i := 0
	for i < len(s) {
		c := s[i]
		if c == 0x1B {
			adv := SkipAnsiSeq(s, i)
			if adv == 0 {
				// Lone ESC with no continuation — treat as a literal byte.
				adv = 1
			} else {
				// Is this CURSOR_MARKER specifically? It's an APC of the
				// form "\x1b_pi:c\x07". Detect by prefix match.
				if adv == len(CURSOR_MARKER) && s[i:i+adv] == CURSOR_MARKER {
					if cursorCol < 0 {
						cursorCol = int(visibleWidthOfCells(cells))
					}
					i += adv
					continue
				}
				// Is this an SGR (CSI ... m)?
				if isSGRSequence(s, i, adv) {
					params := s[i+2 : i+adv-1]
					style = ParseSGR(params, style)
				}
				// All other escapes (CSI non-SGR, OSC, APC, etc.) are
				// zero-width and have no cell representation.
				i += adv
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size <= 1 {
			// Invalid UTF-8 byte — skip.
			i++
			continue
		}
		rw := RuneWidth(r)
		switch {
		case rw == 0 && len(cells) > 0:
			// Combining mark — attach to the previous cell. Don't attach to
			// a continuation cell; attach to its primary (one to the left).
			idx := len(cells) - 1
			for idx > 0 && cells[idx].IsContinuation() {
				idx--
			}
			cells[idx].Combining = append(cells[idx].Combining, r)
		case rw == 2:
			cells = append(cells, Cell{
				Rune:  r,
				Width: 2,
				Style: style,
			})
			// Continuation placeholder.
			cells = append(cells, Cell{Width: 0, Style: style})
		default: // rw == 1
			cells = append(cells, Cell{
				Rune:  r,
				Width: 1,
				Style: style,
			})
		}
		i += size
	}
	return Row{Cells: cells, CursorCol: cursorCol}
}

// hasUnrepresentableEscape reports whether s contains an escape sequence
// the cell model can't represent. CURSOR_MARKER is the only allowed inline
// APC; any other OSC/APC/DCS/PM triggers Raw fallback.
func hasUnrepresentableEscape(s string) bool {
	i := 0
	for i < len(s) {
		if s[i] != 0x1B {
			i++
			continue
		}
		adv := SkipAnsiSeq(s, i)
		if adv == 0 {
			i++
			continue
		}
		// What kind of escape is this?
		if i+1 < len(s) {
			kind := s[i+1]
			switch kind {
			case '[':
				// CSI — only SGR (final 'm') is representable. Non-SGR CSI
				// sequences (cursor movement, scrolling, mode setting) are
				// not representable → fall back to Raw.
				if !isSGRSequence(s, i, adv) {
					return true
				}
			case ']', '_', 'P', '^':
				// OSC / APC / DCS / PM. CURSOR_MARKER is the only allowed one.
				if s[i:i+adv] != CURSOR_MARKER {
					return true
				}
			default:
				// Two-byte escapes (ESC N, ESC O, ESC =, etc.) — not
				// representable.
				return true
			}
		}
		i += adv
	}
	return false
}

// isSGRSequence reports whether s[i:i+adv] is a CSI sequence ending in 'm'.
func isSGRSequence(s string, i, adv int) bool {
	if adv < 3 {
		return false
	}
	if s[i] != 0x1B || s[i+1] != '[' {
		return false
	}
	return s[i+adv-1] == 'm'
}

// visibleWidthOfCells sums the Width fields of the cells (continuations
// contribute 0). Used to compute the column of an inline CURSOR_MARKER.
func visibleWidthOfCells(cells []Cell) int64 {
	var w int64
	for _, c := range cells {
		if c.Width > 0 {
			w += int64(c.Width)
		}
	}
	return w
}

// PadRow right-pads a cell row with space cells (using the given style)
// until its visible width equals width. No-op if already at or past width.
func PadRow(row Row, width int64, style Style) Row {
	if row.IsRaw() {
		return row
	}
	w := row.VisibleWidth()
	if w >= width {
		return row
	}
	padded := make([]Cell, len(row.Cells), int(len(row.Cells)+int(width-w)))
	copy(padded, row.Cells)
	for int64(len(padded)) < width {
		padded = append(padded, Cell{Rune: ' ', Width: 1, Style: style})
	}
	return Row{Cells: padded, CursorCol: row.CursorCol}
}

// TruncateRow truncates a cell row to at most `width` visible columns.
//
// Wide-char safety: if the cut would land on the left half of a wide char
// (leaving an orphaned continuation), that wide char is dropped entirely
// and replaced with a single space cell, so the result is always a
// well-formed row whose VisibleWidth == width (when the input was wider).
//
// CursorCol beyond the new width is clamped to width-1; -1 is preserved.
func TruncateRow(row Row, width int64) Row {
	if row.IsRaw() {
		// Raw rows can't be cell-truncated; caller should fall back to
		// string-level TruncateToWidth if needed. Return as-is.
		return row
	}
	w := row.VisibleWidth()
	if w <= width {
		return row
	}
	// Walk cells accumulating visible width; stop when we hit the budget.
	var out []Cell
	var col int64
	for _, c := range row.Cells {
		if c.Width == 0 {
			// Continuation of the previous wide cell — include it only if
			// its primary was included (i.e. we haven't exceeded width).
			if col <= width {
				out = append(out, c)
			}
			continue
		}
		cw := int64(c.Width)
		if col+cw > width {
			// This cell would overflow. If it's a wide char (width 2) and
			// we have at least 1 column left, fill with a space rather
			// than dropping the column (avoids a ragged right edge).
			if col < width {
				out = append(out, Cell{Rune: ' ', Width: 1, Style: c.Style})
			}
			break
		}
		out = append(out, c)
		col += cw
	}
	r := Row{Cells: out, CursorCol: row.CursorCol}
	if r.CursorCol >= 0 && r.CursorCol >= int(width) {
		r.CursorCol = int(width - 1)
		if r.CursorCol < 0 {
			r.CursorCol = 0
		}
	}
	return r
}
