package component

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/terminal"
)

// ---------------------------------------------------------------------------
// Autocomplete — a dropdown/floating candidate list driven by a text source.
//
// Integration contract:
//   The caller owns the text component (e.g. Input or Editor) and feeds
//   buffer snapshots via Update(value, cursorPos). The Autocomplete
//   component computes the active token, pulls suggestions from registered
//   providers, and shows a SelectList.
//
//   When the user confirms (or TabCompletes), Autocomplete emits the new
//   buffer via OnApply(newValue, newCursor).
// ---------------------------------------------------------------------------

// StaticProvider implements AutocompleteProvider from a fixed slice.
type StaticProvider struct {
	TriggerStr  string
	Suggestions []core.Suggestion
}

// Trigger returns the configured prefix.
func (p *StaticProvider) Trigger() string { return p.TriggerStr }

// Complete fuzzy-filters the preset suggestions.
func (p *StaticProvider) Complete(token string) []core.Suggestion {
	if token == "" {
		return append([]core.Suggestion{}, p.Suggestions...)
	}
	labels := make([]string, len(p.Suggestions))
	for i, s := range p.Suggestions {
		labels[i] = s.Label
	}
	matches := core.FuzzyFilter(token, labels)
	out := make([]core.Suggestion, 0, len(matches))
	idx := make(map[string]int, len(labels))
	for i, l := range labels {
		if _, ok := idx[l]; !ok {
			idx[l] = i
		}
	}
	for _, m := range matches {
		if i, ok := idx[m.Original]; ok {
			out = append(out, p.Suggestions[i])
		}
	}
	return out
}

// FilePathProvider offers file-path completions rooted at the working directory.
type FilePathProvider struct {
	// RootDir is the directory to resolve relative paths against.
	// Empty = os.Getwd().
	RootDir string
	// TriggerStr is the trigger prefix ("@" by default).
	TriggerStr string
	// IncludeHidden — include dotfiles.
	IncludeHidden bool
}

// Trigger returns the configured prefix.
func (p *FilePathProvider) Trigger() string {
	if p.TriggerStr == "" {
		return "@"
	}
	return p.TriggerStr
}

