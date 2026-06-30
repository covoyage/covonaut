package terminal

import (
	"sort"
	"sync"
)

// ---------------------------------------------------------------------------
// Keybindings registry.
//
// Usage:
//
//   km := tui.NewKeybindingsManager(tui.DefaultKeybindings())
//   km.SetUserBindings(map[string][]tui.KeyID{
//       "tui.editor.deleteWordBackward": {"ctrl+backspace"},
//   })
//   if km.Matches(data, "tui.editor.deleteWordBackward") { ... }
//
// Downstream packages can declare their own bindings and merge them:
//
//   km.Register("app.quit", tui.KeybindingDef{
//       DefaultKeys: []tui.KeyID{"ctrl+q"},
//       Description: "Quit application",
//   })
// ---------------------------------------------------------------------------

// KeybindingDef describes a single binding.
type KeybindingDef struct {
	DefaultKeys []KeyID
	Description string
}

// KeybindingConflict reports one physical key claimed by multiple bindings.
type KeybindingConflict struct {
	Key         KeyID
	Keybindings []string
}

// KeybindingsManager is the runtime resolver from binding ID → active KeyIDs.
type KeybindingsManager struct {
	mu sync.RWMutex

	defs         map[string]KeybindingDef
	userBindings map[string][]KeyID
	resolved     map[string][]KeyID
	conflicts    []KeybindingConflict
}

// NewKeybindingsManager creates a manager with the given definitions applied.
func NewKeybindingsManager(defs map[string]KeybindingDef) *KeybindingsManager {
	m := &KeybindingsManager{
		defs:         make(map[string]KeybindingDef, len(defs)),
		userBindings: map[string][]KeyID{},
		resolved:     map[string][]KeyID{},
	}
	for id, def := range defs {
		m.defs[id] = def
	}
	m.rebuild()
	return m
}

// Register adds (or overwrites) a binding definition.
func (m *KeybindingsManager) Register(id string, def KeybindingDef) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.defs[id] = def
	m.rebuild()
}

// SetUserBindings replaces the user-override map and rebuilds the resolver.
// Pass an empty map to revert to defaults.
func (m *KeybindingsManager) SetUserBindings(b map[string][]KeyID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.userBindings = make(map[string][]KeyID, len(b))
	for k, v := range b {
		cp := make([]KeyID, len(v))
		copy(cp, v)
		m.userBindings[k] = cp
	}
	m.rebuild()
}

// Matches reports whether any key event in the raw input `data` matches the
// resolved keys for `id`.
func (m *KeybindingsManager) Matches(data string, id string) bool {
	m.mu.RLock()
	keys := m.resolved[id]
	m.mu.RUnlock()
	for _, k := range keys {
		if MatchesKey(data, k) {
			return true
		}
	}
	return false
}

// Keys returns the currently resolved KeyIDs for a binding.
func (m *KeybindingsManager) Keys(id string) []KeyID {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]KeyID, len(m.resolved[id]))
	copy(out, m.resolved[id])
	return out
}

// Definition returns the registered definition (zero-value if not found).
func (m *KeybindingsManager) Definition(id string) KeybindingDef {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.defs[id]
}

// All returns the full ID → resolved-keys map.
func (m *KeybindingsManager) All() map[string][]KeyID {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string][]KeyID, len(m.resolved))
	for k, v := range m.resolved {
		cp := make([]KeyID, len(v))
		copy(cp, v)
		out[k] = cp
	}
	return out
}

// Conflicts returns any user-defined keys that were bound to more than one ID.
func (m *KeybindingsManager) Conflicts() []KeybindingConflict {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]KeybindingConflict, len(m.conflicts))
	copy(out, m.conflicts)
	return out
}

func (m *KeybindingsManager) rebuild() {
	claims := map[KeyID]map[string]struct{}{}

	for id, userKeys := range m.userBindings {
		if _, ok := m.defs[id]; !ok {
			continue
		}
		for _, k := range normalizeKeyList(userKeys) {
			if _, ok := claims[k]; !ok {
				claims[k] = map[string]struct{}{}
			}
			claims[k][id] = struct{}{}
		}
	}

	var conflicts []KeybindingConflict
	for k, ids := range claims {
		if len(ids) > 1 {
			names := make([]string, 0, len(ids))
			for n := range ids {
				names = append(names, n)
			}
			sort.Strings(names)
			conflicts = append(conflicts, KeybindingConflict{Key: k, Keybindings: names})
		}
	}
	sort.Slice(conflicts, func(i, j int) bool { return conflicts[i].Key < conflicts[j].Key })
	m.conflicts = conflicts

	resolved := make(map[string][]KeyID, len(m.defs))
	for id, def := range m.defs {
		if keys, ok := m.userBindings[id]; ok {
			resolved[id] = normalizeKeyList(keys)
		} else {
			resolved[id] = normalizeKeyList(def.DefaultKeys)
		}
	}
	m.resolved = resolved
}

