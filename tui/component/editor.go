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
//   - Bracketed paste inserts text (including hard newlines) and never submits.
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

	// Mouse-drag text selection — independent of allSelected/Select All.
	// selStart/selEnd are buffer positions (hard row + rune column);
	// selDragging is true while the mouse button is held down.
	selDragging bool
	selActive   bool
	selStart    editorSelPos
	selEnd      editorSelPos

	// lastVisuals/lastPadX/lastPromptW* cache the layout computed by the
	// most recent Render call, so MouseMsg events (which arrive between
	// renders, with screen-relative row/col) can be translated back into
	// logical buffer positions for hit-testing.
	lastVisuals      []editorVisualRow
	lastPadX         int64
	lastPromptWFirst int64
	lastPromptWCont  int64

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

	// Ghost text — inline completion preview (Copilot-style).
	ghostText string // the suggested completion text
	ghostCol  int64  // rune offset in the current line where ghost starts (cursor position at request time)
	ghostRow  int64  // hard row index where the ghost applies

	onSubmit    func(value string)
	onChange    func(value string)
	onCancel    func()
	onTextPaste func(text string) (replacement string, consume bool)

	burst pasteBurst

	chips    map[rune]string
	nextChip rune

	// Optional vim modal editing. When enabled, the editor starts in insert
	// mode so typing still works; Esc enters normal mode (hjkl / i / a / x / dd).
	vimEnabled bool
	vimInsert  bool
	vimPending string
}

type editorSnapshot struct {
	lines [][]rune
	chips map[rune]string
	row   int64
	col   int64
}

// editorSelPos identifies a buffer position for mouse-drag selection: row is
// an index into Editor.lines (a "hard" line), col is a rune offset within it.
type editorSelPos struct {
	row int64
	col int64
}

// editorVisualRow records, for one rendered screen row, which hard line it
// came from and the rune range [colStart, colEnd) of that line it displays.
// hardRow is -1 for rows with no backing content (e.g. MinRows growth
// padding beyond the buffer's actual line count).
type editorVisualRow struct {
	hardRow        int64
	colStart       int64
	colEnd         int64
	isFirstSeg     bool
	useFirstPrompt bool // true only for the composer prompt row
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

// SetVimMode enables or disables vim-style modal editing.
// When turning on, the editor starts in insert mode so the user can type immediately.
func (e *Editor) SetVimMode(on bool) {
	e.mu.Lock()
	e.vimEnabled = on
	e.vimInsert = true
	e.vimPending = ""
	e.mu.Unlock()
}

// VimMode reports whether vim-style modal editing is enabled.
func (e *Editor) VimMode() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.vimEnabled
}

// VimInsertMode reports whether the editor is currently inserting text.
// When vim mode is off this is always true.
func (e *Editor) VimInsertMode() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return !e.vimEnabled || e.vimInsert
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
	s = e.encodeChipsLocked(s)
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
	e.selActive = false
	e.selDragging = false
	e.ghostText = ""
	fn := e.onChange
	val := e.valueLocked()
	e.mu.Unlock()
	if fn != nil {
		fn(val)
	}
}

// SetGhost sets the inline completion ghost text. The text is displayed as a
// dimmed suffix after the cursor on the current line. Call with "" to clear.
func (e *Editor) SetGhost(text string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if text == "" {
		e.ghostText = ""
		return
	}
	e.ghostText = text
	e.ghostRow = e.row
	e.ghostCol = e.col
}

// ClearGhost removes the current ghost text.
func (e *Editor) ClearGhost() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ghostText = ""
}

// AcceptGhost inserts the ghost text into the buffer at the cursor position.
// Returns true if ghost was accepted.
func (e *Editor) AcceptGhost() bool {
	e.mu.Lock()
	if e.ghostText == "" {
		e.mu.Unlock()
		return false
	}
	e.pushSnapshotLocked()
	e.insertStringLocked(e.ghostText)
	e.ghostText = ""
	fn := e.onChange
	v := e.valueLocked()
	e.mu.Unlock()
	if fn != nil {
		fn(v)
	}
	return true
}

// GhostText returns the current ghost text (empty if none).
func (e *Editor) GhostText() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.ghostText
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
		segs[i] = e.expandLineLocked(ln)
	}
	return strings.Join(segs, "\n")
}

