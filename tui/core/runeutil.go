package core

import "unicode"

// ---------------------------------------------------------------------------
// Rune utility functions shared by Input and Editor components.
//
// These are general-purpose rune/cell-width helpers that don't belong to any
// specific component. Layer 0 — no internal dependencies beyond width.go and
// component.go (for CURSOR_MARKER).
// ---------------------------------------------------------------------------

// CellWidthOfRunes returns the visible column width of runes[start:end].
func CellWidthOfRunes(runes []rune, start, end int64) int64 {
	if start < 0 {
		start = 0
	}
	if end > int64(len(runes)) {
		end = int64(len(runes))
	}
	var w int64
	for i := start; i < end; i++ {
		w += RuneWidth(runes[i])
	}
	return w
}

// RuneSlice is a helper wrapping a substring and its source rune range.
type RuneSlice struct {
	Text   string
	StartR int64
	EndR   int64
}

// SliceRunesByCells returns the longest run of runes whose cumulative cell
// width starts at startCell and fits before endCell.
func SliceRunesByCells(runes []rune, startCell, endCell int64) RuneSlice {
	var col int64
	var startR, endR int64 = -1, -1
	var b []rune
	for idx, r := range runes {
		w := RuneWidth(r)
		if col >= startCell && col+w <= endCell {
			if startR < 0 {
				startR = int64(idx)
			}
			b = append(b, r)
			endR = int64(idx) + 1
		}
		col += w
		if col > endCell {
			break
		}
	}
	if startR < 0 {
		startR = 0
		endR = 0
	}
	return RuneSlice{Text: string(b), StartR: startR, EndR: endR}
}

// AdjustHorizontalScroll keeps the cursor visible within [0, avail).
func AdjustHorizontalScroll(scroll, cursorCol, avail int64) int64 {
	if avail <= 0 {
		return 0
	}
	if cursorCol < scroll {
		return cursorCol
	}
	if cursorCol >= scroll+avail {
		return cursorCol - avail + 1
	}
	return scroll
}

// InsertMarker inserts CURSOR_MARKER at the visible column col in rendered.
func InsertMarker(rendered string, col int64) string {
	before := SliceByColumn(rendered, 0, col)
	after := SliceByColumn(rendered, col, VisibleWidth(rendered))
	return before + CURSOR_MARKER + after
}

// ---------------------------------------------------------------------------
// Word-boundary helpers
// ---------------------------------------------------------------------------

func IsWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

func FindWordBoundaryLeft(runes []rune, cursor int64) int64 {
	i := cursor
	for i > 0 && !IsWordRune(runes[i-1]) {
		i--
	}
	for i > 0 && IsWordRune(runes[i-1]) {
		i--
	}
	return i
}

func FindWordBoundaryRight(runes []rune, cursor int64) int64 {
	n := int64(len(runes))
	i := cursor
	for i < n && !IsWordRune(runes[i]) {
		i++
	}
	for i < n && IsWordRune(runes[i]) {
		i++
	}
	return i
}
