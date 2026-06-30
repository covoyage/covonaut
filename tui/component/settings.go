package component

import (
	"fmt"
	"sync"

	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/terminal"
	"github.com/covoyage/covonaut/tui/theme"
)

// ---------------------------------------------------------------------------
// SettingsList — a two-column key/value list where the value can be cycled
// with Left/Right or Enter.
//
// Useful for app settings panes where each row exposes a finite set of
// choices (e.g. theme = light | dark | system).
// ---------------------------------------------------------------------------

// SettingOption is one choice inside a SettingEntry.
type SettingOption struct {
	Value string
	Label string
}

// SettingEntry represents one row in the list.
type SettingEntry struct {
	Key         string
	Label       string
	Options     []SettingOption
	Current     int64
	Description string
}

// SettingsListTheme overrides styling.
type SettingsListTheme struct {
	KeyFn         func(string) string
	ValueFn       func(string) string
	SelectedKeyFn func(string) string
	SelectedValFn func(string) string
	DescriptionFn func(string) string
}

// SettingsList is a Focusable component.
type SettingsList struct {
	mu sync.RWMutex

	entries    []SettingEntry
	cursor     int64
	maxVisible int64
	scroll     int64
	theme      SettingsListTheme
	focused    bool
	km         *terminal.KeybindingsManager

	onChange func(entry SettingEntry)
	onSubmit func(entry SettingEntry)
}

// NewSettingsList creates a SettingsList.
func NewSettingsList(entries []SettingEntry) *SettingsList {
	return &SettingsList{
		entries:    entries,
		maxVisible: 10,
		km:         terminal.GetGlobalKeybindings(),
	}
}

// SetMaxVisible caps the number of rows (min 3).
func (s *SettingsList) SetMaxVisible(n int64) {
	if n < 3 {
		n = 3
	}
	s.mu.Lock()
	s.maxVisible = n
	s.mu.Unlock()
}

// SetTheme installs a custom theme.
func (s *SettingsList) SetTheme(t SettingsListTheme) { s.mu.Lock(); s.theme = t; s.mu.Unlock() }

// SetEntries replaces all entries (cursor reset).
func (s *SettingsList) SetEntries(e []SettingEntry) {
	s.mu.Lock()
	s.entries = e
	s.cursor = 0
	s.scroll = 0
	s.mu.Unlock()
}

// SetValue sets the current option index for an entry by key.
func (s *SettingsList) SetValue(key string, index int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.entries {
		if s.entries[i].Key == key {
			if index < 0 || index >= int64(len(s.entries[i].Options)) {
				return false
			}
			s.entries[i].Current = index
			return true
		}
	}
	return false
}

// GetValue reads the currently selected option value for a key.
func (s *SettingsList) GetValue(key string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, e := range s.entries {
		if e.Key == key {
			if e.Current < 0 || e.Current >= int64(len(e.Options)) {
				return "", false
			}
			return e.Options[e.Current].Value, true
		}
	}
	return "", false
}

// OnChange fires whenever a value cycles.
func (s *SettingsList) OnChange(fn func(SettingEntry)) { s.mu.Lock(); s.onChange = fn; s.mu.Unlock() }

// OnSubmit fires when Enter is pressed on an entry (after cycling).
func (s *SettingsList) OnSubmit(fn func(SettingEntry)) { s.mu.Lock(); s.onSubmit = fn; s.mu.Unlock() }

// SetFocused / IsFocused implement Focusable.
func (s *SettingsList) SetFocused(on bool) { s.mu.Lock(); s.focused = on; s.mu.Unlock() }
func (s *SettingsList) IsFocused() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.focused
}