// EditorDraft is a composer snapshot: buffer, paste-chip map, and cursor.
type EditorDraft struct {
	Lines [][]rune
	Chips map[rune]string
	Row   int64
	Col   int64
}

// Empty reports whether the draft has no buffer content.
func (d EditorDraft) Empty() bool {
	if len(d.Lines) == 0 {
		return true
	}
	return len(d.Lines) == 1 && len(d.Lines[0]) == 0
}

// CaptureDraft copies the current buffer, chips, and cursor.
func (e *Editor) CaptureDraft() EditorDraft {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return EditorDraft{
		Lines: cloneRuneLines(e.lines),
		Chips: cloneChipMap(e.chips),
		Row:   e.row,
		Col:   e.col,
	}
}

// RestoreDraft replaces the buffer with a previously captured draft.
func (e *Editor) RestoreDraft(d EditorDraft) {
	e.mu.Lock()
	e.pushSnapshotLocked()
	if d.Empty() {
		e.lines = [][]rune{{}}
		e.chips = nil
		e.nextChip = chipMarkBase
		e.row = 0
		e.col = 0
	} else {
		e.lines = cloneRuneLines(d.Lines)
		e.chips = cloneChipMap(d.Chips)
		e.nextChip = chipMarkBase
		e.row = d.Row
		e.col = d.Col
		if len(e.lines) == 0 {
			e.lines = [][]rune{{}}
		}
		if e.row < 0 {
			e.row = 0
		}
		if e.row >= int64(len(e.lines)) {
			e.row = int64(len(e.lines) - 1)
		}
		if e.col < 0 {
			e.col = 0
		}
		if e.col > int64(len(e.lines[e.row])) {
			e.col = int64(len(e.lines[e.row]))
		}
	}
	e.allSelected = false
	e.clearMouseSelectionLocked()
	e.ghostText = ""
	fn := e.onChange
	v := e.valueLocked()
	e.mu.Unlock()
	if fn != nil {
		fn(v)
	}
}

// Clear empties the buffer.
func (e *Editor) Clear() { e.SetValue("") }

// SelectAll selects the editor buffer without affecting terminal-level text selection.
func (e *Editor) SelectAll() {
	e.mu.Lock()
	e.clearMouseSelectionLocked()
	e.allSelected = e.valueLocked() != ""
	e.row = int64(len(e.lines) - 1)
	e.col = int64(len(e.lines[e.row]))
	e.mu.Unlock()
}

