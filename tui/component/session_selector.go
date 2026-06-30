package component

import (
	"fmt"
	"strings"
	"sync"

	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/terminal"
	"github.com/covoyage/covonaut/tui/theme"
)

// SessionItem represents a session in the selector.
type SessionItem struct {
	ID            string
	Name          string
	Label         string
	ParentSession string
	CreatedAt     string
	UpdatedAt     string
	MsgCount      int64
	IsCurrent     bool
	Preview       string
}

// SessionSelector is an interactive session picker panel.
type SessionSelector struct {
	mu       sync.RWMutex
	items    []SessionItem
	filtered []SessionItem
	filter   string
	onSelect func(SessionItem)
	onCancel func()
	onDelete func(SessionItem)
	onRename func(SessionItem, string)
	theme    SessionSelectorTheme
	height   int
	km       *terminal.KeybindingsManager
	table    *Table

	renameMode bool
	renameBuf  string
	renameItem *SessionItem

	focusMode  bool
	deleteMode bool
}

// SessionSelectorTheme customizes the selector appearance.
type SessionSelectorTheme struct {
	Title         string
	SearchStyle   theme.Style
	HeaderStyle   theme.Style
	ItemStyle     theme.Style
	SelectedStyle theme.Style
	CurrentStyle  theme.Style
	DimStyle      theme.Style
}

// DefaultSessionSelectorTheme returns the default theme.
func DefaultSessionSelectorTheme() SessionSelectorTheme {
	pal := theme.CurrentPalette()
	return SessionSelectorTheme{
		Title:         "Sessions",
		SearchStyle:   pal.Accent,
		HeaderStyle:   pal.Accent,
		ItemStyle:     pal.Assistant,
		SelectedStyle: pal.SelectHighlight,
		CurrentStyle:  pal.Success,
		DimStyle:      pal.Dim,
	}
}

// NewSessionSelector creates a new session selector.
func NewSessionSelector() *SessionSelector {
	km := terminal.NewKeybindingsManager(map[string]terminal.KeybindingDef{
		"session.up":      {DefaultKeys: []string{"up", "ctrl+p"}},
		"session.down":    {DefaultKeys: []string{"down", "ctrl+n"}},
		"session.confirm": {DefaultKeys: []string{"enter"}},
		"session.cancel":  {DefaultKeys: []string{"esc"}},
		"session.delete":  {DefaultKeys: []string{"ctrl+x"}},
		"session.rename":  {DefaultKeys: []string{"ctrl+r"}},
		"session.filter":  {DefaultKeys: []string{"/"}},
	})
	tbl := NewTable()
	tbl.SetColumns([]Column{
		{Weight: 3, Render: func(idx int, w int64) string {
			item := tbl.Item(idx).(SessionItem)
			id := item.ID
			if len(id) > 8 {
				id = id[:8]
			}
			fork := ""
			if item.ParentSession != "" {
				pid := item.ParentSession
				if len(pid) > 8 {
					pid = pid[:8]
				}
				fork = " ↤" + pid
			}
			return id + fork
		}},
		{Weight: 3, Render: func(idx int, w int64) string {
			item := tbl.Item(idx).(SessionItem)
			ts := item.UpdatedAt
			if ts == "" {
				ts = item.CreatedAt
			}
			return ts
		}},
		{Weight: 2, Render: func(idx int, w int64) string {
			item := tbl.Item(idx).(SessionItem)
			return fmt.Sprintf("%d msgs", item.MsgCount)
		}},
		{Weight: 4, Render: func(idx int, w int64) string {
			item := tbl.Item(idx).(SessionItem)
			name := item.Name
			if name == "" {
				name = item.ID
				if len(name) > 8 {
					name = name[:8]
				}
			}
			label := ""
			if item.Label != "" {
				label = " [" + item.Label + "]"
			}
			return name + label
		}},
	})
	return &SessionSelector{
		theme: DefaultSessionSelectorTheme(),
		km:    km,
		table: tbl,
	}
}

