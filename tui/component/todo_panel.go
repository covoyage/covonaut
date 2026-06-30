package component

import (
	"fmt"
	"strings"
	"sync"

	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/terminal"
	"github.com/covoyage/covonaut/tui/theme"
)

// TodoItem represents a task in the TODO panel.
type TodoItem struct {
	ID       string
	Content  string
	Status   string
	Priority string
}

// TodoPanel is a visual TODO list panel.
type TodoPanel struct {
	mu            sync.RWMutex
	items         []TodoItem
	selected      int
	dataProvider  func() []TodoItem
	theme         TodoPanelTheme
	width         int64
	height        int64
	km            *terminal.KeybindingsManager
	onToggle      func(TodoItem)
	onInvalidate  func()
}

// TodoPanelTheme customizes the panel appearance.
type TodoPanelTheme struct {
	Title         string
	HeaderStyle   theme.Style
	ItemStyle     theme.Style
	SelectedStyle theme.Style
	PendingStyle  theme.Style
	InProgressStyle theme.Style
	CompletedStyle theme.Style
	CancelledStyle theme.Style
	DimStyle      theme.Style
}

// DefaultTodoPanelTheme returns the default theme.
func DefaultTodoPanelTheme() TodoPanelTheme {
	pal := theme.CurrentPalette()
	return TodoPanelTheme{
		Title:         "TODO (↑↓ navigate, Space/Enter toggle, Esc close)",
		HeaderStyle:   pal.Accent,
		ItemStyle:     pal.Assistant,
		SelectedStyle: pal.SelectHighlight,
		PendingStyle:  pal.Assistant,
		InProgressStyle: pal.Accent,
		CompletedStyle: pal.Success,
		CancelledStyle: pal.Dim,
		DimStyle:      pal.Dim,
	}
}

// NewTodoPanel creates a new TODO panel.
func NewTodoPanel() *TodoPanel {
	km := terminal.NewKeybindingsManager(map[string]terminal.KeybindingDef{
		"todo.up":     {DefaultKeys: []string{"up", "ctrl+p"}},
		"todo.down":   {DefaultKeys: []string{"down", "ctrl+n"}},
		"todo.toggle": {DefaultKeys: []string{"enter", "space"}},
		"todo.close":  {DefaultKeys: []string{"esc"}},
	})
	return &TodoPanel{
		theme: DefaultTodoPanelTheme(),
		km:    km,
	}
}

// SetTitle sets the title.
func (t *TodoPanel) SetTitle(title string) {
	t.mu.Lock()
	t.theme.Title = title
	t.mu.Unlock()
}

// SetItems sets the TODO items to display.
func (t *TodoPanel) SetItems(items []TodoItem) {
	t.mu.Lock()
	t.items = items
	t.clampSelectedLocked()
	t.mu.Unlock()
}

// clampSelectedLocked keeps the cursor within bounds. Caller must hold t.mu.
func (t *TodoPanel) clampSelectedLocked() {
	if t.selected < 0 {
		t.selected = 0
	}
	if n := len(t.items); t.selected >= n {
		if n == 0 {
			t.selected = 0
		} else {
			t.selected = n - 1
		}
	}
}

// SetOnToggle sets the callback when a TODO item is toggled.
func (t *TodoPanel) SetOnToggle(fn func(TodoItem)) {
	t.mu.Lock()
	t.onToggle = fn
	t.mu.Unlock()
}

// SetDataProvider sets the function to fetch items.
func (t *TodoPanel) SetDataProvider(fn func() []TodoItem) {
	t.mu.Lock()
	t.dataProvider = fn
	t.mu.Unlock()
}

// SetOnInvalidate sets the callback for UI refresh.
func (t *TodoPanel) SetOnInvalidate(fn func()) {
	t.mu.Lock()
	t.onInvalidate = fn
	t.mu.Unlock()
}

// Reload fetches fresh data and refreshes the UI.
func (t *TodoPanel) Reload() {
	t.mu.RLock()
	provider := t.dataProvider
	t.mu.RUnlock()
	if provider != nil {
		t.SetItems(provider())
	}
	t.invalidate()
}

func (t *TodoPanel) invalidate() {
	t.mu.RLock()
	fn := t.onInvalidate
	t.mu.RUnlock()
	if fn != nil {
		fn()
	}
}

// Update processes messages.
//
// 注意：本面板以居中 overlay 形式弹出，宿主分发给聚焦组件的 MouseMsg
// 携带的是屏幕绝对坐标、未做 overlay 偏移转换，无法在组件内可靠地映射
// 回条目，所以这里只走键盘交互（与 SessionSelector / SkillCenter 一致）。
func (t *TodoPanel) Update(msg core.Msg) core.Cmd {
	switch m := msg.(type) {
	case core.KeyMsg:
		t.processKeys(m.Data)
	}
	return nil
}