func (e *Editor) GetSelectedText() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.allSelected {
		return e.valueLocked()
	}
	start, end, ok := e.normalizedSelectionLocked()
	if !ok || start.row < 0 || end.row >= int64(len(e.lines)) {
		return ""
	}
	var b strings.Builder
	for r := start.row; r <= end.row; r++ {
		line := e.lines[r]
		lo, hi := int64(0), int64(len(line))
		if r == start.row {
			lo = start.col
		}
		if r == end.row {
			hi = end.col
		}
		if lo < 0 {
			lo = 0
		}
		if hi > int64(len(line)) {
			hi = int64(len(line))
		}
		if hi < lo {
			hi = lo
		}
		b.WriteString(e.expandLineLocked(line[lo:hi]))
		if r != end.row {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// normalizedSelectionLocked returns the current mouse-drag selection with
// start <= end (row/col order), and whether a non-empty selection exists.
// Must be called with e.mu held (read or write lock).
func (e *Editor) normalizedSelectionLocked() (start, end editorSelPos, ok bool) {
	if !e.selActive || e.selStart == e.selEnd {
		return editorSelPos{}, editorSelPos{}, false
	}
	start, end = e.selStart, e.selEnd
	if start.row > end.row || (start.row == end.row && start.col > end.col) {
		start, end = end, start
	}
	return start, end, true
}

func (e *Editor) ClearSelection() {
	e.mu.Lock()
	e.allSelected = false
	e.selActive = false
	e.selDragging = false
	e.mu.Unlock()
}

// clearMouseSelection discards any active mouse-drag text selection. Exposed
// for callers outside the lock (e.g. explicit resets); mutating operations
// use clearMouseSelectionLocked directly since they already hold e.mu.
func (e *Editor) clearMouseSelection() {
	e.mu.Lock()
	e.clearMouseSelectionLocked()
	e.mu.Unlock()
}

// clearMouseSelectionLocked resets mouse-drag selection state. Must be
// called with e.mu already held.
func (e *Editor) clearMouseSelectionLocked() {
	e.selActive = false
	e.selDragging = false
}

// handleMouse implements click-drag text selection, mirroring ChatHistory's
// selection model but scoped to the editor's own buffer. This is purely an
// in-app mechanism layered on top of received MouseMsg events — it never
// needs (or benefits from) disabling terminal mouse reporting, so it has no
// effect on scroll-wheel/click behavior elsewhere in the app.
func (e *Editor) handleMouse(m core.MouseMsg) {
	e.mu.Lock()
	defer e.mu.Unlock()

	switch m.Action {
	case core.MousePress:
		row, col, ok := e.hitTestLocked(m.Row, m.Col)
		if !ok {
			return
		}
		e.allSelected = false
		e.selDragging = true
		e.selActive = true
		e.selStart = editorSelPos{row: row, col: col}
		e.selEnd = e.selStart
	case core.MouseMotion:
		if !e.selDragging {
			return
		}
		if row, col, ok := e.hitTestLocked(m.Row, m.Col); ok {
			e.selEnd = editorSelPos{row: row, col: col}
		}
	case core.MouseRelease:
		if !e.selDragging {
			return
		}
		e.selDragging = false
		if e.selStart == e.selEnd {
			e.selActive = false
		}
	}
}

// hitTestLocked translates a screen (row, col) coordinate — as delivered in
// a MouseMsg, relative to the editor's own rendered area — into a logical
// (hardRow, runeCol) buffer position, using the layout cached by the most
// recent Render call. ok is false when the coordinate falls outside any
// rendered content (e.g. growth-padding rows or stale layout).
func (e *Editor) hitTestLocked(screenRow, screenCol int64) (row, col int64, ok bool) {
	if screenRow < 0 || screenRow >= int64(len(e.lastVisuals)) {
		return 0, 0, false
	}
	v := e.lastVisuals[screenRow]
	if v.hardRow < 0 || v.hardRow >= int64(len(e.lines)) {
		return 0, 0, false
	}
	promptW := e.lastPromptWFirst
	if !v.useFirstPrompt {
		promptW = e.lastPromptWCont
	}
	innerCol := screenCol - e.lastPadX - promptW
	if innerCol < 0 {
		innerCol = 0
	}
	line := e.lines[v.hardRow]
	segStart, segEnd := v.colStart, v.colEnd
	if segStart < 0 {
		segStart = 0
	}
	if segEnd > int64(len(line)) {
		segEnd = int64(len(line))
	}
	if segEnd < segStart {
		segEnd = segStart
	}
	seg := line[segStart:segEnd]
	idx := e.runeIndexAtCellLocked(seg, innerCol)
	return v.hardRow, segStart + idx, true
}

// runeIndexAtCell returns the rune index within runes whose cumulative
// visible cell width first reaches or exceeds targetCell, clamped to
// len(runes). Used to map a mouse click's screen column back to a rune
// offset in CJK-aware (variable cell width) text.
func runeIndexAtCell(runes []rune, targetCell int64) int64 {
	if targetCell <= 0 {
		return 0
	}
	var col int64
	for i, r := range runes {
		w := core.RuneWidth(r)
		if col+w > targetCell {
			return int64(i)
		}
		col += w
	}
	return int64(len(runes))
}

// OnSubmit / OnChange / OnCancel register callbacks.
func (e *Editor) OnSubmit(fn func(string)) { e.mu.Lock(); e.onSubmit = fn; e.mu.Unlock() }
func (e *Editor) OnChange(fn func(string)) { e.mu.Lock(); e.onChange = fn; e.mu.Unlock() }
func (e *Editor) OnCancel(fn func())       { e.mu.Lock(); e.onCancel = fn; e.mu.Unlock() }

// OnTextPaste registers a rewrite hook for pasted text (bracketed or burst).
func (e *Editor) OnTextPaste(fn func(string) (string, bool)) {
	e.mu.Lock()
	e.onTextPaste = fn
	e.mu.Unlock()
}

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

	// Cache layout metrics so mouse events arriving between renders can be
	// translated back into logical (row, col) buffer positions.
	e.lastPadX = e.padX
	e.lastPromptWFirst = promptWFirst
	e.lastPromptWCont = promptWCont

	// Empty + unfocused = placeholder.
	if len(e.lines) == 1 && len(e.lines[0]) == 0 && !e.focused && placeText != "" {
		e.lastVisuals = nil
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
			composerFirst := i == 0 && first
			w := innerWidth
			if !composerFirst {
				w = contInner
			}
			slice := e.sliceByCellsLocked(ln, e.cellWidthLocked(ln, 0, offset), e.cellWidthLocked(ln, 0, offset)+w)
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
				cursorCol = e.cellWidthLocked(e.lines[e.row], v.colOffset, e.col)
				break
			}
		}
	}

	selStart, selEnd, hasSel := e.normalizedSelectionLocked()

	var out []string
	rowsMeta := make([]editorVisualRow, 0, len(visuals))
	for idx, v := range visuals {
		useFirstPrompt := idx == 0
		prompt := firstPrompt
		innerW := innerWidth
		if !useFirstPrompt {
			prompt = contPrompt
			innerW = contInner
		}
		segRunes := []rune(v.text)
		segLen := int64(len(segRunes))
		rowsMeta = append(rowsMeta, editorVisualRow{
			hardRow:        v.hardRow,
			colStart:       v.colOffset,
			colEnd:         v.colOffset + segLen,
			isFirstSeg:     v.isFirstSeg,
			useFirstPrompt: useFirstPrompt,
		})

		display := e.expandLineLocked(segRunes)
		var bodyText string
		switch {
		case e.allSelected && display != "":
			bodyText = "\x1b[48;5;33m" + core.StripAnsi(textFn(display)) + "\x1b[0m"
		case hasSel:
			if from, to, ok := selRangeInSegment(v.hardRow, v.colOffset, segLen, selStart, selEnd); ok && from < to {
				before := e.expandLineLocked(segRunes[:from])
				sel := e.expandLineLocked(segRunes[from:to])
				after := e.expandLineLocked(segRunes[to:])
				bodyText = before + "\x1b[48;5;33m" + sel + "\x1b[0m" + after
			} else {
				bodyText = textFn(display)
			}
		default:
			bodyText = textFn(display)
		}

		body := core.PadToWidth(bodyText, innerW)
		if int64(idx) == cursorV {
			body = core.InsertMarker(body, cursorCol)
			// Append ghost text (inline completion preview) as dimmed text
			// after the cursor, only on the first segment of the cursor row.
			if e.ghostText != "" && v.hardRow == e.ghostRow && v.isFirstSeg {
				ghostRunes := []rune(e.ghostText)
				// Truncate ge.cellWidthLocked width
				usedCells := core.CellWidthOfRunes(e.lines[e.row], 0, e.col)
				remaining := innerW - usedCells
				if remaining > 0 && int64(len(ghostRunes)) > 0 {
					ghostDisplay := core.TruncateToWidth(e.ghostText, remaining, "")
					ghostStyled := theme.CurrentPalette().Dim.Render(ghostDisplay)
					// Insert ghost after cursor marker
					markerIdx := strings.Index(body, core.CURSOR_MARKER)
					if markerIdx >= 0 {
						markerEnd := markerIdx + len(core.CURSOR_MARKER)
						body = body[:markerEnd] + ghostStyled + body[markerEnd:]
					}
				}
			}
		}
		line := pad + promptFn(prompt) + body
		out = append(out, core.PadToWidth(line, width))
	}
	// Growth policy.
	for int64(len(out)) < e.minRows {
		useFirstPrompt := len(out) == 0
		emptyPrompt := firstPrompt
		emptyInner := innerWidth
		if !useFirstPrompt {
			emptyPrompt = contPrompt
			emptyInner = contInner
		}
		emptyPad := pad
		body := core.PadToWidth("", emptyInner)
		line := emptyPad + promptFn(emptyPrompt) + body
		out = append(out, core.PadToWidth(line, width))
		rowsMeta = append(rowsMeta, editorVisualRow{hardRow: -1, useFirstPrompt: useFirstPrompt})
	}
	if int64(len(out)) > e.maxRows {
		// Keep the segment containing the cursor visible; drop leading rows.
		if cursorV >= e.maxRows {
			drop := cursorV - e.maxRows + 1
			out = out[drop:]
			rowsMeta = rowsMeta[drop:]
		} else {
			out = out[:e.maxRows]
			rowsMeta = rowsMeta[:e.maxRows]
		}
	}

	if e.focused && e.focusIndicator != "" {
		if len(out) > 0 {
			last := len(out) - 1
			out[last] = e.focusIndicator + out[last]
		}
	}

	e.lastVisuals = rowsMeta
	return out
}