func normalizeKeyList(keys []KeyID) []KeyID {
	seen := map[KeyID]bool{}
	out := make([]KeyID, 0, len(keys))
	for _, k := range keys {
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	return out
}

// ---------------------------------------------------------------------------
// Default bindings shipped with the TUI framework.
// ---------------------------------------------------------------------------

// DefaultKeybindings returns a fresh copy of the built-in bindings.
func DefaultKeybindings() map[string]KeybindingDef {
	return map[string]KeybindingDef{
		"tui.editor.cursorUp":           {DefaultKeys: []KeyID{"up"}, Description: "Move cursor up"},
		"tui.editor.cursorDown":         {DefaultKeys: []KeyID{"down"}, Description: "Move cursor down"},
		"tui.editor.cursorLeft":         {DefaultKeys: []KeyID{"left", "shift+left", "ctrl+b"}, Description: "Move cursor left"},
		"tui.editor.cursorRight":        {DefaultKeys: []KeyID{"right", "shift+right", "ctrl+f"}, Description: "Move cursor right"},
		"tui.editor.cursorWordLeft":     {DefaultKeys: []KeyID{"alt+left", "ctrl+left", "alt+b"}, Description: "Word left"},
		"tui.editor.cursorWordRight":    {DefaultKeys: []KeyID{"alt+right", "ctrl+right", "alt+f"}, Description: "Word right"},
		"tui.editor.cursorLineStart":    {DefaultKeys: []KeyID{"home", "ctrl+a"}, Description: "Line start"},
		"tui.editor.cursorLineEnd":      {DefaultKeys: []KeyID{"end", "ctrl+e"}, Description: "Line end"},
		"tui.editor.pageUp":             {DefaultKeys: []KeyID{"pageUp"}, Description: "Page up"},
		"tui.editor.pageDown":           {DefaultKeys: []KeyID{"pageDown"}, Description: "Page down"},
		"tui.editor.deleteCharBackward": {DefaultKeys: []KeyID{"backspace"}, Description: "Delete backward"},
		"tui.editor.deleteCharForward":  {DefaultKeys: []KeyID{"delete", "ctrl+d"}, Description: "Delete forward"},
		"tui.editor.deleteWordBackward": {DefaultKeys: []KeyID{"ctrl+w", "alt+backspace"}, Description: "Delete word backward"},
		"tui.editor.deleteWordForward":  {DefaultKeys: []KeyID{"alt+d", "alt+delete"}, Description: "Delete word forward"},
		"tui.editor.deleteToLineStart":  {DefaultKeys: []KeyID{"ctrl+u"}, Description: "Delete to line start"},
		"tui.editor.deleteToLineEnd":    {DefaultKeys: []KeyID{"ctrl+k"}, Description: "Delete to line end"},
		"tui.editor.yank":               {DefaultKeys: []KeyID{"ctrl+y"}, Description: "Yank"},
		"tui.editor.yankPop":            {DefaultKeys: []KeyID{"alt+y"}, Description: "Yank pop"},
		"tui.editor.undo":               {DefaultKeys: []KeyID{"ctrl+-", "ctrl+z"}, Description: "Undo"},
		"tui.editor.selectAll":          {DefaultKeys: []KeyID{"super+a", "meta+a", "ctrl+super+a", "ctrl+meta+a"}, Description: "Select all editor text"},

		"tui.input.newLine": {DefaultKeys: []KeyID{"shift+enter", "alt+enter"}, Description: "Insert newline"},
		"tui.input.submit":  {DefaultKeys: []KeyID{"enter"}, Description: "Submit input"},
		"tui.input.tab":     {DefaultKeys: []KeyID{"tab"}, Description: "Autocomplete"},
		"tui.input.copy":    {DefaultKeys: []KeyID{"ctrl+c", "super+c", "meta+c", "ctrl+super+c", "ctrl+meta+c"}, Description: "Copy / interrupt"},

		"tui.select.up":       {DefaultKeys: []KeyID{"up", "ctrl+p"}, Description: "Selection up"},
		"tui.select.down":     {DefaultKeys: []KeyID{"down", "ctrl+n"}, Description: "Selection down"},
		"tui.select.pageUp":   {DefaultKeys: []KeyID{"pageUp"}, Description: "Selection page up"},
		"tui.select.pageDown": {DefaultKeys: []KeyID{"pageDown"}, Description: "Selection page down"},
		"tui.select.confirm":  {DefaultKeys: []KeyID{"enter"}, Description: "Confirm selection"},
		"tui.select.cancel":   {DefaultKeys: []KeyID{"escape", "ctrl+c"}, Description: "Cancel selection"},
	}
}

// ---------------------------------------------------------------------------
// Global manager (optional; a TUI instance may use its own).
// ---------------------------------------------------------------------------

var (
	globalKBMu sync.Mutex
	globalKB   *KeybindingsManager
)

// GetGlobalKeybindings returns the process-global keybindings manager,
// creating a default one on first access.
func GetGlobalKeybindings() *KeybindingsManager {
	globalKBMu.Lock()
	defer globalKBMu.Unlock()
	if globalKB == nil {
		globalKB = NewKeybindingsManager(DefaultKeybindings())
	}
	return globalKB
}

// SetGlobalKeybindings replaces the process-global manager.
func SetGlobalKeybindings(m *KeybindingsManager) {
	globalKBMu.Lock()
	defer globalKBMu.Unlock()
	globalKB = m
}
