package component

import (
	"strings"
	"sync"

	"github.com/covoyage/covonaut/tui/core"
)

// Column defines a single column in a Table.
type Column struct {
	// Weight for proportional distribution. All non-zero weights are summed;
	// each column gets totalWidth * Weight / sumWeight. 0 means "fill rest".
	Weight int64

	// MinWidth ensures the column is never narrower than this (where possible).
	MinWidth int64

	// MaxWidth caps the column width. When set, the column never exceeds this
	// even if proportional distribution would give more. The difference is
	// left as gap — the table row may be shorter than totalWidth.
	MaxWidth int64

	// Render returns the cell content for the given row at the given column width.
	Render func(rowIdx int, colWidth int64) string
}

// TableTheme controls the visual style of table rows.
type TableTheme struct {
	RowStyle core.Style
	SelStyle core.Style
	DimStyle core.Style
	EmptyMsg string
}

// Table is a reusable interactive table component. It handles column layout,
// cell rendering, selection management, and keyboard navigation.
// It does NOT include chrome (title, search bar, footer, etc.) — the caller
// adds those around Table's output.
type Table struct {
	mu      sync.RWMutex
	columns []Column
	items   []interface{}
	sel     int
	scroll  int
	theme   TableTheme

	onSelect func(int)
	onCancel func()
}

// NewTable creates a Table with default theme.
func NewTable() *Table {
	return &Table{
		theme: TableTheme{EmptyMsg: "No items"},
	}
}

func (t *Table) SetColumns(cols []Column) {
	t.mu.Lock()
	t.columns = cols
	t.mu.Unlock()
}

func (t *Table) SetItems(items []interface{}) {
	t.mu.Lock()
	t.items = items
	t.sel = 0
	t.scroll = 0
	t.mu.Unlock()
}

func (t *Table) SetTheme(th TableTheme) {
	t.mu.Lock()
	t.theme = th
	t.mu.Unlock()
}

func (t *Table) OnSelect(fn func(int)) { t.mu.Lock(); t.onSelect = fn; t.mu.Unlock() }
func (t *Table) OnCancel(fn func())     { t.mu.Lock(); t.onCancel = fn; t.mu.Unlock() }

func (t *Table) Selected() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.sel
}

// MoveSelected moves the selection by delta (wraps around).
func (t *Table) MoveSelected(delta int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := len(t.items)
	if n == 0 {
		return
	}
	t.sel += delta
	for t.sel < 0 {
		t.sel += n
	}
	for t.sel >= n {
		t.sel -= n
	}
	t.clampScrollLocked()
}

// Confirm fires the onSelect callback for the current selection.
func (t *Table) Confirm() {
	t.mu.RLock()
	fn := t.onSelect
	sel := t.sel
	t.mu.RUnlock()
	if fn != nil && sel >= 0 && sel < len(t.items) {
		fn(sel)
	}
}

// Cancel fires the onCancel callback.
func (t *Table) Cancel() {
	t.mu.RLock()
	fn := t.onCancel
	t.mu.RUnlock()
	if fn != nil {
		fn()
	}
}

// ItemCount returns the total number of items.
func (t *Table) ItemCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.items)
}

// clampScrollLocked adjusts scroll so the selection stays visible.
func (t *Table) clampScrollLocked() {
	if t.sel < t.scroll {
		t.scroll = t.sel
	}
	// maxVisible isn't known here — the caller renders with a height.
}

// SetScroll adjusts the scroll offset (clamped to valid range).
func (t *Table) SetScroll(s int) {
	t.mu.Lock()
	n := len(t.items)
	if s < 0 {
		s = 0
	}
	if s >= n {
		s = n - 1
		if s < 0 {
			s = 0
		}
	}
	t.scroll = s
	t.mu.Unlock()
}

// Scroll returns the current scroll offset.
func (t *Table) Scroll() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.scroll
}

// ---------------------------------------------------------------------------
// Layout & rendering helpers
// ---------------------------------------------------------------------------