// selRangeInSegment computes the selected rune range [from, to) within a
// single visual segment of hardRow — which spans rune offsets
// [segStart, segStart+segLen) of that hard line — given a normalized
// mouse-drag selection. ok is false when hardRow falls outside the
// selection entirely; from/to may still fall outside [0, segLen) when the
// selection doesn't reach this particular segment, so callers must check
// from < to before using the range (guaranteeing 0 <= from < to <= segLen).
func selRangeInSegment(hardRow, segStart, segLen int64, selStart, selEnd editorSelPos) (from, to int64, ok bool) {
	if hardRow < selStart.row || hardRow > selEnd.row {
		return 0, 0, false
	}
	rowFrom, rowTo := int64(0), segLen+segStart
	if hardRow == selStart.row {
		rowFrom = selStart.col
	}
	if hardRow == selEnd.row {
		rowTo = selEnd.col
	}
	from = rowFrom - segStart
	to = rowTo - segStart
	if from < 0 {
		from = 0
	}
	if to > segLen {
		to = segLen
	}
	return from, to, true
}

// Invalidate is a no-op (no cache).
func (e *Editor) Invalidate() {}

func (e *Editor) Update(msg core.Msg) core.Cmd {
	switch m := msg.(type) {
	case core.KeyMsg:
		if looksLikePastedText(m.Data) {
			e.insertPastedText(m.Data)
		} else {
			e.processKeys(m.Data)
		}
	case core.PasteMsg:
		e.insertPastedText(m.Text)
	case core.WindowSizeMsg:
		e.Invalidate()
	case core.MouseMsg:
		e.handleMouse(m)
	}
	return nil
}