func (t *TodoPanel) processKeys(data string) {
	t.mu.RLock()
	km := t.km
	t.mu.RUnlock()
	if km == nil {
		return
	}

	switch {
	case km.Matches(data, "todo.up"):
		t.moveSelected(-1)
	case km.Matches(data, "todo.down"):
		t.moveSelected(1)
	case km.Matches(data, "todo.toggle"):
		t.toggleSelected()
	}
}

func (t *TodoPanel) moveSelected(delta int) {
	t.mu.Lock()
	n := len(t.items)
	if n == 0 {
		t.mu.Unlock()
		return
	}
	t.selected += delta
	if t.selected < 0 {
		t.selected = n - 1
	}
	if t.selected >= n {
		t.selected = 0
	}
	invalidate := t.onInvalidate
	t.mu.Unlock()
	if invalidate != nil {
		invalidate()
	}
}

func (t *TodoPanel) toggleSelected() {
	// 读锁内：仅取出当前选中 item 与回调引用，不持锁调外部接口。
	t.mu.RLock()
	if t.selected < 0 || t.selected >= len(t.items) {
		t.mu.RUnlock()
		return
	}
	item := t.items[t.selected]
	toggle := t.onToggle
	t.mu.RUnlock()

	if toggle == nil {
		return
	}
	// 锁外触发回调（回调可能再访问本面板，避免持锁调外部接口）。
	toggle(item)

	// 写锁内 reload：dataProvider() 的结果直接覆盖 t.items。
	t.mu.Lock()
	if t.dataProvider != nil {
		t.items = t.dataProvider()
	}
	t.clampSelectedLocked()
	invalidate := t.onInvalidate
	t.mu.Unlock()
	if invalidate != nil {
		invalidate()
	}
}

// Invalidate is a no-op.
func (t *TodoPanel) Invalidate() {}

// Render draws the TODO panel.
func (t *TodoPanel) Render(width int64) []string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if width < 1 {
		width = 1
	}

	var lines []string

	// Title
	lines = append(lines, t.theme.HeaderStyle.Render(t.theme.Title))
	lines = append(lines, t.theme.DimStyle.Render(strings.Repeat("─", int(width))))

	// Summary
	pending, inProgress, completed, cancelled := t.countStatuses()
	summary := fmt.Sprintf("Pending: %d | In Progress: %d | Completed: %d | Cancelled: %d",
		pending, inProgress, completed, cancelled)
	lines = append(lines, t.theme.DimStyle.Render(summary))
	lines = append(lines, "")

	// Available content width after prefix/subfix.
	const prefixW = 2 // "▸ " / "  " before each line
	iconW := int64(2) // icon + trailing space
	spareW := prefixW + iconW + 3 // "▸ ○ …" roughly

	for idx, item := range t.items {
		icon := t.statusIcon(item.Status)
		style := t.statusStyle(item.Status)
		isSelected := idx == t.selected
		if isSelected {
			style = t.theme.SelectedStyle
		}

		priority := ""
		if item.Priority != "" {
			priority = " [" + item.Priority + "]"
		}

		cursor := "  "
		if isSelected {
			cursor = t.theme.HeaderStyle.Render("▸ ")
		}

		contentLines := strings.Split(item.Content, "\n")
		for _, content := range contentLines {
			// Truncate oversize line to panel width.
			if contentW := core.VisibleWidth(content); contentW > width-spareW {
				maxContentW := width - spareW - core.VisibleWidth(priority)
				if maxContentW < 1 {
					maxContentW = 1
				}
				content = core.TruncateToWidth(content, maxContentW, "…")
			}

			line := icon + " " + style.Render(content) + t.theme.DimStyle.Render(priority)
			lines = append(lines, cursor+line)

			// Only show icon/priority/cursor on first line; continuation lines are indented.
			icon = ""
			priority = ""
			cursor = "  "
		}
	}

	if len(t.items) == 0 {
		lines = append(lines, t.theme.DimStyle.Render("  No tasks"))
	}

	return lines
}

func (t *TodoPanel) statusIcon(status string) string {
	switch status {
	case "pending":
		return "○"
	case "in_progress":
		return "◐"
	case "completed":
		return "●"
	case "cancelled":
		return "✗"
	default:
		return "○"
	}
}

func (t *TodoPanel) statusStyle(status string) theme.Style {
	switch status {
	case "pending":
		return t.theme.PendingStyle
	case "in_progress":
		return t.theme.InProgressStyle
	case "completed":
		return t.theme.CompletedStyle
	case "cancelled":
		return t.theme.CancelledStyle
	default:
		return t.theme.ItemStyle
	}
}

func (t *TodoPanel) countStatuses() (pending, inProgress, completed, cancelled int) {
	for _, item := range t.items {
		switch item.Status {
		case "pending":
			pending++
		case "in_progress":
			inProgress++
		case "completed":
			completed++
		case "cancelled":
			cancelled++
		}
	}
	return
}
