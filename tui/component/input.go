package component

import (
	"sync"

	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/terminal"
	"github.com/covoyage/covonaut/tui/theme"
)

// ---------------------------------------------------------------------------
// Input — single-line editor component.
//
// Supports:
//   - Printable rune insertion (CJK-aware via runeWidth).
//   - Backspace / Delete / word delete (Ctrl+W, Alt+Backspace, Alt+D).
//   - Cursor movement: arrow keys, Ctrl+A/E, Home/End, word navigation.
//   - Ctrl+U / Ctrl+K (delete to line start / end).
//   - Horizontal scrolling when the value is wider than the viewport.
//   - IME cursor positioning via CURSOR_MARKER.
//   - Submit on Enter (OnSubmit callback).
//   - Kill-ring (Ctrl+Y / Alt+Y).
//   - History walking via OnHistoryPrev / OnHistoryNext (bound to Up/Down).
// ---------------------------------------------------------------------------

// Input is a Focusable single-line editor component.
type Input struct {
	mu sync.RWMutex

	runes       []rune
	cursor      int64 // in runes, 0 ≤ cursor ≤ len(runes)
	scroll      int64 // horizontal scroll offset in cells
	allSelected bool
	prompt      string
	promptFn    func(string) string
	placeFn     func(string) string
	textFn      func(string) string
	focused     bool
	paddingX    int64
	placeText   string

	killRing   []string
	killIndex  int64
	lastKillOp bool // whether the last op added to the kill ring (for yank-pop)

	km *terminal.KeybindingsManager

	onSubmit      func(value string)
	onChange      func(value string)
	onHistoryPrev func() (value string, ok bool)
	onHistoryNext func() (value string, ok bool)
}

// NewInput creates a single-line Input bound to the given keybindings
// manager. Pass nil to use the global manager.
func NewInput(km *terminal.KeybindingsManager) *Input {
	if km == nil {
		km = terminal.GetGlobalKeybindings()
	}
	return &Input{
		prompt: "> ",
		km:     km,
	}
}

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

// SetPrompt sets the visible prompt string (default "> ").
func (i *Input) SetPrompt(s string) { i.mu.Lock(); i.prompt = s; i.mu.Unlock() }

// SetPromptFn installs an optional style function for the prompt.
func (i *Input) SetPromptFn(fn func(string) string) { i.mu.Lock(); i.promptFn = fn; i.mu.Unlock() }

// SetTextFn installs an optional style function for the typed text.
func (i *Input) SetTextFn(fn func(string) string) { i.mu.Lock(); i.textFn = fn; i.mu.Unlock() }

// SetPlaceholder sets a dim placeholder rendered when value is empty.
func (i *Input) SetPlaceholder(s string) { i.mu.Lock(); i.placeText = s; i.mu.Unlock() }

// SetPlaceholderFn customises placeholder styling.
func (i *Input) SetPlaceholderFn(fn func(string) string) {
	i.mu.Lock()
	i.placeFn = fn
	i.mu.Unlock()
}

// SetPaddingX sets horizontal padding applied before the prompt.
func (i *Input) SetPaddingX(x int64) {
	if x < 0 {
		x = 0
	}
	i.mu.Lock()
	i.paddingX = x
	i.mu.Unlock()
}

// SetValue overwrites the buffer and places the cursor at the end.
func (i *Input) SetValue(s string) {
	i.mu.Lock()
	i.runes = []rune(s)
	i.cursor = int64(len(i.runes))
	i.scroll = 0
	i.allSelected = false
	changeFn := i.onChange
	i.mu.Unlock()
	if changeFn != nil {
		changeFn(s)
	}
}

// GetValue returns the current buffer contents.
func (i *Input) GetValue() string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return string(i.runes)
}

// Clear empties the buffer.
func (i *Input) Clear() { i.SetValue("") }

// SelectAll selects the input value without affecting terminal-level text selection.
func (i *Input) SelectAll() {
	i.mu.Lock()
	i.allSelected = len(i.runes) > 0
	i.cursor = int64(len(i.runes))
	i.mu.Unlock()
}

func (i *Input) GetSelectedText() string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	if !i.allSelected {
		return ""
	}
	return string(i.runes)
}

func (i *Input) ClearSelection() {
	i.mu.Lock()
	i.allSelected = false
	i.mu.Unlock()
}

// OnSubmit registers the callback fired on Enter.
func (i *Input) OnSubmit(fn func(string)) { i.mu.Lock(); i.onSubmit = fn; i.mu.Unlock() }

// OnChange registers the callback fired whenever the buffer changes.
func (i *Input) OnChange(fn func(string)) { i.mu.Lock(); i.onChange = fn; i.mu.Unlock() }