func (e *Editor) applyTextPaste(s string) string {
	e.mu.RLock()
	fn := e.onTextPaste
	e.mu.RUnlock()
	if fn == nil {
		return s
	}
	replacement, consume := fn(s)
	if !consume {
		return s
	}
	return replacement
}

// looksLikePastedText reports whether a KeyMsg payload is a batched paste
// (multiple key events including a newline) rather than a single Enter.
func looksLikePastedText(data string) bool {
	if !strings.ContainsAny(data, "\n\r") {
		return false
	}
	return len(terminal.ParseKeys(data)) > 1
}

func normalizePasteNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// insertPastedText inserts s at the cursor as buffer text. Newlines become
// hard lines; Enter is never treated as submit.
func (e *Editor) insertPastedText(s string) {
	s = normalizePasteNewlines(s)
	if s == "" {
		return
	}
	s = e.applyTextPaste(s)
	if s == "" {
		return
	}
	e.mu.Lock()
	e.ghostText = ""
	e.clearMouseSelectionLocked()
	e.pushSnapshotLocked()
	e.insertStringLocked(s)
	e.lastKill = false
	fn := e.onChange
	v := e.valueLocked()
	e.mu.Unlock()
	if fn != nil {
		fn(v)
	}
}

func (e *Editor) processKeys(data string) {
	keys := terminal.ParseKeys(data)
	if len(keys) == 0 {
		return
	}
	km := e.km
	single := len(keys) == 1
	for _, k := range keys {
		raw := k.Raw
		switch {
		case km.Matches(raw, "tui.input.newLine"):
			e.ghostText = ""
			e.burst.reset()
			e.insertRune('\n')
		case km.Matches(raw, "tui.input.submit"):
			e.ghostText = ""
			if single && e.burst.consumeEnterAsNewline() {
				e.insertRune('\n')
				continue
			}
			e.submit()
		case e.handleVimKey(k):
			e.ghostText = ""
		case km.Matches(raw, "tui.editor.selectAll"):
			e.SelectAll()
		case km.Matches(raw, "tui.editor.cursorLeft"):
			e.ghostText = ""
			e.moveCursor(0, -1)
		case km.Matches(raw, "tui.editor.cursorRight"):
			e.ghostText = ""
			e.moveCursor(0, 1)
		case km.Matches(raw, "tui.editor.cursorUp"):
			e.ghostText = ""
			if e.focused && e.row == 0 && e.historyPrev() {
			} else {
				e.moveCursor(-1, 0)
			}
		case km.Matches(raw, "tui.editor.cursorDown"):
			e.ghostText = ""
			if e.focused && e.row >= int64(len(e.lines)-1) && e.historyNext() {
			} else {
				e.moveCursor(1, 0)
			}
		case km.Matches(raw, "tui.editor.cursorWordLeft"):
			e.ghostText = ""
			e.moveWord(-1)
		case km.Matches(raw, "tui.editor.cursorWordRight"):
			e.ghostText = ""
			e.moveWord(1)
		case km.Matches(raw, "tui.editor.cursorLineStart"):
			e.mu.Lock()
			e.ghostText = ""
			e.allSelected = false
			e.clearMouseSelectionLocked()
			e.col = 0
			e.mu.Unlock()
		case km.Matches(raw, "tui.editor.cursorLineEnd"):
			e.mu.Lock()
			e.ghostText = ""
			e.allSelected = false
			e.clearMouseSelectionLocked()
			e.col = int64(len(e.lines[e.row]))
			e.mu.Unlock()
		case km.Matches(raw, "tui.editor.deleteCharBackward"):
			e.ghostText = ""
			e.deleteBackward()
		case km.Matches(raw, "tui.editor.deleteCharForward"):
			e.ghostText = ""
			e.deleteForward()
		case km.Matches(raw, "tui.editor.deleteWordBackward"):
			e.ghostText = ""
			e.deleteWordBackward()
		case km.Matches(raw, "tui.editor.deleteWordForward"):
			e.ghostText = ""
			e.deleteWordForward()
		case km.Matches(raw, "tui.editor.deleteToLineStart"):
			e.ghostText = ""
			e.deleteToLineStart()
		case km.Matches(raw, "tui.editor.deleteToLineEnd"):
			e.ghostText = ""
			e.deleteToLineEnd()
		case km.Matches(raw, "tui.editor.yank"):
			e.ghostText = ""
			e.yank()
		case km.Matches(raw, "tui.editor.yankPop"):
			e.ghostText = ""
			e.yankPop()
		case km.Matches(raw, "tui.editor.undo"):
			e.ghostText = ""
			e.undo()
		case km.Matches(raw, "ctrl+shift+z"), km.Matches(raw, "ctrl+y"):
			e.ghostText = ""
			e.redo()
		default:
			if k.Rune == '\n' || k.Rune == '\r' {
				continue
			}
			if k.IsPrintable() {
				e.ghostText = ""
				if single {
					e.burst.notePlain()
				} else {
					e.burst.reset()
				}
				e.insertRune(k.Rune)
			}
		}
	}
}