// Complete lists entries in dir(path) filtering by the final segment.
func (p *FilePathProvider) Complete(token string) []core.Suggestion {
	base := p.RootDir
	if base == "" {
		if wd, err := os.Getwd(); err == nil {
			base = wd
		} else {
			return nil
		}
	}
	dir, prefix := filepath.Split(token)
	search := filepath.Join(base, dir)
	entries, err := os.ReadDir(search)
	if err != nil {
		return nil
	}
	var out []core.Suggestion
	for _, ent := range entries {
		name := ent.Name()
		if !p.IncludeHidden && strings.HasPrefix(name, ".") {
			continue
		}
		if prefix != "" && !strings.HasPrefix(strings.ToLower(name), strings.ToLower(prefix)) {
			continue
		}
		insert := filepath.Join(dir, name)
		label := insert
		desc := ""
		if ent.IsDir() {
			label += "/"
			insert += string(os.PathSeparator)
			desc = "dir"
		} else {
			desc = "file"
		}
		out = append(out, core.Suggestion{
			Label:       label,
			InsertText:  insert,
			Description: desc,
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// Autocomplete component
// ---------------------------------------------------------------------------

// Autocomplete is a Focusable component that wraps a SelectList.
//
// It is typically mounted inside an Overlay positioned above/below the host
// input. The host calls Update() on every change and Hide() when cancelling.
type Autocomplete struct {
	mu        sync.RWMutex
	list      *SelectList
	providers []core.AutocompleteProvider

	value     string
	cursorPos int64
	active    bool

	activeTriggerPos int64 // index in `value` where the trigger character lives
	activeToken      string

	onApply   func(newValue string, newCursor int64, suggestion core.Suggestion)
	onDismiss func()
}

// NewAutocomplete builds an empty Autocomplete component.
func NewAutocomplete(providers ...core.AutocompleteProvider) *Autocomplete {
	sl := NewSelectList(nil)
	sl.SetMaxVisible(6)
	ac := &Autocomplete{
		list:      sl,
		providers: providers,
	}
	sl.OnSelect(func(_ SelectItem) {
		ac.applyCurrent()
	})
	sl.OnCancel(func() {
		ac.Hide()
		if ac.onDismiss != nil {
			ac.onDismiss()
		}
	})
	return ac
}

// AddProvider appends a provider.
func (a *Autocomplete) AddProvider(p core.AutocompleteProvider) {
	a.mu.Lock()
	a.providers = append(a.providers, p)
	a.mu.Unlock()
}

// OnApply registers the callback fired when a suggestion is applied.
func (a *Autocomplete) OnApply(fn func(newValue string, newCursor int64, suggestion core.Suggestion)) {
	a.mu.Lock()
	a.onApply = fn
	a.mu.Unlock()
}

// OnDismiss registers the callback fired when the popup is dismissed (e.g. Escape).
func (a *Autocomplete) OnDismiss(fn func()) {
	a.mu.Lock()
	a.onDismiss = fn
	a.mu.Unlock()
}

// Active reports whether the popup currently has suggestions to show.
func (a *Autocomplete) Active() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.active
}

// Hide forces the popup off.
func (a *Autocomplete) Hide() {
	a.mu.Lock()
	a.active = false
	a.list.SetItems(nil)
	a.mu.Unlock()
}

// Refresh recomputes suggestions for the given buffer state.
//
// cursorPos is a RUNE index (0..len([]rune(value))).
func (a *Autocomplete) Refresh(value string, cursorPos int64) {
	a.mu.Lock()
	a.value = value
	a.cursorPos = cursorPos

	trigger, triggerPos, token := a.detectTriggerLocked()
	if trigger == nil {
		a.active = false
		a.list.SetItems(nil)
		a.mu.Unlock()
		return
	}
	suggestions := trigger.Complete(token)
	items := make([]SelectItem, len(suggestions))
	for i, s := range suggestions {
		items[i] = SelectItem{
			Value:       s.InsertText,
			Label:       s.Label,
			Description: s.Description,
		}
	}
	a.list.SetItems(items)
	a.active = len(items) > 0
	a.activeTriggerPos = triggerPos
	a.activeToken = token
	a.mu.Unlock()
}

// detectTriggerLocked finds the innermost provider match ending at cursor.
func (a *Autocomplete) detectTriggerLocked() (core.AutocompleteProvider, int64, string) {
	runes := []rune(a.value)
	if a.cursorPos < 0 || a.cursorPos > int64(len(runes)) {
		return nil, -1, ""
	}

	// Always-active providers run against the word preceding the cursor.
	// Triggered providers fire when the word is preceded by the trigger.
	start := a.cursorPos
	for start > 0 {
		r := runes[start-1]
		if r == ' ' || r == '\t' || r == '\n' {
			break
		}
		start--
	}
	word := string(runes[start:a.cursorPos])

	for _, p := range a.providers {
		t := p.Trigger()
		if t == "" {
			return p, start, word
		}
		if strings.HasPrefix(word, t) {
			return p, start, strings.TrimPrefix(word, t)
		}
	}
	return nil, -1, ""
}

// applyCurrent injects the highlighted suggestion back into the host buffer.
func (a *Autocomplete) applyCurrent() {
	a.mu.Lock()
	item, ok := a.list.CurrentItem()
	if !ok {
		a.mu.Unlock()
		return
	}
	trigger, triggerPos, _ := a.detectTriggerLocked()
	if trigger == nil {
		a.mu.Unlock()
		return
	}
	replace := item.Value
	if trigger.Trigger() != "" {
		replace = trigger.Trigger() + replace
	}
	runes := []rune(a.value)
	newRunes := make([]rune, 0, len(runes)+len([]rune(replace)))
	newRunes = append(newRunes, runes[:triggerPos]...)
	newRunes = append(newRunes, []rune(replace)...)
	newRunes = append(newRunes, runes[a.cursorPos:]...)
	newValue := string(newRunes)
	newCursor := triggerPos + int64(len([]rune(replace)))

	suggestion := core.Suggestion{
		Label:       item.Label,
		InsertText:  item.Value,
		Description: item.Description,
	}
	fn := a.onApply
	a.active = false
	a.list.SetItems(nil)
	a.mu.Unlock()
	if fn != nil {
		fn(newValue, newCursor, suggestion)
	}
}

// ---------------------------------------------------------------------------
// Component implementation
// ---------------------------------------------------------------------------

// Render draws the underlying SelectList (empty when not active).
func (a *Autocomplete) Render(width int64) []string {
	a.mu.RLock()
	active := a.active
	a.mu.RUnlock()
	if !active {
		return nil
	}
	return a.list.Render(width)
}

func (a *Autocomplete) Update(msg core.Msg) core.Cmd {
	switch m := msg.(type) {
	case core.KeyMsg:
		if terminal.MatchesKey(m.Data, "tab") {
			a.applyCurrent()
			return nil
		}
		a.list.Update(msg)
	case core.WindowSizeMsg:
		a.Invalidate()
	}
	return nil
}

// Invalidate fans out.
func (a *Autocomplete) Invalidate() { a.list.Invalidate() }

// SetFocused / IsFocused implement Focusable.
func (a *Autocomplete) SetFocused(on bool) { a.list.SetFocused(on) }
func (a *Autocomplete) IsFocused() bool    { return a.list.IsFocused() }