// SetTitle sets the title.
func (s *SessionSelector) SetTitle(t string) {
	s.mu.Lock()
	s.theme.Title = t
	s.mu.Unlock()
}

// SetItems sets the session items to display.
func (s *SessionSelector) SetItems(items []SessionItem) {
	s.mu.Lock()
	s.items = items
	s.filter = ""
	s.focusMode = false
	s.applyFilterLocked()
	s.mu.Unlock()
}

// SetOnSelect sets the callback when a session is selected.
func (s *SessionSelector) SetOnSelect(fn func(SessionItem)) {
	s.mu.Lock()
	s.onSelect = fn
	s.mu.Unlock()
}

// SetOnCancel sets the callback when selection is cancelled.
func (s *SessionSelector) SetOnCancel(fn func()) {
	s.mu.Lock()
	s.onCancel = fn
	s.mu.Unlock()
}

// SetOnDelete sets the callback when a session is deleted.
func (s *SessionSelector) SetOnDelete(fn func(SessionItem)) {
	s.mu.Lock()
	s.onDelete = fn
	s.mu.Unlock()
}

// SetOnRename sets the callback when a session is renamed.
func (s *SessionSelector) SetOnRename(fn func(SessionItem, string)) {
	s.mu.Lock()
	s.onRename = fn
	s.mu.Unlock()
}

// SetAvailableHeight sets the available rendering height for the list.
func (s *SessionSelector) SetAvailableHeight(h int) {
	s.mu.Lock()
	s.height = h
	s.mu.Unlock()
}

func (s *SessionSelector) applyFilterLocked() {
	if s.filter == "" {
		s.filtered = make([]SessionItem, len(s.items))
		copy(s.filtered, s.items)
	} else {
		lower := strings.ToLower(s.filter)
		s.filtered = s.filtered[:0]
		for _, item := range s.items {
			if strings.Contains(strings.ToLower(item.Name), lower) ||
				strings.Contains(strings.ToLower(item.ID), lower) ||
				strings.Contains(strings.ToLower(item.Label), lower) ||
				strings.Contains(strings.ToLower(item.Preview), lower) {
				s.filtered = append(s.filtered, item)
			}
		}
	}
	// Sync table items.
	iface := make([]interface{}, len(s.filtered))
	for i, it := range s.filtered {
		iface[i] = it
	}
	s.table.SetItems(iface)
}

// HandleEsc intercepts ESC for inline modes (search, rename).
// Returns true if ESC was consumed (mode cancelled), false if no mode active.
func (s *SessionSelector) HandleEsc() bool {
	s.mu.Lock()
	if s.focusMode {
		s.filter = ""
		s.applyFilterLocked()
		s.focusMode = false
		s.mu.Unlock()
		return true
	}
	if s.renameMode {
		s.renameMode = false
		s.renameBuf = ""
		s.renameItem = nil
		s.mu.Unlock()
		return true
	}
	s.mu.Unlock()
	return false
}

// Invalidate is a no-op.
func (s *SessionSelector) Invalidate() {}

// Update processes messages.
func (s *SessionSelector) Update(msg core.Msg) core.Cmd {
	switch m := msg.(type) {
	case core.KeyMsg:
		s.processKeys(m.Data)
	}
	return nil
}

func (s *SessionSelector) processKeys(data string) {
	s.mu.RLock()
	km := s.km
	fm := s.focusMode
	rn := s.renameMode
	s.mu.RUnlock()

	if rn {
		s.handleRenameInput(data)
		return
	}

	if fm {
		s.handleFocusInput(data)
		return
	}

	switch {
	case km.Matches(data, "session.up"):
		s.table.MoveSelected(-1)
	case km.Matches(data, "session.down"):
		s.table.MoveSelected(1)
	case km.Matches(data, "session.confirm"):
		s.confirm()
	case km.Matches(data, "session.cancel"):
		s.cancel()
	case km.Matches(data, "session.filter"):
		s.startFocus()
	case km.Matches(data, "session.delete"):
		s.confirmDelete()
	case km.Matches(data, "session.rename"):
		s.startRename()
	}
}