// Render draws a key/value list with the current row highlighted.
func (s *SettingsList) Render(width int64) []string {
	s.mu.RLock()
	entries := s.entries
	cursor := s.cursor
	scroll := s.scroll
	maxV := s.maxVisible
	sltheme := s.theme
	s.mu.RUnlock()

	if len(entries) == 0 {
		return []string{core.PadToWidth(theme.CurrentPalette().Dim.Render("(no settings)"), width)}
	}

	maxKey := int64(0)
	for _, e := range entries {
		w := core.VisibleWidth(e.Label)
		if w > maxKey {
			maxKey = w
		}
	}
	if maxKey > width/2 {
		maxKey = width / 2
	}

	visible := maxV
	if int64(len(entries)) < visible {
		visible = int64(len(entries))
	}
	end := scroll + visible
	if end > int64(len(entries)) {
		end = int64(len(entries))
	}

	var out []string
	for i := scroll; i < end; i++ {
		e := entries[i]
		selected := i == cursor
		key := core.PadToWidth(core.TruncateToWidth(e.Label, maxKey, "…"), maxKey)
		val := "(none)"
		if e.Current >= 0 && e.Current < int64(len(e.Options)) {
			val = e.Options[e.Current].Label
			if val == "" {
				val = e.Options[e.Current].Value
			}
		}

		keyFn := sltheme.KeyFn
		if keyFn == nil {
			keyFn = func(x string) string { return x }
		}
		valFn := sltheme.ValueFn
		if valFn == nil {
			valFn = theme.CurrentPalette().Dim.Render
		}
		if selected {
			keyFn = sltheme.SelectedKeyFn
			if keyFn == nil {
				keyFn = func(x string) string { return theme.CurrentPalette().SettingsKey.Render(x) }
			}
			valFn = sltheme.SelectedValFn
			if valFn == nil {
				valFn = func(x string) string { return theme.CurrentPalette().SettingsValueSelected.Render(x) }
			}
		}

		indicator := "  "
		if selected {
			indicator = "> "
		}
		line := fmt.Sprintf("%s%s  %s", indicator, keyFn(key), valFn(val))

		if selected && e.Description != "" {
			dFn := sltheme.DescriptionFn
			if dFn == nil {
				dFn = theme.CurrentPalette().Dim.Render
			}
			line += "   " + dFn(e.Description)
		}

		out = append(out, core.PadToWidth(core.TruncateToWidth(line, width, "…"), width))
	}

	if int64(len(entries)) > maxV {
		info := fmt.Sprintf("  [%d/%d]", cursor+1, len(entries))
		out = append(out, core.PadToWidth(theme.CurrentPalette().Dim.Render(info), width))
	}

	return out
}

// Invalidate is a no-op.
func (s *SettingsList) Invalidate() {}

func (s *SettingsList) Update(msg core.Msg) core.Cmd {
	switch m := msg.(type) {
	case core.KeyMsg:
		s.processKeys(m.Data)
	case core.WindowSizeMsg:
		s.Invalidate()
	}
	return nil
}

func (s *SettingsList) processKeys(data string) {
	s.mu.RLock()
	km := s.km
	s.mu.RUnlock()

	switch {
	case km.Matches(data, "tui.select.up"):
		s.moveCursor(-1)
	case km.Matches(data, "tui.select.down"):
		s.moveCursor(1)
	case km.Matches(data, "tui.editor.cursorLeft"):
		s.cycleValue(-1)
	case km.Matches(data, "tui.editor.cursorRight"):
		s.cycleValue(1)
	case km.Matches(data, "tui.select.confirm"):
		s.submitOrCycle()
	}
}

func (s *SettingsList) moveCursor(delta int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := int64(len(s.entries))
	if n == 0 {
		return
	}
	s.cursor += delta
	if s.cursor < 0 {
		s.cursor = 0
	}
	if s.cursor >= n {
		s.cursor = n - 1
	}
	if s.cursor < s.scroll {
		s.scroll = s.cursor
	}
	if s.cursor >= s.scroll+s.maxVisible {
		s.scroll = s.cursor - s.maxVisible + 1
	}
}

func (s *SettingsList) cycleValue(delta int64) {
	s.mu.Lock()
	if len(s.entries) == 0 {
		s.mu.Unlock()
		return
	}
	e := &s.entries[s.cursor]
	n := int64(len(e.Options))
	if n == 0 {
		s.mu.Unlock()
		return
	}
	e.Current = (e.Current + delta + n) % n
	updated := *e
	fn := s.onChange
	s.mu.Unlock()
	if fn != nil {
		fn(updated)
	}
}

func (s *SettingsList) submitOrCycle() {
	s.mu.Lock()
	if len(s.entries) == 0 {
		s.mu.Unlock()
		return
	}
	// Enter acts as "cycle forward then fire onSubmit", so single-option rows
	// still notify callers.
	e := &s.entries[s.cursor]
	if len(e.Options) > 1 {
		e.Current = (e.Current + 1) % int64(len(e.Options))
	}
	updated := *e
	onSubmit := s.onSubmit
	onChange := s.onChange
	s.mu.Unlock()
	if onChange != nil {
		onChange(updated)
	}
	if onSubmit != nil {
		onSubmit(updated)
	}
}
