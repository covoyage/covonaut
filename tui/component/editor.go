package component

import (
	"strings"
	"sync"

	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/terminal"
	"github.com/covoyage/covonaut/tui/theme"
)

// ---------------------------------------------------------------------------
// Editor — multi-line text editor component.
//
// Features:
//   - Multi-line buffer, hard newlines via Shift+Enter / Alt+Enter.
//   - Enter submits (OnSubmit); Ctrl+J inserts newline regardless.
//   - Emacs-style keybindings for cursor motion and deletion (reuses the
//     tui.editor.* keybindings).
//   - Soft wrap at viewport width (CJK-aware via VisibleWidth).
//   - Kill-ring (Ctrl+Y / Alt+Y).
//   - Undo/redo stack (Ctrl+Z / Ctrl+Shift+Z).
//   - Auto-growing height with MinRows / MaxRows caps.
//   - CURSOR_MARKER emitted so the TUI can drive the hardware cursor for IME.
// ---------------------------------------------------------------------------

// Editor is a Focusable multi-line editor component.
type Editor struct {
	mu sync.RWMutex

	lines [][]rune // one entry per hard line
	row   int64    // 0-based cursor row
	col   int64    // 0-based cursor rune col within the row

	allSelected bool // editor-scoped selection created by Select All

	minRows int64
	maxRows int64
	padX    int64

	promptFirst string // prompt on first visual line
	promptCont  string // prompt on continuation lines
	promptFn    func(string) string
	textFn      func(string) string
	placeText   string
	placeFn     func(string) string

	focused        bool
	focusIndicator string
	km             *terminal.KeybindingsManager

	killRing  []string
	killIndex int64
	lastKill  bool

	history    []editorSnapshot
	future     []editorSnapshot
	historyMax int64

	// inputHistory stores submitted values for up/down recall.
	inputHistory      []string
	inputHistoryIndex int64 // -1 = not browsing history, >=0 = index into inputHistory
	inputHistoryMax   int64
	inputHistoryDraft string // buffer saved when entering history browse mode

	onSubmit func(value string)
	onChange func(value string)
	onCancel func()
}

type editorSnapshot struct {
	lines [][]rune
	row   int64
	col   int64
}

// NewEditor creates a multi-line editor bound to km (nil = global).
func NewEditor(km *terminal.KeybindingsManager) *Editor {
	if km == nil {
		km = terminal.GetGlobalKeybindings()
	}
	return &Editor{
		lines:             [][]rune{{}},
		minRows:           1,
		maxRows:           10,
		promptFirst:       "> ",
		promptCont:        "  ",
		km:                km,
		historyMax:        200,
		inputHistoryMax:   1000,
		inputHistoryIndex: -1,
	}
}

// SetPrompt sets the first-line and continuation-line prompt strings.
func (e *Editor) SetPrompt(first, cont string) {
	e.mu.Lock()
	e.promptFirst = first
	e.promptCont = cont
	e.mu.Unlock()
}

// SetPromptFn styles the prompt.
func (e *Editor) SetPromptFn(fn func(string) string) { e.mu.Lock(); e.promptFn = fn; e.mu.Unlock() }

// SetTextFn styles the body text.
func (e *Editor) SetTextFn(fn func(string) string) { e.mu.Lock(); e.textFn = fn; e.mu.Unlock() }

// SetPlaceholder sets text shown when empty & unfocused.
func (e *Editor) SetPlaceholder(s string) { e.mu.Lock(); e.placeText = s; e.mu.Unlock() }

// SetPlaceholderFn styles the placeholder.
func (e *Editor) SetPlaceholderFn(fn func(string) string) {
	e.mu.Lock()
	e.placeFn = fn
	e.mu.Unlock()
}

// SetMinRows / SetMaxRows control auto-growth of the component height.
func (e *Editor) SetMinRows(n int64) {
	if n < 1 {
		n = 1
	}
	e.mu.Lock()
	e.minRows = n
	e.mu.Unlock()
}
func (e *Editor) SetMaxRows(n int64) {
	if n < 1 {
		n = 1
	}
	e.mu.Lock()
	e.maxRows = n
	e.mu.Unlock()
}