func (e *Editor) handleVimKey(k terminal.Key) bool {
	e.mu.Lock()
	enabled := e.vimEnabled
	insert := e.vimInsert
	e.mu.Unlock()
	if !enabled {
		return false
	}
	if k.Name == "escape" {
		e.mu.Lock()
		e.vimInsert = false
		e.vimPending = ""
		e.mu.Unlock()
		return true
	}
	if insert {
		return false
	}
	if k.Mods&^terminal.ModShift != 0 {
		return true
	}
	name := k.Name
	if name == "" && k.Rune != 0 {
		name = string(k.Rune)
	}

	e.mu.Lock()
	pending := e.vimPending
	e.vimPending = ""
	e.mu.Unlock()

	if pending == "d" {
		switch name {
		case "d":
			e.vimDeleteLine()
		case "w":
			e.deleteWordForward()
		case "$":
			e.deleteToLineEnd()
		case "0":
			e.deleteToLineStart()
		}
		return true
	}

	switch name {
	case "h":
		e.moveCursor(0, -1)
	case "l":
		e.moveCursor(0, 1)
	case "j":
		e.moveCursor(1, 0)
	case "k":
		e.moveCursor(-1, 0)
	case "w":
		e.moveWord(1)
	case "b":
		e.moveWord(-1)
	case "0":
		e.mu.Lock()
		e.allSelected = false
		e.clearMouseSelectionLocked()
		e.col = 0
		e.mu.Unlock()
	case "$":
		e.mu.Lock()
		e.allSelected = false
		e.clearMouseSelectionLocked()
		e.col = int64(len(e.lines[e.row]))
		e.mu.Unlock()
	case "i":
		e.mu.Lock()
		e.vimInsert = true
		e.mu.Unlock()
	case "a":
		e.moveCursor(0, 1)
		e.mu.Lock()
		e.vimInsert = true
		e.mu.Unlock()
	case "I":
		e.mu.Lock()
		e.allSelected = false
		e.clearMouseSelectionLocked()
		e.col = 0
		e.vimInsert = true
		e.mu.Unlock()
	case "A":
		e.mu.Lock()
		e.allSelected = false
		e.clearMouseSelectionLocked()
		e.col = int64(len(e.lines[e.row]))
		e.vimInsert = true
		e.mu.Unlock()
	case "x":
		e.deleteForward()
	case "d":
		e.mu.Lock()
		e.vimPending = "d"
		e.mu.Unlock()
	case "p":
		e.yank()
	case "u":
		e.undo()
	case "G":
		e.mu.Lock()
		e.allSelected = false
		e.clearMouseSelectionLocked()
		e.row = int64(len(e.lines) - 1)
		e.col = int64(len(e.lines[e.row]))
		e.mu.Unlock()
	case "g":
		e.mu.Lock()
		if pending == "g" {
			e.allSelected = false
			e.clearMouseSelectionLocked()
			e.row = 0
			e.col = 0
		} else {
			e.vimPending = "g"
		}
		e.mu.Unlock()
	}
	return true
}