// OnHistoryPrev binds an Up-arrow handler returning the previous value (ok=false to no-op).
func (i *Input) OnHistoryPrev(fn func() (string, bool)) {
	i.mu.Lock()
	i.onHistoryPrev = fn
	i.mu.Unlock()
}

// OnHistoryNext binds a Down-arrow handler returning the next value.
func (i *Input) OnHistoryNext(fn func() (string, bool)) {
	i.mu.Lock()
	i.onHistoryNext = fn
	i.mu.Unlock()
}

// SetFocused marks focus state.
func (i *Input) SetFocused(on bool) { i.mu.Lock(); i.focused = on; i.mu.Unlock() }

// IsFocused reports focus state.
func (i *Input) IsFocused() bool { i.mu.RLock(); defer i.mu.RUnlock(); return i.focused }

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

// Render emits a single line: [padX][prompt][scrolled text][cursor marker].
func (i *Input) Render(width int64) []string {
	i.mu.Lock()
	defer i.mu.Unlock()

	pad := repeatSpace(i.paddingX)
	rawPrompt := i.prompt
	prompt := rawPrompt
	if i.promptFn != nil {
		prompt = i.promptFn(prompt)
	}
	promptW := core.VisibleWidth(rawPrompt)
	avail := width - i.paddingX - promptW
	if avail < 1 {
		avail = 1
	}

	var body string
	valueEmpty := len(i.runes) == 0

	if valueEmpty && !i.focused {
		placeholder := i.placeText
		if i.placeFn != nil {
			placeholder = i.placeFn(placeholder)
		} else {
			placeholder = theme.CurrentPalette().Dim.Render(placeholder)
		}
		body = core.PadToWidth(core.TruncateToWidth(placeholder, avail, "…"), avail)
	} else {
		cursorCol := core.CellWidthOfRunes(i.runes, 0, i.cursor)
		i.scroll = core.AdjustHorizontalScroll(i.scroll, cursorCol, avail)

		visible := core.SliceRunesByCells(i.runes, i.scroll, i.scroll+avail)
		displayed := visible.Text
		if i.textFn != nil {
			displayed = i.textFn(displayed)
		}
		if i.allSelected && visible.Text != "" {
			displayed = "\x1b[48;5;33m" + core.StripAnsi(displayed) + "\x1b[0m"
		}
		body = core.PadToWidth(displayed, avail)

		if i.focused {
			cursorLocal := cursorCol - i.scroll
			if cursorLocal < 0 {
				cursorLocal = 0
			}
			if cursorLocal > avail {
				cursorLocal = avail
			}
			body = core.InsertMarker(body, cursorLocal)
		}
	}

	line := pad + prompt + body
	return []string{line}
}

// Invalidate is a no-op for Input (no cache).
func (i *Input) Invalidate() {}

func (i *Input) Update(msg core.Msg) core.Cmd {
	switch m := msg.(type) {
	case core.KeyMsg:
		i.processKeys(m.Data)
	case core.PasteMsg:
		i.processKeys(m.Text)
	case core.WindowSizeMsg:
		i.Invalidate()
	}
	return nil
}