// ColWidths computes column widths from the total available width.
// Columns with Weight>0 get proportional allocations; Weight==0 fills
// remaining space. If the flexible column's computed width would be less
// than its MinWidth, all columns are redistributed proportionally.
func (t *Table) ColWidths(total int64) []int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()

	n := len(t.columns)
	if n == 0 {
		return nil
	}

	widths := make([]int64, n)

	// Collect stats.
	var sumWeight int64
	for _, c := range t.columns {
		if c.Weight > 0 {
			sumWeight += c.Weight
		}
	}

	// Pass 1: assign proportional widths.
	var used int64
	restIdx := -1
	for i, c := range t.columns {
		if c.Weight > 0 && sumWeight > 0 {
			w := total * c.Weight / sumWeight
			if c.MinWidth > 0 && w < c.MinWidth {
				w = c.MinWidth
			}
			if c.MaxWidth > 0 && w > c.MaxWidth {
				w = c.MaxWidth
			}
			widths[i] = w
			used += w
		} else {
			restIdx = i
		}
	}

	// Pass 2: assign rest / handle overflow.
	if restIdx >= 0 {
		rest := total - used
		col := &t.columns[restIdx]
		if col.MinWidth > 0 && rest < col.MinWidth {
			rest = col.MinWidth
		}
		if col.MaxWidth > 0 && rest > col.MaxWidth {
			rest = col.MaxWidth
		}
		widths[restIdx] = rest
		used += rest
	} else if used < total {
		// Distribute rounding remainder to the last column.
		for i := len(widths) - 1; i >= 0; i-- {
			colsRem := total - used
			if colsRem <= 0 {
				break
			}
			add := colsRem
			if t.columns[i].MaxWidth > 0 && widths[i]+add > t.columns[i].MaxWidth {
				add = t.columns[i].MaxWidth - widths[i]
			}
			if add < 0 {
				add = 0
			}
			widths[i] += add
			used += add
		}
	}

	return widths
}

// RenderRow returns a single row's cell content (without prefix/selection style).
// Each column is padded/truncated to its computed width. The total visible width
// may be less than `total` when MaxWidth constraints are in effect — the caller
// should pad/center as needed.
func (t *Table) RenderRow(rowIdx int, total int64) string {
	widths := t.ColWidths(total)
	var b strings.Builder
	for ci, col := range t.columns {
		content := col.Render(rowIdx, widths[ci])
		vw := int64(core.VisibleWidth(content))
		if vw > widths[ci] {
			content = core.TruncateToWidth(content, widths[ci], "…")
		} else if vw < widths[ci] {
			content += strings.Repeat(" ", int(widths[ci]-vw))
		}
		b.WriteString(content)
	}
	return b.String()
}

// RowWidth returns the actual visible width of a row rendered at total.
// Useful when MaxWidth constraints cause the row to be shorter than total.
func (t *Table) RowWidth(total int64) int64 {
	widths := t.ColWidths(total)
	var s int64
	for _, w := range widths {
		s += w
	}
	return s
}

// Item returns the item at the given index (nil if out of range).
func (t *Table) Item(idx int) interface{} {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if idx < 0 || idx >= len(t.items) {
		return nil
	}
	return t.items[idx]
}

// Items returns all items.
func (t *Table) Items() []interface{} {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]interface{}, len(t.items))
	copy(out, t.items)
	return out
}

// VisibleRange returns the (start, end) indices for items visible given
// the maximum number of visible rows. Caller adjusts maxVisible to account
// for chrome rows (title, search, footer, etc.).
func (t *Table) VisibleRange(maxVisible int) (start, end int) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	n := len(t.items)
	if n == 0 || maxVisible <= 0 {
		return 0, 0
	}

	// Clamp scroll so selection is visible.
	if t.sel < t.scroll {
		t.scroll = t.sel
	}
	if t.sel >= t.scroll+maxVisible {
		t.scroll = t.sel - maxVisible + 1
	}
	if t.scroll < 0 {
		t.scroll = 0
	}

	start = t.scroll
	end = start + maxVisible
	if end > n {
		end = n
	}
	return
}

// Theme returns the current theme (for callers that need to style around table).
func (t *Table) Theme() TableTheme {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.theme
}
