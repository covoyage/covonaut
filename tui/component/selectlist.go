package component

import (
	"fmt"
	"sync"

	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/terminal"
	"github.com/covoyage/covonaut/tui/theme"
)

// ---------------------------------------------------------------------------
// SelectList — interactive keyboard-driven selection list.
//
// Features:
//   - Arrow keys / Ctrl+P / Ctrl+N navigation.
//   - Enter confirms, Escape cancels.
//   - Optional fuzzy filter (SetFilter).
//   - Viewport scrolling when list exceeds MaxVisible.
//   - OnSelectionChange fires as the highlight moves.
// ---------------------------------------------------------------------------

// SelectItem represents a single row in a SelectList.
type SelectItem struct {
	Value       string
	Label       string
	Description string
}

// SelectListTheme overrides styling.
type SelectListTheme struct {
	SelectedPrefixFn func(string) string
	SelectedTextFn   func(string) string
	DescriptionFn    func(string) string
	ScrollInfoFn     func(string) string
	NoMatchFn        func(string) string
	MatchHighlightFn func(string) string // highlights matched runes when filtering
}

// SelectList is a Focusable component.
type SelectList struct {
	mu sync.RWMutex

	items      []SelectItem
	filtered   []filteredItem
	filter     string
	maxVisible int64
	cursor     int64
	scroll     int64
	focused    bool
	theme      SelectListTheme
	km         *terminal.KeybindingsManager

	onSelect          func(SelectItem)
	onCancel          func()
	onSelectionChange func(SelectItem)
}

type filteredItem struct {
	item    SelectItem
	indexes []int64
}

// NewSelectList creates a list with a default visible window of 10 rows.
func NewSelectList(items []SelectItem) *SelectList {
	sl := &SelectList{
		items:      items,
		maxVisible: 10,
		km:         terminal.GetGlobalKeybindings(),
	}
	sl.rebuildFiltered()
	return sl
}

// SetMaxVisible caps the number of rows drawn (minimum 3).
func (s *SelectList) SetMaxVisible(n int64) {
	if n < 3 {
		n = 3
	}
	s.mu.Lock()
	s.maxVisible = n
	s.mu.Unlock()
}

// SetTheme installs a custom theme.
func (s *SelectList) SetTheme(t SelectListTheme) { s.mu.Lock(); s.theme = t; s.mu.Unlock() }

// SetKeybindings overrides the default keybinding manager.
func (s *SelectList) SetKeybindings(km *terminal.KeybindingsManager) {
	if km == nil {
		km = terminal.GetGlobalKeybindings()
	}
	s.mu.Lock()
	s.km = km
	s.mu.Unlock()
}

// SetItems replaces the underlying list.
func (s *SelectList) SetItems(items []SelectItem) {
	s.mu.Lock()
	s.items = items
	s.cursor = 0
	s.scroll = 0
	s.mu.Unlock()
	s.rebuildFiltered()
}

// SetFilter applies a fuzzy filter (empty = no filter).
func (s *SelectList) SetFilter(q string) {
	s.mu.Lock()
	s.filter = q
	s.cursor = 0
	s.scroll = 0
	s.mu.Unlock()
	s.rebuildFiltered()
	s.fireChange()
}

// OnSelect registers the confirmation callback.
func (s *SelectList) OnSelect(fn func(SelectItem)) { s.mu.Lock(); s.onSelect = fn; s.mu.Unlock() }

// OnCancel registers the cancel callback.
func (s *SelectList) OnCancel(fn func()) { s.mu.Lock(); s.onCancel = fn; s.mu.Unlock() }

// OnSelectionChange fires whenever the highlight moves.
func (s *SelectList) OnSelectionChange(fn func(SelectItem)) {
	s.mu.Lock()
	s.onSelectionChange = fn
	s.mu.Unlock()
}

// CurrentItem returns the currently highlighted item (zero value if empty).
func (s *SelectList) CurrentItem() (SelectItem, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cursor < 0 || s.cursor >= int64(len(s.filtered)) {
		return SelectItem{}, false
	}
	return s.filtered[s.cursor].item, true
}

// SetFocused / IsFocused implement Focusable.
func (s *SelectList) SetFocused(on bool) { s.mu.Lock(); s.focused = on; s.mu.Unlock() }
func (s *SelectList) IsFocused() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.focused
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