// SetPaddingX sets left/right padding (cells).
func (e *Editor) SetPaddingX(n int64) {
	if n < 0 {
		n = 0
	}
	e.mu.Lock()
	e.padX = n
	e.mu.Unlock()
}

// SetValue overwrites the buffer and moves the cursor to the end.
func (e *Editor) SetValue(s string) {
	e.mu.Lock()
	e.pushSnapshotLocked()
	raw := strings.Split(s, "\n")
	lines := make([][]rune, 0, len(raw))
	for _, line := range raw {
		lines = append(lines, []rune(line))
	}
	if len(lines) == 0 {
		lines = append(lines, []rune{})
	}
	e.lines = lines
	e.row = int64(len(lines) - 1)
	e.col = int64(len(e.lines[e.row]))
	e.allSelected = false
	fn := e.onChange
	val := e.valueLocked()
	e.mu.Unlock()
	if fn != nil {
		fn(val)
	}
}

// GetValue returns the current buffer as a string.
func (e *Editor) GetValue() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.valueLocked()
}

func (e *Editor) valueLocked() string {
	segs := make([]string, len(e.lines))
	for i, ln := range e.lines {
		segs[i] = string(ln)
	}
	return strings.Join(segs, "\n")
}

// Clear empties the buffer.
func (e *Editor) Clear() { e.SetValue("") }

// SelectAll selects the editor buffer without affecting terminal-level text selection.
func (e *Editor) SelectAll() {
	e.mu.Lock()
	e.allSelected = e.valueLocked() != ""
	e.row = int64(len(e.lines) - 1)
	e.col = int64(len(e.lines[e.row]))
	e.mu.Unlock()
}

func (e *Editor) GetSelectedText() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if !e.allSelected {
		return ""
	}
	return e.valueLocked()
}

func (e *Editor) ClearSelection() {
	e.mu.Lock()
	e.allSelected = false
	e.mu.Unlock()
}

// OnSubmit / OnChange / OnCancel register callbacks.
func (e *Editor) OnSubmit(fn func(string)) { e.mu.Lock(); e.onSubmit = fn; e.mu.Unlock() }
func (e *Editor) OnChange(fn func(string)) { e.mu.Lock(); e.onChange = fn; e.mu.Unlock() }
func (e *Editor) OnCancel(fn func())       { e.mu.Lock(); e.onCancel = fn; e.mu.Unlock() }

// SetFocused / IsFocused implement Focusable.
func (e *Editor) SetFocused(on bool) { e.mu.Lock(); e.focused = on; e.mu.Unlock() }
func (e *Editor) IsFocused() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.focused
}