func (s *SessionSelector) handleFocusInput(data string) {
	if terminal.MatchesKey(data, "enter") {
		s.endFocus()
		s.confirm()
		return
	}
	if terminal.MatchesKey(data, "backspace") {
		s.mu.Lock()
		if len(s.filter) > 0 {
			s.filter = s.filter[:len(s.filter)-1]
			s.applyFilterLocked()
		}
		s.mu.Unlock()
		return
	}
	if len(data) == 1 && data[0] >= 32 && data[0] < 127 {
		s.mu.Lock()
		s.filter += data
		s.applyFilterLocked()
		s.mu.Unlock()
	}
}

func (s *SessionSelector) startFocus() {
	s.mu.Lock()
	s.focusMode = true
	s.mu.Unlock()
}

func (s *SessionSelector) endFocus() {
	s.mu.Lock()
	s.focusMode = false
	s.mu.Unlock()
}

func (s *SessionSelector) handleRenameInput(data string) {
	if terminal.MatchesKey(data, "enter") {
		s.mu.RLock()
		item := *s.renameItem
		buf := s.renameBuf
		s.mu.RUnlock()
		if buf != "" && s.onRename != nil {
			go s.onRename(item, buf)
		}
		s.endRename()
		return
	}
	if terminal.MatchesKey(data, "escape") {
		s.endRename()
		return
	}
	if terminal.MatchesKey(data, "backspace") {
		s.mu.Lock()
		if len(s.renameBuf) > 0 {
			s.renameBuf = s.renameBuf[:len(s.renameBuf)-1]
		}
		s.mu.Unlock()
		return
	}
	if len(data) == 1 && data[0] >= 32 && data[0] < 127 {
		s.mu.Lock()
		s.renameBuf += data
		s.mu.Unlock()
	}
}

func (s *SessionSelector) startRename() {
	s.mu.Lock()
	defer s.mu.Unlock()
	sel := s.table.Selected()
	if len(s.filtered) == 0 || sel < 0 || sel >= len(s.filtered) {
		return
	}
	item := s.filtered[sel]
	s.renameMode = true
	s.renameBuf = item.Name
	s.renameItem = &item
}

func (s *SessionSelector) endRename() {
	s.mu.Lock()
	s.renameMode = false
	s.renameBuf = ""
	s.renameItem = nil
	s.mu.Unlock()
}

func (s *SessionSelector) confirm() {
	s.mu.RLock()
	sel := s.table.Selected()
	items := s.filtered
	fn := s.onSelect
	s.mu.RUnlock()
	if len(items) > 0 && sel >= 0 && sel < len(items) && fn != nil {
		go fn(items[sel])
	}
}

func (s *SessionSelector) cancel() {
	s.mu.RLock()
	fn := s.onCancel
	s.mu.RUnlock()
	if fn != nil {
		go fn()
	}
}

func (s *SessionSelector) confirmDelete() {
	s.mu.RLock()
	sel := s.table.Selected()
	items := s.filtered
	fn := s.onDelete
	s.mu.RUnlock()
	if sel < 0 || sel >= len(items) || fn == nil {
		return
	}
	go fn(items[sel])
}