func (i *Input) processKeys(data string) {
	km := i.km

	// Parse multiple events in one feed (e.g. pasted text).
	keys := terminal.ParseKeys(data)
	if len(keys) == 0 {
		return
	}

	for _, k := range keys {
		raw := k.Raw

		switch {
		case km.Matches(raw, "tui.input.submit"):
			i.submit()
		case km.Matches(raw, "tui.editor.selectAll"):
			i.SelectAll()
		case km.Matches(raw, "tui.editor.cursorLeft"):
			i.moveCursor(-1)
		case km.Matches(raw, "tui.editor.cursorRight"):
			i.moveCursor(1)
		case km.Matches(raw, "tui.editor.cursorWordLeft"):
			i.moveCursorWord(-1)
		case km.Matches(raw, "tui.editor.cursorWordRight"):
			i.moveCursorWord(1)
		case km.Matches(raw, "tui.editor.cursorLineStart"):
			i.mu.Lock()
			i.allSelected = false
			i.cursor = 0
			i.mu.Unlock()
		case km.Matches(raw, "tui.editor.cursorLineEnd"):
			i.mu.Lock()
			i.allSelected = false
			i.cursor = int64(len(i.runes))
			i.mu.Unlock()
		case km.Matches(raw, "tui.editor.deleteCharBackward"):
			i.deleteBackward(1)
		case km.Matches(raw, "tui.editor.deleteCharForward"):
			i.deleteForward(1)
		case km.Matches(raw, "tui.editor.deleteWordBackward"):
			i.deleteWordBackward()
		case km.Matches(raw, "tui.editor.deleteWordForward"):
			i.deleteWordForward()
		case km.Matches(raw, "tui.editor.deleteToLineStart"):
			i.deleteToLineStart()
		case km.Matches(raw, "tui.editor.deleteToLineEnd"):
			i.deleteToLineEnd()
		case km.Matches(raw, "tui.editor.yank"):
			i.yank()
		case km.Matches(raw, "tui.editor.yankPop"):
			i.yankPop()
		case km.Matches(raw, "tui.select.up"), km.Matches(raw, "tui.editor.cursorUp"):
			i.historyPrev()
		case km.Matches(raw, "tui.select.down"), km.Matches(raw, "tui.editor.cursorDown"):
			i.historyNext()
		default:
			if k.IsPrintable() {
				i.insertRune(k.Rune)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Editing operations
// ---------------------------------------------------------------------------

func (i *Input) insertRune(r rune) {
	i.mu.Lock()
	if i.allSelected {
		i.clearSelectionContentLocked()
	}
	before := i.runes[:i.cursor]
	after := i.runes[i.cursor:]
	newRunes := make([]rune, 0, len(i.runes)+1)
	newRunes = append(newRunes, before...)
	newRunes = append(newRunes, r)
	newRunes = append(newRunes, after...)
	i.runes = newRunes
	i.cursor++
	i.lastKillOp = false
	i.allSelected = false
	value := string(i.runes)
	changeFn := i.onChange
	i.mu.Unlock()
	if changeFn != nil {
		changeFn(value)
	}
}

func (i *Input) moveCursor(delta int64) {
	i.mu.Lock()
	i.allSelected = false
	i.cursor += delta
	if i.cursor < 0 {
		i.cursor = 0
	}
	if i.cursor > int64(len(i.runes)) {
		i.cursor = int64(len(i.runes))
	}
	i.lastKillOp = false
	i.mu.Unlock()
}

func (i *Input) moveCursorWord(delta int64) {
	i.mu.Lock()
	i.allSelected = false
	if delta < 0 {
		i.cursor = core.FindWordBoundaryLeft(i.runes, i.cursor)
	} else {
		i.cursor = core.FindWordBoundaryRight(i.runes, i.cursor)
	}
	i.lastKillOp = false
	i.mu.Unlock()
}

func (i *Input) deleteBackward(n int64) {
	i.mu.Lock()
	if i.allSelected {
		i.clearSelectionContentLocked()
		v := string(i.runes)
		fn := i.onChange
		i.mu.Unlock()
		if fn != nil {
			fn(v)
		}
		return
	}
	if i.cursor <= 0 {
		i.mu.Unlock()
		return
	}
	start := i.cursor - n
	if start < 0 {
		start = 0
	}
	i.runes = append(i.runes[:start], i.runes[i.cursor:]...)
	i.cursor = start
	i.lastKillOp = false
	i.allSelected = false
	v := string(i.runes)
	fn := i.onChange
	i.mu.Unlock()
	if fn != nil {
		fn(v)
	}
}

func (i *Input) deleteForward(n int64) {
	i.mu.Lock()
	if i.allSelected {
		i.clearSelectionContentLocked()
		v := string(i.runes)
		fn := i.onChange
		i.mu.Unlock()
		if fn != nil {
			fn(v)
		}
		return
	}
	end := i.cursor + n
	if end > int64(len(i.runes)) {
		end = int64(len(i.runes))
	}
	if end <= i.cursor {
		i.mu.Unlock()
		return
	}
	i.runes = append(i.runes[:i.cursor], i.runes[end:]...)
	i.lastKillOp = false
	i.allSelected = false
	v := string(i.runes)
	fn := i.onChange
	i.mu.Unlock()
	if fn != nil {
		fn(v)
	}
}

func (i *Input) deleteWordBackward() {
	i.mu.Lock()
	if i.allSelected {
		i.clearSelectionContentLocked()
		v := string(i.runes)
		fn := i.onChange
		i.mu.Unlock()
		if fn != nil {
			fn(v)
		}
		return
	}
	start := core.FindWordBoundaryLeft(i.runes, i.cursor)
	if start >= i.cursor {
		i.mu.Unlock()
		return
	}
	killed := string(i.runes[start:i.cursor])
	i.runes = append(i.runes[:start], i.runes[i.cursor:]...)
	i.cursor = start
	i.pushKillRing(killed)
	v := string(i.runes)
	fn := i.onChange
	i.mu.Unlock()
	if fn != nil {
		fn(v)
	}
}

func (i *Input) deleteWordForward() {
	i.mu.Lock()
	if i.allSelected {
		i.clearSelectionContentLocked()
		v := string(i.runes)
		fn := i.onChange
		i.mu.Unlock()
		if fn != nil {
			fn(v)
		}
		return
	}
	end := core.FindWordBoundaryRight(i.runes, i.cursor)
	if end <= i.cursor {
		i.mu.Unlock()
		return
	}
	killed := string(i.runes[i.cursor:end])
	i.runes = append(i.runes[:i.cursor], i.runes[end:]...)
	i.pushKillRing(killed)
	v := string(i.runes)
	fn := i.onChange
	i.mu.Unlock()
	if fn != nil {
		fn(v)
	}
}

func (i *Input) deleteToLineStart() {
	i.mu.Lock()
	if i.allSelected {
		i.clearSelectionContentLocked()
		v := string(i.runes)
		fn := i.onChange
		i.mu.Unlock()
		if fn != nil {
			fn(v)
		}
		return
	}
	if i.cursor == 0 {
		i.mu.Unlock()
		return
	}
	killed := string(i.runes[:i.cursor])
	i.runes = i.runes[i.cursor:]
	i.cursor = 0
	i.pushKillRing(killed)
	v := string(i.runes)
	fn := i.onChange
	i.mu.Unlock()
	if fn != nil {
		fn(v)
	}
}

func (i *Input) deleteToLineEnd() {
	i.mu.Lock()
	if i.allSelected {
		i.clearSelectionContentLocked()
		v := string(i.runes)
		fn := i.onChange
		i.mu.Unlock()
		if fn != nil {
			fn(v)
		}
		return
	}
	if i.cursor >= int64(len(i.runes)) {
		i.mu.Unlock()
		return
	}
	killed := string(i.runes[i.cursor:])
	i.runes = i.runes[:i.cursor]
	i.pushKillRing(killed)
	v := string(i.runes)
	fn := i.onChange
	i.mu.Unlock()
	if fn != nil {
		fn(v)
	}
}

func (i *Input) pushKillRing(s string) {
	if s == "" {
		return
	}
	i.killRing = append(i.killRing, s)
	if len(i.killRing) > 32 {
		i.killRing = i.killRing[len(i.killRing)-32:]
	}
	i.killIndex = int64(len(i.killRing) - 1)
	i.lastKillOp = true
}

func (i *Input) yank() {
	i.mu.Lock()
	if len(i.killRing) == 0 {
		i.mu.Unlock()
		return
	}
	if i.allSelected {
		i.clearSelectionContentLocked()
	}
	text := i.killRing[i.killIndex]
	i.insertStringLocked(text)
	i.lastKillOp = true
	i.allSelected = false
	v := string(i.runes)
	fn := i.onChange
	i.mu.Unlock()
	if fn != nil {
		fn(v)
	}
}

func (i *Input) yankPop() {
	i.mu.Lock()
	if !i.lastKillOp || len(i.killRing) == 0 {
		i.mu.Unlock()
		return
	}
	// Remove the previously yanked text and insert the ring's previous entry.
	prevText := i.killRing[i.killIndex]
	prevLen := int64(len([]rune(prevText)))
	if i.cursor >= prevLen {
		i.runes = append(i.runes[:i.cursor-prevLen], i.runes[i.cursor:]...)
		i.cursor -= prevLen
	}
	i.killIndex--
	if i.killIndex < 0 {
		i.killIndex = int64(len(i.killRing) - 1)
	}
	text := i.killRing[i.killIndex]
	i.insertStringLocked(text)
	v := string(i.runes)
	fn := i.onChange
	i.mu.Unlock()
	if fn != nil {
		fn(v)
	}
}

func (i *Input) insertStringLocked(s string) {
	if i.allSelected {
		i.clearSelectionContentLocked()
	}
	rs := []rune(s)
	before := i.runes[:i.cursor]
	after := i.runes[i.cursor:]
	newRunes := make([]rune, 0, len(i.runes)+len(rs))
	newRunes = append(newRunes, before...)
	newRunes = append(newRunes, rs...)
	newRunes = append(newRunes, after...)
	i.runes = newRunes
	i.cursor += int64(len(rs))
	i.allSelected = false
}

func (i *Input) clearSelectionContentLocked() {
	i.runes = nil
	i.cursor = 0
	i.scroll = 0
	i.allSelected = false
	i.lastKillOp = false
}

func (i *Input) submit() {
	i.mu.Lock()
	val := string(i.runes)
	fn := i.onSubmit
	i.mu.Unlock()
	if fn != nil {
		fn(val)
	}
}

func (i *Input) historyPrev() {
	i.mu.RLock()
	fn := i.onHistoryPrev
	i.mu.RUnlock()
	if fn == nil {
		return
	}
	if v, ok := fn(); ok {
		i.SetValue(v)
	}
}

func (i *Input) historyNext() {
	i.mu.RLock()
	fn := i.onHistoryNext
	i.mu.RUnlock()
	if fn == nil {
		return
	}
	if v, ok := fn(); ok {
		i.SetValue(v)
	}
}
