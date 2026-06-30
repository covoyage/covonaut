package component

import (
	"sort"
	"strings"
	"sync"

	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/terminal"
	"github.com/covoyage/covonaut/tui/theme"
)

// ---------------------------------------------------------------------------
// KeyHelp — a Component that renders a two-column help panel listing every
// registered keybinding (grouped by the "category" prefix of their ID).
//
// Typical usage — overlay with PushOverlay:
//
//	helpComp := component.NewKeyHelp(km)
//	ov := tui.NewCenteredOverlay(helpComp, 70, 70)
//	tuiApp.PushOverlay(ov)
//
// The component itself has no input bindings — closing the overlay is the
// caller's responsibility (typically bound to Escape or the same key that
// opened it).
// ---------------------------------------------------------------------------

// KeyHelp renders the keybindings registered on a KeybindingsManager.
type KeyHelp struct {
	mu sync.RWMutex

	km       *terminal.KeybindingsManager
	title    string
	filter   string
	maxRows  int64
	offset   int64
	groupBy  string // prefix separator, default "."
	override map[string]string

	cacheWidth int64
	cacheLines []string
	dirty      bool
}

// NewKeyHelp constructs a KeyHelp bound to the given manager.
func NewKeyHelp(km *terminal.KeybindingsManager) *KeyHelp {
	return &KeyHelp{
		km:      km,
		title:   "Keybindings",
		groupBy: ".",
		dirty:   true,
	}
}

// SetTitle overrides the panel title (default "Keybindings").
func (h *KeyHelp) SetTitle(t string) {
	h.mu.Lock()
	h.title = t
	h.dirty = true
	h.mu.Unlock()
}

// SetFilter restricts the display to bindings whose ID or description
// contains the substring (case-insensitive). Empty string clears the filter.
func (h *KeyHelp) SetFilter(s string) {
	h.mu.Lock()
	h.filter = strings.ToLower(s)
	h.offset = 0
	h.dirty = true
	h.mu.Unlock()
}

// SetOverrideLabel maps an ID to a human label (e.g. "Quit application")
// shown instead of the ID. Useful when you want friendly names.
func (h *KeyHelp) SetOverrideLabel(id, label string) {
	h.mu.Lock()
	if h.override == nil {
		h.override = map[string]string{}
	}
	h.override[id] = label
	h.dirty = true
	h.mu.Unlock()
}

// SetMaxRows clamps the scrollable viewport.
func (h *KeyHelp) SetMaxRows(n int64) {
	h.mu.Lock()
	if h.maxRows == n {
		h.mu.Unlock()
		return
	}
	h.maxRows = n
	h.dirty = true
	h.mu.Unlock()
}

// ScrollBy pages through a long binding list.
func (h *KeyHelp) ScrollBy(delta int64) {
	h.mu.Lock()
	h.offset += delta
	if h.offset < 0 {
		h.offset = 0
	}
	h.dirty = true
	h.mu.Unlock()
}

// Render produces the help panel for the given width.
func (h *KeyHelp) Render(width int64) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.dirty && h.cacheWidth == width && h.cacheLines != nil {
		return h.cacheLines
	}

	h.cacheLines = h.renderLocked(width)
	h.cacheWidth = width
	h.dirty = false
	return h.cacheLines
}

// Invalidate forces a re-render on next frame.
func (h *KeyHelp) Invalidate() {
	h.mu.Lock()
	h.dirty = true
	h.mu.Unlock()
}

func (h *KeyHelp) Update(msg core.Msg) core.Cmd {
	switch m := msg.(type) {
	case core.KeyMsg:
		data := m.Data
		switch {
		case terminal.MatchesKey(data, "up"):
			h.ScrollBy(-1)
		case terminal.MatchesKey(data, "down"):
			h.ScrollBy(1)
		case terminal.MatchesKey(data, "pgup"):
			h.ScrollBy(-5)
		case terminal.MatchesKey(data, "pgdown"):
			h.ScrollBy(5)
		}
	case core.WindowSizeMsg:
		h.Invalidate()
	}
	return nil
}

// ---------------------------------------------------------------------------
// Internals
// ---------------------------------------------------------------------------

type kbRow struct {
	group string
	id    string
	label string
	keys  string
	desc  string
}

func (h *KeyHelp) collectRows() []kbRow {
	if h.km == nil {
		return nil
	}
	all := h.km.All()
	rows := make([]kbRow, 0, len(all))
	for id, keys := range all {
		def := h.km.Definition(id)
		group, rest := splitGroup(id, h.groupBy)
		label := rest
		if v, ok := h.override[id]; ok {
			label = v
		}
		rows = append(rows, kbRow{
			group: group,
			id:    id,
			label: label,
			keys:  formatKeyList(keys),
			desc:  def.Description,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].group != rows[j].group {
			return rows[i].group < rows[j].group
		}
		return rows[i].id < rows[j].id
	})
	if h.filter != "" {
		out := rows[:0]
		for _, r := range rows {
			if strings.Contains(strings.ToLower(r.id), h.filter) ||
				strings.Contains(strings.ToLower(r.desc), h.filter) ||
				strings.Contains(strings.ToLower(r.keys), h.filter) {
				out = append(out, r)
			}
		}
		rows = out
	}
	return rows
}

func (h *KeyHelp) renderLocked(width int64) []string {
	rows := h.collectRows()

	// Compute key column width.
	var keyCol int64
	for _, r := range rows {
		if w := core.VisibleWidth(r.keys); w > keyCol {
			keyCol = w
		}
	}
	if keyCol < 8 {
		keyCol = 8
	}
	if keyCol > width/2 {
		keyCol = width / 2
	}
	labelCol := width - keyCol - 3 // 3 = " • "
	if labelCol < 10 {
		labelCol = 10
	}

	var out []string
	pal := theme.CurrentPalette()
	if h.title != "" {
		out = append(out, pal.User.Render(h.title))
		out = append(out, pal.Dim.Render(strings.Repeat("─", int(width))))
	}
	lastGroup := ""
	for _, r := range rows {
		if r.group != lastGroup {
			if lastGroup != "" {
				out = append(out, "")
			}
			out = append(out, pal.Dim.Italic().Render(r.group))
			lastGroup = r.group
		}
		keys := core.PadToWidth(pal.User.Render(r.keys), keyCol)
		desc := r.desc
		if desc == "" {
			desc = r.label
		}
		line := keys + pal.Dim.Render(" • ") + core.TruncateToWidth(desc, labelCol, "…")
		out = append(out, line)
	}

	// Pagination.
	if h.maxRows > 0 && int64(len(out)) > h.maxRows {
		end := int64(len(out))
		start := h.offset
		if start > end-h.maxRows {
			start = end - h.maxRows
			if start < 0 {
				start = 0
			}
		}
		if start < 0 {
			start = 0
		}
		stop := start + h.maxRows
		if stop > end {
			stop = end
		}
		out = out[start:stop]
	}
	return out
}

func splitGroup(id, sep string) (string, string) {
	idx := strings.LastIndex(id, sep)
	if idx < 0 {
		return id, id
	}
	return id[:idx], id[idx+1:]
}

func formatKeyList(keys []terminal.KeyID) string {
	if len(keys) == 0 {
		return "—"
	}
	return strings.Join(keys, " / ")
}
