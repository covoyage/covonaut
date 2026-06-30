package component

import (
	"fmt"
	"strings"
	"sync"

	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/terminal"
	"github.com/covoyage/covonaut/tui/theme"
)

// SkillItem represents a skill in the skill center.
type SkillItem struct {
	Name       string
	State      string
	Provenance string
	Pinned     bool
	UseCount   int
	CreatedAt  string
	LastUsed   string
}

// SkillCenter is a visual skill management panel.
type SkillCenter struct {
	mu           sync.RWMutex
	items        []SkillItem
	filtered     []SkillItem
	selected     int
	filter       string
	theme        SkillCenterTheme
	width        int64
	height       int64
	km           *terminal.KeybindingsManager
	onSelect     func(SkillItem)
	onInvalidate func()
}

// SkillCenterTheme customizes the panel appearance.
type SkillCenterTheme struct {
	Title         string
	HeaderStyle   theme.Style
	ItemStyle     theme.Style
	ActiveStyle   theme.Style
	StaleStyle    theme.Style
	ArchivedStyle theme.Style
	SelectedStyle theme.Style
	DimStyle      theme.Style
}

// DefaultSkillCenterTheme returns the default theme.
func DefaultSkillCenterTheme() SkillCenterTheme {
	pal := theme.CurrentPalette()
	return SkillCenterTheme{
		Title:         "Skills (↑↓ navigate, / filter, Enter view, Esc close)",
		HeaderStyle:   pal.Accent,
		ItemStyle:     pal.Assistant,
		ActiveStyle:   pal.Success,
		StaleStyle:    pal.Accent,
		ArchivedStyle: pal.Dim,
		SelectedStyle: pal.SelectHighlight,
		DimStyle:      pal.Dim,
	}
}

// NewSkillCenter creates a new skill center panel.
func NewSkillCenter() *SkillCenter {
	km := terminal.NewKeybindingsManager(map[string]terminal.KeybindingDef{
		"skill.up":      {DefaultKeys: []string{"up", "ctrl+p"}},
		"skill.down":    {DefaultKeys: []string{"down", "ctrl+n"}},
		"skill.select":  {DefaultKeys: []string{"enter"}},
		"skill.close":   {DefaultKeys: []string{"esc"}},
	})
	return &SkillCenter{
		theme: DefaultSkillCenterTheme(),
		km:    km,
	}
}

// SetTitle sets the title.
func (s *SkillCenter) SetTitle(title string) {
	s.mu.Lock()
	s.theme.Title = title
	s.mu.Unlock()
}

// SetItems sets the skill items to display.
func (s *SkillCenter) SetItems(items []SkillItem) {
	s.mu.Lock()
	s.items = items
	s.selected = 0
	s.filter = ""
	s.applyFilterLocked()
	s.mu.Unlock()
}

func (s *SkillCenter) applyFilterLocked() {
	if s.filter == "" {
		s.filtered = make([]SkillItem, len(s.items))
		copy(s.filtered, s.items)
		return
	}

	lower := strings.ToLower(s.filter)
	s.filtered = s.filtered[:0]
	for _, item := range s.items {
		if strings.Contains(strings.ToLower(item.Name), lower) ||
			strings.Contains(strings.ToLower(item.Provenance), lower) {
			s.filtered = append(s.filtered, item)
		}
	}
}

// Update processes messages.
func (s *SkillCenter) Update(msg core.Msg) core.Cmd {
	switch m := msg.(type) {
	case core.KeyMsg:
		s.processKeys(m.Data)
	}
	return nil
}

func (s *SkillCenter) processKeys(data string) {
	s.mu.RLock()
	km := s.km
	s.mu.RUnlock()

	switch {
	case km.Matches(data, "skill.up"):
		s.moveSelected(-1)
	case km.Matches(data, "skill.down"):
		s.moveSelected(1)
	case km.Matches(data, "skill.select"):
		s.confirm()
	case km.Matches(data, "skill.close"):
		// Close handled by overlay system
	}
}

func (s *SkillCenter) moveSelected(delta int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.filtered)
	if n == 0 {
		return
	}
	s.selected += delta
	if s.selected < 0 {
		s.selected = n - 1
	}
	if s.selected >= n {
		s.selected = 0
	}
}

