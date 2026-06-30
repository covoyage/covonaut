package core

// ---------------------------------------------------------------------------
// Cell-level frame diff.
//
// DiffRows compares two frame buffers (slices of Row) and returns the rows
// that differ, ready for the renderer to re-emit. The comparison is
// stricter than the string model's: two rows are equal only if every cell
// matches (style + rune + combining + cursor col). This means rows that
// differ only in SGR encoding (e.g. "\x1b[31m" vs "\x1b[38;5;1m") but
// resolve to the same cell style are now treated as equal — the renderer
// skips them, saving bandwidth that the string diff couldn't.
// ---------------------------------------------------------------------------

// RowDiff is one changed row in a frame diff.
type RowDiff struct {
	Row     int64
	Content Row
}

// DiffRows returns every row index at which old and new differ, or where
// new is longer than old. Rows present in old but missing in new are not
// returned (the caller clears trailing lines separately).
func DiffRows(old, new []Row) []RowDiff {
	var out []RowDiff
	for i, n := range new {
		if i >= len(old) {
			out = append(out, RowDiff{Row: int64(i), Content: n})
			continue
		}
		if !RowsEqual(old[i], n) {
			out = append(out, RowDiff{Row: int64(i), Content: n})
		}
	}
	return out
}