// Render draws up to MaxVisible rows plus optional scroll-info footer.
func (s *SelectList) Render(width int64) []string {
	s.mu.RLock()
	items := s.filtered
	cursor := s.cursor
	scroll := s.scroll
	maxV := s.maxVisible
	filter := s.filter
	sltheme := s.theme
	s.mu.RUnlock()

	var out []string

	if len(items) == 0 {
		msg := "(no matches)"
		if filter == "" {
			msg = "(no items)"
		}
		if sltheme.NoMatchFn != nil {
			msg = sltheme.NoMatchFn(msg)
		} else {
			msg = theme.CurrentPalette().Dim.Render(msg)
		}
		out = append(out, core.PadToWidth(msg, width))
		return out
	}

	visible := maxV
	if int64(len(items)) < visible {
		visible = int64(len(items))
	}
	end := scroll + visible
	if end > int64(len(items)) {
		end = int64(len(items))
	}

	for i := scroll; i < end; i++ {
		fi := items[i]
		prefix := "  "
		labelFn := func(x string) string { return x }
		if i == cursor {
			prefix = "> "
			if sltheme.SelectedPrefixFn != nil {
				prefix = sltheme.SelectedPrefixFn(prefix)
			} else {
				prefix = theme.CurrentPalette().SelectHighlight.Render(prefix)
			}
			if sltheme.SelectedTextFn != nil {
				labelFn = sltheme.SelectedTextFn
			} else {
				labelFn = func(x string) string { return theme.CurrentPalette().SelectHighlight.Render(x) }
			}
		}
		label := fi.item.Label
		if filter != "" && sltheme.MatchHighlightFn != nil {
			label = core.HighlightMatches(label, fi.indexes, sltheme.MatchHighlightFn)
		}
		line := prefix + labelFn(label)
		if fi.item.Description != "" {
			descFn := theme.CurrentPalette().Dim.Render
			if sltheme.DescriptionFn != nil {
				descFn = sltheme.DescriptionFn
			}
			line += "  " + descFn(fi.item.Description)
		}
		out = append(out, core.PadToWidth(core.TruncateToWidth(line, width, "…"), width))
	}

	if int64(len(items)) > maxV {
		info := fmt.Sprintf("  [%d/%d]", cursor+1, len(items))
		if sltheme.ScrollInfoFn != nil {
			info = sltheme.ScrollInfoFn(info)
		} else {
			info = theme.CurrentPalette().Dim.Render(info)
		}
		out = append(out, core.PadToWidth(info, width))
	}
	return out
}

// Invalidate is a no-op.
func (s *SelectList) Invalidate() {}

func (s *SelectList) Update(msg core.Msg) core.Cmd {
	switch m := msg.(type) {
	case core.KeyMsg:
		s.processKeys(m.Data)
	case core.WindowSizeMsg:
		s.Invalidate()
	}
	return nil
}

func (s *SelectList) processKeys(data string) {
	s.mu.RLock()
	km := s.km
	s.mu.RUnlock()

	switch {
	case km.Matches(data, "tui.select.up"):
		s.moveCursor(-1)
	case km.Matches(data, "tui.select.down"):
		s.moveCursor(1)
	case km.Matches(data, "tui.select.pageUp"):
		s.moveCursor(-s.pageSize())
	case km.Matches(data, "tui.select.pageDown"):
		s.moveCursor(s.pageSize())
	case km.Matches(data, "tui.select.confirm"):
		s.confirm()
	case km.Matches(data, "tui.select.cancel"):
		s.cancel()
	}
}

// ---------------------------------------------------------------------------
// Internal
// ---------------------------------------------------------------------------

func (s *SelectList) pageSize() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.maxVisible < 3 {
		return 1
	}
	return s.maxVisible - 1
}

func (s *SelectList) moveCursor(delta int64) {
	s.mu.Lock()
	n := int64(len(s.filtered))
	if n == 0 {
		s.mu.Unlock()
		return
	}
	s.cursor += delta
	if s.cursor < 0 {
		s.cursor = n - 1
	}
	if s.cursor >= n {
		s.cursor = 0
	}
	if s.cursor < s.scroll {
		s.scroll = s.cursor
	}
	if s.cursor >= s.scroll+s.maxVisible {
		s.scroll = s.cursor - s.maxVisible + 1
	}
	s.mu.Unlock()
	s.fireChange()
}

func (s *SelectList) confirm() {
	item, ok := s.CurrentItem()
	if !ok {
		return
	}
	s.mu.RLock()
	fn := s.onSelect
	s.mu.RUnlock()
	if fn != nil {
		fn(item)
	}
}

func (s *SelectList) cancel() {
	s.mu.RLock()
	fn := s.onCancel
	s.mu.RUnlock()
	if fn != nil {
		fn()
	}
}

func (s *SelectList) fireChange() {
	item, ok := s.CurrentItem()
	if !ok {
		return
	}
	s.mu.RLock()
	fn := s.onSelectionChange
	s.mu.RUnlock()
	if fn != nil {
		fn(item)
	}
}

func (s *SelectList) rebuildFiltered() {
	s.mu.Lock()
	items := s.items
	filter := s.filter
	s.mu.Unlock()

	if filter == "" {
		out := make([]filteredItem, len(items))
		for i, it := range items {
			out[i] = filteredItem{item: it}
		}
		s.mu.Lock()
		s.filtered = out
		if s.cursor >= int64(len(out)) {
			s.cursor = 0
			if len(out) > 0 {
				s.cursor = 0
			}
		}
		s.mu.Unlock()
		return
	}

	// Build match candidates from labels.
	labels := make([]string, len(items))
	for i, it := range items {
		labels[i] = it.Label
	}
	matches := core.FuzzyFilter(filter, labels)

	labelIndex := make(map[string]int)
	for i, l := range labels {
		if _, ok := labelIndex[l]; !ok {
			labelIndex[l] = i
		}
	}
	out := make([]filteredItem, 0, len(matches))
	for _, m := range matches {
		idx, ok := labelIndex[m.Original]
		if !ok {
			continue
		}
		out = append(out, filteredItem{item: items[idx], indexes: m.Indexes})
	}

	s.mu.Lock()
	s.filtered = out
	if s.cursor >= int64(len(out)) {
		s.cursor = 0
	}
	if s.scroll >= int64(len(out)) {
		s.scroll = 0
	}
	s.mu.Unlock()
}