func (s *SkillCenter) confirm() {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.filtered) > 0 && s.selected >= 0 && s.selected < len(s.filtered) {
		item := s.filtered[s.selected]
		if s.onSelect != nil {
			s.onSelect(item)
		}
	}
}

// SetOnSelect sets the callback when a skill is selected.
func (s *SkillCenter) SetOnSelect(fn func(SkillItem)) {
	s.mu.Lock()
	s.onSelect = fn
	s.mu.Unlock()
}

// SetOnInvalidate sets the callback for UI refresh.
func (s *SkillCenter) SetOnInvalidate(fn func()) {
	s.mu.Lock()
	s.onInvalidate = fn
	s.mu.Unlock()
}

func (s *SkillCenter) invalidate() {
	s.mu.RLock()
	fn := s.onInvalidate
	s.mu.RUnlock()
	if fn != nil {
		fn()
	}
}

// Invalidate is a no-op.
func (s *SkillCenter) Invalidate() {}

// Render draws the skill center.
func (s *SkillCenter) Render(width int64) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if width < 1 {
		width = 1
	}

	var lines []string

	// Title
	lines = append(lines, s.theme.HeaderStyle.Render(s.theme.Title))
	lines = append(lines, s.theme.DimStyle.Render(strings.Repeat("─", int(width))))

	// Filter indicator
	if s.filter != "" {
		lines = append(lines, s.theme.DimStyle.Render("Filter: "+s.filter+" ("+fmt.Sprintf("%d of %d", len(s.filtered), len(s.items))+")"))
	}

	// Summary
	active, stale, archived := s.countStates()
	summary := fmt.Sprintf("Active: %d | Stale: %d | Archived: %d | Total: %d",
		active, stale, archived, len(s.items))
	lines = append(lines, s.theme.DimStyle.Render(summary))
	lines = append(lines, "")

	// Items
	maxVisible := int(s.height) - 7
	if maxVisible < 1 {
		maxVisible = 10
	}

	start := 0
	if s.selected >= maxVisible {
		start = s.selected - maxVisible + 1
	}
	end := start + maxVisible
	if end > len(s.filtered) {
		end = len(s.filtered)
	}

	for i := start; i < end; i++ {
		item := s.filtered[i]
		isSelected := i == s.selected
		line := s.formatSkillItem(item, width)
		if isSelected {
			lines = append(lines, "▸ "+s.theme.SelectedStyle.Render(line))
		} else {
			lines = append(lines, "  "+s.theme.ItemStyle.Render(line))
		}
	}

	if len(s.filtered) == 0 {
		lines = append(lines, s.theme.DimStyle.Render("  No skills found"))
	}

	return lines
}

func (s *SkillCenter) formatSkillItem(item SkillItem, width int64) string {
	stateIcon := s.stateIcon(item.State)
	stateStyle := s.stateStyle(item.State)

	pinned := ""
	if item.Pinned {
		pinned = " 📌"
	}

	uses := ""
	if item.UseCount > 0 {
		uses = fmt.Sprintf(" (%d uses)", item.UseCount)
	}

	name := item.Name
	if width > 0 && int64(core.VisibleWidth(stateIcon+name+pinned+uses)) > width-2 {
		name = core.TruncateToWidth(name, width-int64(core.VisibleWidth(stateIcon+pinned+uses))-4, "…")
	}

	return stateIcon + " " + stateStyle.Render(name) + s.theme.DimStyle.Render(pinned+uses)
}

func (s *SkillCenter) stateIcon(state string) string {
	switch state {
	case "active":
		return "●"
	case "stale":
		return "◐"
	case "archived":
		return "○"
	default:
		return "○"
	}
}

func (s *SkillCenter) stateStyle(state string) theme.Style {
	switch state {
	case "active":
		return s.theme.ActiveStyle
	case "stale":
		return s.theme.StaleStyle
	case "archived":
		return s.theme.ArchivedStyle
	default:
		return s.theme.ItemStyle
	}
}

func (s *SkillCenter) countStates() (active, stale, archived int) {
	for _, item := range s.items {
		switch item.State {
		case "active":
			active++
		case "stale":
			stale++
		case "archived":
			archived++
		}
	}
	return
}