func (e *Editor) vimDeleteLine() {
	e.mu.Lock()
	e.clearMouseSelectionLocked()
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
	killed := string(e.lines[e.row])
	if len(e.lines) == 1 {
		e.lines = [][]rune{{}}
		e.row = 0
		e.col = 0
	} else {
		e.lines = append(e.lines[:e.row], e.lines[e.row+1:]...)
		if e.row >= int64(len(e.lines)) {
			e.row = int64(len(e.lines) - 1)
		}
		e.col = 0
	}
	e.pushKillRingLocked(killed + "\n")
	fn := e.onChange
	v := e.valueLocked()
	e.mu.Unlock()
	if fn != nil {
		fn(v)
	}
}

// ---------------------------------------------------------------------------
// Editing
// ---------------------------------------------------------------------------

func (e *Editor) insertRune(r rune) {
	e.mu.Lock()
	e.clearMouseSelectionLocked()
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
	e.burst.reset()
	e.allSelected = false
	e.clearMouseSelectionLocked()
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
	e.clearMouseSelectionLocked()
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
	e.burst.reset()
	e.mu.Lock()
	e.clearMouseSelectionLocked()
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
	e.burst.reset()
	e.mu.Lock()
	e.clearMouseSelectionLocked()
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
	e.clearMouseSelectionLocked()
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
	e.clearMouseSelectionLocked()
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
	e.clearMouseSelectionLocked()
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
	e.clearMouseSelectionLocked()
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
	e.clearMouseSelectionLocked()
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
	e.clearMouseSelectionLocked()
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
	s = e.encodeChipsLocked(s)
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
		chips: cloneChipMap(e.chips),
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
	e.clearMouseSelectionLocked()
	if len(e.history) == 0 {
		e.mu.Unlock()
		return
	}
	current := editorSnapshot{
		lines: cloneRuneLines(e.lines),
		chips: cloneChipMap(e.chips),
		row:   e.row,
		col:   e.col,
	}
	snap := e.history[len(e.history)-1]
	e.history = e.history[:len(e.history)-1]
	e.future = append(e.future, current)
	e.lines = snap.lines
	e.chips = cloneChipMap(snap.chips)
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
	e.clearMouseSelectionLocked()
	if len(e.future) == 0 {
		e.mu.Unlock()
		return
	}
	current := editorSnapshot{
		lines: cloneRuneLines(e.lines),
		chips: cloneChipMap(e.chips),
		row:   e.row,
		col:   e.col,
	}
	snap := e.future[len(e.future)-1]
	e.future = e.future[:len(e.future)-1]
	e.history = append(e.history, current)
	e.lines = snap.lines
	e.chips = cloneChipMap(snap.chips)
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
	s = e.encodeChipsLocked(s)
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