func (e *Editor) SetFocusIndicator(indicator string) {
	e.mu.Lock()
	e.focusIndicator = indicator
	e.mu.Unlock()
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

// Render soft-wraps the buffer to width, applies prompts, and inserts
// CURSOR_MARKER at the current cursor position (when focused).
func (e *Editor) Render(width int64) []string {
	e.mu.Lock()
	defer e.mu.Unlock()

	pad := repeatSpace(e.padX)
	firstPrompt := e.promptFirst
	contPrompt := e.promptCont
	promptFn := e.promptFn
	textFn := e.textFn
	placeText := e.placeText
	placeFn := e.placeFn

	if promptFn == nil {
		promptFn = func(s string) string { return s }
	}
	if textFn == nil {
		textFn = func(s string) string { return s }
	}
	if placeFn == nil {
		placeFn = theme.CurrentPalette().Dim.Render
	}

	promptWFirst := core.VisibleWidth(firstPrompt)
	promptWCont := core.VisibleWidth(contPrompt)
	innerWidth := width - e.padX - promptWFirst
	if innerWidth < 1 {
		innerWidth = 1
	}
	contInner := width - e.padX - promptWCont
	if contInner < 1 {
		contInner = 1
	}

	// Empty + unfocused = placeholder.
	if len(e.lines) == 1 && len(e.lines[0]) == 0 && !e.focused && placeText != "" {
		line := pad + promptFn(firstPrompt) + placeFn(core.TruncateToWidth(placeText, innerWidth, "…"))
		out := []string{core.PadToWidth(line, width)}
		for int64(len(out)) < e.minRows {
			out = append(out, core.PadToWidth("", width))
		}
		return out
	}

	type visual struct {
		text       string
		isFirstSeg bool
		hardRow    int64
		colOffset  int64 // rune offset within hard row at which this segment starts
	}
	var visuals []visual
	for i, ln := range e.lines {
		if len(ln) == 0 {
			visuals = append(visuals, visual{text: "", isFirstSeg: true, hardRow: int64(i), colOffset: 0})
			continue
		}
		offset := int64(0)
		first := true
		for offset < int64(len(ln)) {
			w := innerWidth
			if !first {
				w = contInner
			}
			slice := core.SliceRunesByCells(ln, core.CellWidthOfRunes(ln, 0, offset), core.CellWidthOfRunes(ln, 0, offset)+w)
			if slice.EndR == slice.StartR {
				// cannot fit any rune (edge case) — just take 1 rune
				slice.StartR = offset
				if offset < int64(len(ln)) {
					slice.EndR = offset + 1
				}
				slice.Text = string(ln[offset:slice.EndR])
			}
			visuals = append(visuals, visual{
				text:       slice.Text,
				isFirstSeg: first,
				hardRow:    int64(i),
				colOffset:  offset,
			})
			offset = slice.EndR
			first = false
		}
	}

	// Compute cursor visual row/col.
	var cursorV, cursorCol int64 = -1, -1
	if e.focused {
		for vi, v := range visuals {
			if v.hardRow != e.row {
				continue
			}
			segLen := int64(len([]rune(v.text)))
			segEnd := v.colOffset + segLen
			if e.col >= v.colOffset && e.col <= segEnd {
				cursorV = int64(vi)
				cursorCol = core.CellWidthOfRunes(e.lines[e.row], v.colOffset, e.col)
				break
			}
		}
	}

	var out []string
	for idx, v := range visuals {
		prompt := firstPrompt
		innerW := innerWidth
		if !v.isFirstSeg {
			prompt = contPrompt
			innerW = contInner
		}
		bodyText := textFn(v.text)
		if e.allSelected && v.text != "" {
			bodyText = "\x1b[48;5;33m" + core.StripAnsi(bodyText) + "\x1b[0m"
		}
		body := core.PadToWidth(bodyText, innerW)
		if int64(idx) == cursorV {
			body = core.InsertMarker(body, cursorCol)
		}
		line := pad + promptFn(prompt) + body
		out = append(out, core.PadToWidth(line, width))
	}
	// Growth policy.
	for int64(len(out)) < e.minRows {
		emptyPrompt := firstPrompt
		emptyInner := innerWidth
		emptyPad := pad
		body := core.PadToWidth("", emptyInner)
		line := emptyPad + promptFn(emptyPrompt) + body
		out = append(out, core.PadToWidth(line, width))
	}
	if int64(len(out)) > e.maxRows {
		// Keep the segment containing the cursor visible; drop leading rows.
		if cursorV >= e.maxRows {
			drop := cursorV - e.maxRows + 1
			out = out[drop:]
		} else {
			out = out[:e.maxRows]
		}
	}

	if e.focused && e.focusIndicator != "" {
		if len(out) > 0 {
			last := len(out) - 1
			out[last] = e.focusIndicator + out[last]
		}
	}

	return out
}

// Invalidate is a no-op (no cache).
func (e *Editor) Invalidate() {}

func (e *Editor) Update(msg core.Msg) core.Cmd {
	switch m := msg.(type) {
	case core.KeyMsg:
		e.processKeys(m.Data)
	case core.PasteMsg:
		e.processKeys(m.Text)
	case core.WindowSizeMsg:
		e.Invalidate()
	}
	return nil
}

func (e *Editor) processKeys(data string) {
	keys := terminal.ParseKeys(data)
	if len(keys) == 0 {
		return
	}
	km := e.km
	for _, k := range keys {
		raw := k.Raw
		switch {
		case km.Matches(raw, "tui.input.newLine"):
			e.insertRune('\n')
		case km.Matches(raw, "tui.input.submit"):
			e.submit()
		case km.Matches(raw, "tui.editor.selectAll"):
			e.SelectAll()
		case km.Matches(raw, "tui.editor.cursorLeft"):
			e.moveCursor(0, -1)
		case km.Matches(raw, "tui.editor.cursorRight"):
			e.moveCursor(0, 1)
		case km.Matches(raw, "tui.editor.cursorUp"):
			if e.focused && e.row == 0 && e.historyPrev() {
			} else {
				e.moveCursor(-1, 0)
			}
		case km.Matches(raw, "tui.editor.cursorDown"):
			if e.focused && e.row >= int64(len(e.lines)-1) && e.historyNext() {
			} else {
				e.moveCursor(1, 0)
			}
		case km.Matches(raw, "tui.editor.cursorWordLeft"):
			e.moveWord(-1)
		case km.Matches(raw, "tui.editor.cursorWordRight"):
			e.moveWord(1)
		case km.Matches(raw, "tui.editor.cursorLineStart"):
			e.mu.Lock()
			e.allSelected = false
			e.col = 0
			e.mu.Unlock()
		case km.Matches(raw, "tui.editor.cursorLineEnd"):
			e.mu.Lock()
			e.allSelected = false
			e.col = int64(len(e.lines[e.row]))
			e.mu.Unlock()
		case km.Matches(raw, "tui.editor.deleteCharBackward"):
			e.deleteBackward()
		case km.Matches(raw, "tui.editor.deleteCharForward"):
			e.deleteForward()
		case km.Matches(raw, "tui.editor.deleteWordBackward"):
			e.deleteWordBackward()
		case km.Matches(raw, "tui.editor.deleteWordForward"):
			e.deleteWordForward()
		case km.Matches(raw, "tui.editor.deleteToLineStart"):
			e.deleteToLineStart()
		case km.Matches(raw, "tui.editor.deleteToLineEnd"):
			e.deleteToLineEnd()
		case km.Matches(raw, "tui.editor.yank"):
			e.yank()
		case km.Matches(raw, "tui.editor.yankPop"):
			e.yankPop()
		case km.Matches(raw, "tui.editor.undo"):
			e.undo()
		case km.Matches(raw, "ctrl+shift+z"), km.Matches(raw, "ctrl+y"):
			e.redo()
		default:
			if k.Rune == '\n' || k.Rune == '\r' {
				continue
			}
			if k.IsPrintable() {
				e.insertRune(k.Rune)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Editing
// ---------------------------------------------------------------------------

func (e *Editor) insertRune(r rune) {
	e.mu.Lock()
	e.pushSnapshotLocked()
	if e.allSelected {
		e.clearSelectionContentLocked()
	}
	if r == '\n' {
		cur := e.lines[e.row]
		before := append([]rune{}, cur[:e.col]...)
		after := append([]rune{}, cur[e.col:]...)
		e.lines[e.row] = before
		newLines := make([][]rune, 0, len(e.lines)+1)
		newLines = append(newLines, e.lines[:e.row+1]...)
		newLines = append(newLines, after)
		newLines = append(newLines, e.lines[e.row+1:]...)
		e.lines = newLines
		e.row++
		e.col = 0
	} else {
		cur := e.lines[e.row]
		newLine := make([]rune, 0, len(cur)+1)
		newLine = append(newLine, cur[:e.col]...)
		newLine = append(newLine, r)
		newLine = append(newLine, cur[e.col:]...)
		e.lines[e.row] = newLine
		e.col++
	}
	e.lastKill = false
	e.allSelected = false
	fn := e.onChange
	v := e.valueLocked()
	e.mu.Unlock()
	if fn != nil {
		fn(v)
	}
}

func (e *Editor) moveCursor(dRow, dCol int64) {
	e.mu.Lock()
	e.allSelected = false
	if dRow != 0 {
		e.row += dRow
		if e.row < 0 {
			e.row = 0
		}
		if e.row >= int64(len(e.lines)) {
			e.row = int64(len(e.lines) - 1)
		}
		if e.col > int64(len(e.lines[e.row])) {
			e.col = int64(len(e.lines[e.row]))
		}
	}
	if dCol != 0 {
		e.col += dCol
		if e.col < 0 {
			if e.row > 0 {
				e.row--
				e.col = int64(len(e.lines[e.row]))
			} else {
				e.col = 0
			}
		}
		if e.col > int64(len(e.lines[e.row])) {
			if e.row < int64(len(e.lines)-1) {
				e.row++
				e.col = 0
			} else {
				e.col = int64(len(e.lines[e.row]))
			}
		}
	}
	e.lastKill = false
	e.mu.Unlock()
}

func (e *Editor) moveWord(delta int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.allSelected = false
	if delta < 0 {
		if e.col == 0 && e.row > 0 {
			e.row--
			e.col = int64(len(e.lines[e.row]))
			return
		}
		e.col = core.FindWordBoundaryLeft(e.lines[e.row], e.col)
	} else {
		if e.col == int64(len(e.lines[e.row])) && e.row < int64(len(e.lines)-1) {
			e.row++
			e.col = 0
			return
		}
		e.col = core.FindWordBoundaryRight(e.lines[e.row], e.col)
	}
	e.lastKill = false
}

func (e *Editor) deleteBackward() {
	e.mu.Lock()
	e.pushSnapshotLocked()
	if e.allSelected {
		e.clearSelectionContentLocked()
		fn := e.onChange
		v := e.valueLocked()
		e.mu.Unlock()
		if fn != nil {
			fn(v)
		}
		return
	}
	if e.col == 0 {
		if e.row == 0 {
			e.mu.Unlock()
			return
		}
		prev := e.lines[e.row-1]
		cur := e.lines[e.row]
		e.col = int64(len(prev))
		e.lines[e.row-1] = append(prev, cur...)
		e.lines = append(e.lines[:e.row], e.lines[e.row+1:]...)
		e.row--
	} else {
		cur := e.lines[e.row]
		e.lines[e.row] = append(cur[:e.col-1], cur[e.col:]...)
		e.col--
	}
	fn := e.onChange
	v := e.valueLocked()
	e.lastKill = false
	e.allSelected = false
	e.mu.Unlock()
	if fn != nil {
		fn(v)
	}
}

func (e *Editor) deleteForward() {
	e.mu.Lock()
	e.pushSnapshotLocked()
	if e.allSelected {
		e.clearSelectionContentLocked()
		fn := e.onChange
		v := e.valueLocked()
		e.mu.Unlock()
		if fn != nil {
			fn(v)
		}
		return
	}
	cur := e.lines[e.row]
	if e.col >= int64(len(cur)) {
		if e.row >= int64(len(e.lines)-1) {
			e.mu.Unlock()
			return
		}
		next := e.lines[e.row+1]
		e.lines[e.row] = append(cur, next...)
		e.lines = append(e.lines[:e.row+1], e.lines[e.row+2:]...)
	} else {
		e.lines[e.row] = append(cur[:e.col], cur[e.col+1:]...)
	}
	fn := e.onChange
	v := e.valueLocked()
	e.lastKill = false
	e.allSelected = false
	e.mu.Unlock()
	if fn != nil {
		fn(v)
	}
}

func (e *Editor) deleteWordBackward() {
	e.mu.Lock()
	e.pushSnapshotLocked()
	if e.allSelected {
		e.clearSelectionContentLocked()
		fn := e.onChange
		v := e.valueLocked()
		e.mu.Unlock()
		if fn != nil {
			fn(v)
		}
		return
	}
	if e.col == 0 {
		if e.row == 0 {
			e.mu.Unlock()
			return
		}
		prev := e.lines[e.row-1]
		cur := e.lines[e.row]
		e.col = int64(len(prev))
		e.lines[e.row-1] = append(prev, cur...)
		e.lines = append(e.lines[:e.row], e.lines[e.row+1:]...)
		e.row--
		e.mu.Unlock()
		return
	}
	start := core.FindWordBoundaryLeft(e.lines[e.row], e.col)
	killed := string(e.lines[e.row][start:e.col])
	e.lines[e.row] = append(e.lines[e.row][:start], e.lines[e.row][e.col:]...)
	e.col = start
	e.pushKillRingLocked(killed)
	fn := e.onChange
	v := e.valueLocked()
	e.mu.Unlock()
	if fn != nil {
		fn(v)
	}
}

func (e *Editor) deleteWordForward() {
	e.mu.Lock()
	e.pushSnapshotLocked()
	if e.allSelected {
		e.clearSelectionContentLocked()
		fn := e.onChange
		v := e.valueLocked()
		e.mu.Unlock()
		if fn != nil {
			fn(v)
		}
		return
	}
	cur := e.lines[e.row]
	if e.col >= int64(len(cur)) {
		if e.row >= int64(len(e.lines)-1) {
			e.mu.Unlock()
			return
		}
		next := e.lines[e.row+1]
		e.lines[e.row] = append(cur, next...)
		e.lines = append(e.lines[:e.row+1], e.lines[e.row+2:]...)
		e.mu.Unlock()
		return
	}
	end := core.FindWordBoundaryRight(cur, e.col)
	killed := string(cur[e.col:end])
	e.lines[e.row] = append(cur[:e.col], cur[end:]...)
	e.pushKillRingLocked(killed)
	fn := e.onChange
	v := e.valueLocked()
	e.mu.Unlock()
	if fn != nil {
		fn(v)
	}
}

func (e *Editor) deleteToLineStart() {
	e.mu.Lock()
	e.pushSnapshotLocked()
	if e.allSelected {
		e.clearSelectionContentLocked()
		fn := e.onChange
		v := e.valueLocked()
		e.mu.Unlock()
		if fn != nil {
			fn(v)
		}
		return
	}
	cur := e.lines[e.row]
	if e.col == 0 {
		e.mu.Unlock()
		return
	}
	killed := string(cur[:e.col])
	e.lines[e.row] = cur[e.col:]
	e.col = 0
	e.pushKillRingLocked(killed)
	fn := e.onChange
	v := e.valueLocked()
	e.mu.Unlock()
	if fn != nil {
		fn(v)
	}
}

func (e *Editor) deleteToLineEnd() {
	e.mu.Lock()
	e.pushSnapshotLocked()
	if e.allSelected {
		e.clearSelectionContentLocked()
		fn := e.onChange
		v := e.valueLocked()
		e.mu.Unlock()
		if fn != nil {
			fn(v)
		}
		return
	}
	cur := e.lines[e.row]
	if e.col >= int64(len(cur)) {
		// Merge with next line if any.
		if e.row < int64(len(e.lines)-1) {
			next := e.lines[e.row+1]
			e.lines[e.row] = append(cur, next...)
			e.lines = append(e.lines[:e.row+1], e.lines[e.row+2:]...)
			e.pushKillRingLocked("\n")
		}
		e.mu.Unlock()
		return
	}
	killed := string(cur[e.col:])
	e.lines[e.row] = cur[:e.col]
	e.pushKillRingLocked(killed)
	fn := e.onChange
	v := e.valueLocked()
	e.mu.Unlock()
	if fn != nil {
		fn(v)
	}
}

func (e *Editor) pushKillRingLocked(s string) {
	if s == "" {
		return
	}
	e.killRing = append(e.killRing, s)
	if len(e.killRing) > 64 {
		e.killRing = e.killRing[len(e.killRing)-64:]
	}
	e.killIndex = int64(len(e.killRing) - 1)
	e.lastKill = true
}

func (e *Editor) yank() {
	e.mu.Lock()
	if len(e.killRing) == 0 {
		e.mu.Unlock()
		return
	}
	e.pushSnapshotLocked()
	text := e.killRing[e.killIndex]
	e.insertStringLocked(text)
	e.lastKill = true
	fn := e.onChange
	v := e.valueLocked()
	e.mu.Unlock()
	if fn != nil {
		fn(v)
	}
}

func (e *Editor) yankPop() {
	e.mu.Lock()
	if !e.lastKill || len(e.killRing) == 0 {
		e.mu.Unlock()
		return
	}
	e.pushSnapshotLocked()
	prev := e.killRing[e.killIndex]
	e.removeBeforeCursorLocked(int64(len([]rune(prev))))
	e.killIndex--
	if e.killIndex < 0 {
		e.killIndex = int64(len(e.killRing) - 1)
	}
	e.insertStringLocked(e.killRing[e.killIndex])
	fn := e.onChange
	v := e.valueLocked()
	e.mu.Unlock()
	if fn != nil {
		fn(v)
	}
}

func (e *Editor) insertStringLocked(s string) {
	if e.allSelected {
		e.clearSelectionContentLocked()
	}
	for _, r := range s {
		if r == '\n' {
			cur := e.lines[e.row]
			before := append([]rune{}, cur[:e.col]...)
			after := append([]rune{}, cur[e.col:]...)
			e.lines[e.row] = before
			newLines := make([][]rune, 0, len(e.lines)+1)
			newLines = append(newLines, e.lines[:e.row+1]...)
			newLines = append(newLines, after)
			newLines = append(newLines, e.lines[e.row+1:]...)
			e.lines = newLines
			e.row++
			e.col = 0
			continue
		}
		cur := e.lines[e.row]
		newLine := make([]rune, 0, len(cur)+1)
		newLine = append(newLine, cur[:e.col]...)
		newLine = append(newLine, r)
		newLine = append(newLine, cur[e.col:]...)
		e.lines[e.row] = newLine
		e.col++
	}
	e.allSelected = false
}

func (e *Editor) clearSelectionContentLocked() {
	e.lines = [][]rune{{}}
	e.row = 0
	e.col = 0
	e.allSelected = false
	e.lastKill = false
}

func (e *Editor) removeBeforeCursorLocked(n int64) {
	// Remove n runes immediately before the cursor (may span line breaks).
	for n > 0 {
		if e.col >= n {
			cur := e.lines[e.row]
			e.lines[e.row] = append(cur[:e.col-n], cur[e.col:]...)
			e.col -= n
			return
		}
		n -= e.col
		if e.row == 0 {
			e.lines[e.row] = e.lines[e.row][e.col:]
			e.col = 0
			return
		}
		// Merge with previous line.
		prev := e.lines[e.row-1]
		cur := e.lines[e.row][e.col:]
		e.col = int64(len(prev))
		e.lines[e.row-1] = append(prev, cur...)
		e.lines = append(e.lines[:e.row], e.lines[e.row+1:]...)
		e.row--
		n-- // the newline itself counted as one rune
	}
}

// ---------------------------------------------------------------------------
// Undo / redo
// ---------------------------------------------------------------------------

func (e *Editor) pushSnapshotLocked() {
	snap := editorSnapshot{
		lines: cloneRuneLines(e.lines),
		row:   e.row,
		col:   e.col,
	}
	e.history = append(e.history, snap)
	if int64(len(e.history)) > e.historyMax {
		e.history = e.history[len(e.history)-int(e.historyMax):]
	}
	e.future = nil
}

func (e *Editor) undo() {
	e.mu.Lock()
	if len(e.history) == 0 {
		e.mu.Unlock()
		return
	}
	current := editorSnapshot{
		lines: cloneRuneLines(e.lines),
		row:   e.row,
		col:   e.col,
	}
	snap := e.history[len(e.history)-1]
	e.history = e.history[:len(e.history)-1]
	e.future = append(e.future, current)
	e.lines = snap.lines
	e.row = snap.row
	e.col = snap.col
	e.allSelected = false
	fn := e.onChange
	v := e.valueLocked()
	e.mu.Unlock()
	if fn != nil {
		fn(v)
	}
}

func (e *Editor) redo() {
	e.mu.Lock()
	if len(e.future) == 0 {
		e.mu.Unlock()
		return
	}
	current := editorSnapshot{
		lines: cloneRuneLines(e.lines),
		row:   e.row,
		col:   e.col,
	}
	snap := e.future[len(e.future)-1]
	e.future = e.future[:len(e.future)-1]
	e.history = append(e.history, current)
	e.lines = snap.lines
	e.row = snap.row
	e.col = snap.col
	e.allSelected = false
	fn := e.onChange
	v := e.valueLocked()
	e.mu.Unlock()
	if fn != nil {
		fn(v)
	}
}

func cloneRuneLines(lines [][]rune) [][]rune {
	out := make([][]rune, len(lines))
	for i, ln := range lines {
		cp := make([]rune, len(ln))
		copy(cp, ln)
		out[i] = cp
	}
	return out
}

// submit invokes OnSubmit with the full value.
func (e *Editor) submit() {
	e.mu.RLock()
	val := e.valueLocked()
	fn := e.onSubmit
	e.mu.RUnlock()
	if fn != nil {
		fn(val)
	}
}

// ---------------------------------------------------------------------------
// Input history (up/down arrow recall)
// ---------------------------------------------------------------------------

// PushInputHistory saves a submitted value to the recall history.
func (e *Editor) PushInputHistory(value string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if value == "" {
		return
	}
	// Avoid duplicating the most recent entry.
	if len(e.inputHistory) > 0 && e.inputHistory[len(e.inputHistory)-1] == value {
		return
	}
	e.inputHistory = append(e.inputHistory, value)
	if int64(len(e.inputHistory)) > e.inputHistoryMax {
		e.inputHistory = e.inputHistory[len(e.inputHistory)-int(e.inputHistoryMax):]
	}
}

// historyPrev recalls the previous (older) input from history.
// Returns true if history was consumed (cursor movement should be suppressed).
func (e *Editor) historyPrev() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.inputHistory) == 0 {
		return false
	}
	if e.inputHistoryIndex < 0 {
		// First time entering history mode — save current draft.
		e.inputHistoryDraft = e.valueLocked()
		e.inputHistoryIndex = int64(len(e.inputHistory) - 1)
	} else if e.inputHistoryIndex > 0 {
		e.inputHistoryIndex--
	} else {
		// Already at oldest entry; stay there.
		return true
	}
	e.setValueLocked(e.inputHistory[e.inputHistoryIndex])
	return true
}

// historyNext recalls the next (newer) input from history, or restores the
// draft when moving past the newest entry. Returns true if history was consumed.
func (e *Editor) historyNext() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.inputHistoryIndex < 0 {
		return false
	}
	e.inputHistoryIndex++
	if int(e.inputHistoryIndex) >= len(e.inputHistory) {
		// Past newest — restore draft and exit history mode.
		e.setValueLocked(e.inputHistoryDraft)
		e.inputHistoryIndex = -1
		e.inputHistoryDraft = ""
	} else {
		e.setValueLocked(e.inputHistory[e.inputHistoryIndex])
	}
	return true
}

// setValueLocked overwrites the buffer without pushing an undo snapshot.
func (e *Editor) setValueLocked(s string) {
	raw := strings.Split(s, "\n")
	lines := make([][]rune, 0, len(raw))
	for _, line := range raw {
		lines = append(lines, []rune(line))
	}
	if len(lines) == 0 {
		lines = append(lines, []rune{})
	}
	e.lines = lines
	e.row = int64(len(lines) - 1)
	e.col = int64(len(e.lines[e.row]))
}