// Render draws the session selector.
func (s *SessionSelector) Render(width int64) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if width < 1 {
		width = 1
	}

	var lines []string

	// Title
	title := core.PadToWidth(s.theme.Title, width)
	lines = append(lines, s.theme.HeaderStyle.Render(title))
	lines = append(lines, s.theme.DimStyle.Render(strings.Repeat("─", int(width))))

	// Search bar
	if s.focusMode {
		searchText := s.filter
		if searchText == "" {
			searchText = "type to search..."
		}
		searchBar := core.PadToWidth("  / "+searchText+"█", width)
		lines = append(lines, s.theme.SearchStyle.Render(searchBar))
	} else if s.filter != "" {
		filterText := core.PadToWidth("  filter: "+s.filter, width)
		lines = append(lines, s.theme.DimStyle.Render(filterText))
	}

	// Count
	countLine := fmt.Sprintf("  %d sessions", len(s.items))
	if len(s.filtered) != len(s.items) {
		countLine = fmt.Sprintf("  %d of %d sessions", len(s.filtered), len(s.items))
	}
	countLine = core.PadToWidth(countLine, width)
	lines = append(lines, s.theme.DimStyle.Render(countLine))

	if s.renameMode {
		lines = append(lines, "")
		header := core.PadToWidth("  Rename session:", width)
		lines = append(lines, s.theme.HeaderStyle.Render(header))
		prompt := core.PadToWidth("  > "+s.renameBuf+"█", width)
		lines = append(lines, prompt)
		hint := core.PadToWidth("    Enter to confirm, Esc to cancel", width)
		lines = append(lines, s.theme.DimStyle.Render(hint))
		avail := s.calcHeight() - int64(len(lines))
		for i := int64(0); i < avail; i++ {
			lines = append(lines, "")
		}
		return lines
	}

	// Items
	maxVisible := int(s.calcHeight() - 8)
	if maxVisible < 4 {
		maxVisible = 4
	}

	// Indent the table to match the panel's other rows (title/footer use a
	// 2-space indent). A small margin keeps the table filling the overlay
	// width instead of sitting as a narrow centered block.
	margin := int64(2)
	tableWidth := width - margin*2
	if tableWidth < 1 {
		tableWidth = 1
	}
	start, end := s.table.VisibleRange(maxVisible)
	for i := start; i < end; i++ {
		item := s.filtered[i]
		row := s.table.RenderRow(i, tableWidth)
		var prefix string
		if i == s.table.Selected() {
			prefix = "▸ "
		} else if item.IsCurrent {
			prefix = "● "
		} else {
			prefix = "  "
		}
		// Left-align the table at the panel indent and pad to the full width so
		// every row fills the overlay edge-to-edge (consistent with title/footer).
		full := prefix + row
		if int64(core.VisibleWidth(full)) > width {
			full = core.TruncateToWidth(full, width, "…")
		} else {
			full = core.PadToWidth(full, width)
		}
		if i == s.table.Selected() {
			lines = append(lines, s.theme.SelectedStyle.Render(full))
		} else if item.IsCurrent {
			lines = append(lines, s.theme.CurrentStyle.Render(full))
		} else {
			lines = append(lines, s.theme.ItemStyle.Render(full))
		}
		if item.Preview != "" {
			pl := item.Preview
			plw := int64(core.VisibleWidth(pl))
			if plw > tableWidth {
				pl = core.TruncateToWidth(pl, tableWidth, "…")
			}
			mleft := strings.Repeat(" ", int(margin))
			full := mleft + pl
			full = core.PadToWidth(full, width)
			lines = append(lines, s.theme.DimStyle.Render(full))
		}
	}

	if len(s.filtered) == 0 {
		msg := core.PadToWidth("  No sessions found", width)
		lines = append(lines, s.theme.DimStyle.Render(msg))
	}

	// Fill remaining space
	for int64(len(lines)) < s.calcHeight() {
		lines = append(lines, "")
	}

	// Footer
	if len(lines) > 0 {
		lines = append(lines, s.theme.DimStyle.Render(strings.Repeat("─", int(width))))
		footerText := core.PadToWidth("/ search  ↑↓ nav  Enter resume  Ctrl+X delete  Ctrl+R rename  Esc close", width)
		lines = append(lines, s.theme.DimStyle.Render("  "+footerText))
	}

	return lines
}

func (s *SessionSelector) calcHeight() int64 {
	h := int64(s.height)
	if h < 10 {
		h = 24
	}
	return h
}
