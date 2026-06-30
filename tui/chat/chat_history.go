package chat

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covonaut/tui/component"
	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/theme"
)

// ---------------------------------------------------------------------------
// ChatHistory — a viewport-clipped list of chat messages.
//
// Each entry is a ChatMessage with a role, optional ID (for streaming
// updates), a body rendered as Markdown, and metadata. Append/Update are
// thread-safe and invalidate the render cache.
//
// Viewport behaviour:
//   - The caller sets MaxRows (typically recomputed on resize by ChatLayout).
//   - FollowTail=true (default) auto-scrolls to the bottom on every Append.
//   - The user can scroll manually with PgUp/PgDn/↑↓ when focused.
// ---------------------------------------------------------------------------

// ChatRole tags a message's origin.
type ChatRole int64

const (
	RoleUser ChatRole = iota + 1
	RoleAssistant
	RoleSystem
	RoleTool
	RoleError
	RoleDivider
)

// ChatMessage is one item in the chat transcript.
type ChatMessage struct {
	ID       string        // optional — non-empty enables in-place updates.
	Role     ChatRole      // governs default styling & prefix.
	Text     string        // raw source (markdown for assistant, plain for others).
	Pending  bool          // the message is still streaming; a cursor may be shown.
	Meta     string        // e.g. tool name, duration.
	At       time.Time     // emission time (for display).
	Duration time.Duration // optional — displayed after Meta.
	Collapsed bool         // when true, tool output shows summary; click to expand.

	// Thinking blocks (structured content).
	ThinkingSegments []ThinkingSegment

	// Internal: last delta dedup to suppress streaming repetition loops.
	lastDelta string
}

// ThinkingSegment holds a chunk of thinking text.
type ThinkingSegment struct {
	Text      string
	Collapsed bool
}

// ChatHistoryTheme customizes prefix / styling for each role.
type ChatHistoryTheme struct {
	UserPrefix      string
	UserStyle       theme.Style
	AssistantPrefix string
	AssistantStyle  theme.Style
	SystemPrefix    string
	SystemStyle     theme.Style
	ToolPrefix      string
	ToolStyle       theme.Style
	ToolBorder      theme.Style
	SuccessStyle    theme.Style
	ErrorPrefix     string
	ErrorStyle      theme.Style
	DividerChar     string
	DimStyle        theme.Style
	ThinkingStyle   theme.Style
	SelectedBg      string // ANSI background for selection
	MarkdownTheme   component.MarkdownTheme
}

// DefaultChatHistoryTheme returns a theme built from the current palette.
func DefaultChatHistoryTheme() ChatHistoryTheme {
	pal := theme.CurrentPalette()
	return ChatHistoryTheme{
		UserPrefix:      "> ",
		UserStyle:       pal.User,
		AssistantPrefix: "",
		AssistantStyle:  pal.Assistant,
		SystemPrefix:    "",
		SystemStyle:     pal.System,
		ToolPrefix:      theme.SymbolArrow + " ",
		ToolStyle:       pal.Dim,
		ToolBorder:      pal.BorderMuted,
		SuccessStyle:    pal.Success,
		ErrorPrefix:     theme.SymbolCross + " ",
		ErrorStyle:      pal.Error,
		DividerChar:     "─",
		DimStyle:        pal.Dim,
		ThinkingStyle:   pal.Thinking,
		SelectedBg:      "\x1b[48;5;33m",
		MarkdownTheme:   component.DefaultMarkdownTheme(),
	}
}

// ChatHistory is a Component that renders ChatMessages inside a scrollable
// viewport.
type msgRange struct {
	startLine int
	endLine   int
	msgIndex  int
	toolGroup bool // true if this is a collapsed tool group
	groupFrom int  // first message index in the group
	groupTo   int  // last message index in the group
}

type selectionPos struct {
	line int64 // absolute line index in cachedAll
	col  int64 // visible column within the line
}

type ChatHistory struct {
	mu       sync.Mutex
	messages []ChatMessage
	theme    ChatHistoryTheme
	maxRows  int64
	offset   int64 // lines scrolled up from the bottom (0 = tail)
	follow   bool

	reasoningRenderer ReasoningRenderer

	// render cache keyed on width + invalidation counter.
	cachedWidth    int64
	cachedAll      []string
	cachedMsgRanges []msgRange
	dirty          bool
	expandedGroups map[int]bool // group message indices that are expanded

	// optional invalidate callback (usually TUI.RequestRender).
	onInvalidate func()

	// callback invoked when a message is copied
	onCopy func(text string)

	// text selection state — stored as absolute line indices in cachedAll
	selActive   bool
	selStart    selectionPos
	selEnd      selectionPos
	selDragging bool

	// When a left-click follows a wheel event within a short window, the entire
	// gesture (press-motion-release) is suppressed. This prevents accidental text
	// selection when the user switches from two-finger scroll to single-finger
	// slide on a trackpad.
	lastWheelAt    time.Time
	suppressGesture bool
}

// NewChatHistory returns an empty history using the default theme.
// Reasoning display defaults to hidden; set a ReasoningRenderer via
// SetReasoningRenderer to enable it.
func NewChatHistory() *ChatHistory {
	return &ChatHistory{
		theme:            DefaultChatHistoryTheme(),
		follow:           true,
		dirty:            true,
		expandedGroups:   make(map[int]bool),
		reasoningRenderer: HiddenReasoningRenderer{},
	}
}

// SetTheme overrides the styling theme.
func (h *ChatHistory) SetTheme(t ChatHistoryTheme) {
	h.mu.Lock()
	h.theme = t
	h.dirty = true
	h.mu.Unlock()
	h.invalidate()
}

// SetReasoningRenderer installs the renderer used to display thinking
// segments. Pass nil to hide reasoning entirely (HiddenReasoningRenderer).
// Passing a *DefaultReasoningRenderer restores the legacy Show/Mode policy.
func (h *ChatHistory) SetReasoningRenderer(r ReasoningRenderer) {
	h.mu.Lock()
	if r == nil {
		r = HiddenReasoningRenderer{}
	}
	h.reasoningRenderer = r
	h.dirty = true
	h.mu.Unlock()
	h.invalidate()
}

// SetOnInvalidate wires a callback invoked on any mutation (typically
// TUI.RequestRender).
func (h *ChatHistory) SetOnInvalidate(fn func()) {
	h.mu.Lock()
	h.onInvalidate = fn
	h.mu.Unlock()
}

func (h *ChatHistory) SetOnCopy(fn func(text string)) {
	h.mu.Lock()
	h.onCopy = fn
	h.mu.Unlock()
}

// SetMaxRows clamps the visible viewport.
func (h *ChatHistory) SetMaxRows(n int64) {
	h.mu.Lock()
	if h.maxRows == n {
		h.mu.Unlock()
		return
	}
	h.maxRows = n
	h.mu.Unlock()
	h.invalidate()
}

// SetMaxRowsDirect sets the viewport height without triggering invalidation.
// Use this from within Render to avoid re-entrant RequestRender calls.
func (h *ChatHistory) SetMaxRowsDirect(n int64) {
	h.mu.Lock()
	h.maxRows = n
	h.mu.Unlock()
}

// CollapseConsecutiveTools collapses consecutive tool/system messages
// at the end when there are 2+ of them (called on turn end).
func (h *ChatHistory) CollapseConsecutiveTools() {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Find the last run of consecutive tool/system messages.
	runStart := -1
	for i := len(h.messages) - 1; i >= 0; i-- {
		r := h.messages[i].Role
		if r == RoleTool || r == RoleSystem {
			runStart = i
		} else {
			break
		}
	}
	if runStart < 0 {
		return
	}
	// Check that we have at least 2
	if len(h.messages)-runStart < 2 {
		return
	}
	// Ensure expandedGroups map exists and mark this group as collapsed
	// by removing the key (default is false/collapsed).
	if h.expandedGroups == nil {
		h.expandedGroups = make(map[int]bool)
	}
	delete(h.expandedGroups, runStart)
	h.dirty = true
}

// Append adds a new message, auto-scrolling to the bottom when follow-tail
// is enabled. If m.ID is empty, a new unique ID is generated and returned.
func (h *ChatHistory) Append(m ChatMessage) string {
	h.mu.Lock()
	if m.ID == "" {
		m.ID = fmt.Sprintf("msg-%d", time.Now().UnixNano())
	}
	if m.At.IsZero() {
		m.At = time.Now()
	}
	h.messages = append(h.messages, m)
	h.dirty = true
	// Clear selection since content changed invalidates absolute indices
	h.selActive = false
	h.selDragging = false
	if h.follow {
		h.offset = 0
	}
	id := m.ID
	h.mu.Unlock()
	h.invalidate()
	return id
}

// PatchMessage patches the text / pending / meta fields of a message identified
// by id. No-op if id is unknown.
func (h *ChatHistory) PatchMessage(id string, fn func(m *ChatMessage)) bool {
	if fn == nil {
		return false
	}
	h.mu.Lock()
	for i := range h.messages {
		if h.messages[i].ID == id {
			fn(&h.messages[i])
			h.dirty = true
			h.selActive = false
			h.selDragging = false
			h.mu.Unlock()
			h.invalidate()
			return true
		}
	}
	h.mu.Unlock()
	return false
}

// AppendDelta appends text to an existing assistant message, or creates a
// new one if `id` is empty or unknown. Returns the effective message ID.
func (h *ChatHistory) AppendDelta(id, delta string) string {
	return h.AppendDeltaWithKind(id, delta, "")
}

// AppendDeltaWithKind appends text to an existing assistant message, routing
// to thinking or text segments based on `kind` ("thinking" or "text"/"").
func (h *ChatHistory) AppendDeltaWithKind(id, delta, kind string) string {
	if delta == "" {
		return id
	}

	h.mu.Lock()

	// Suppress consecutive identical deltas (LLM streaming repetition loop).
	for i := range h.messages {
		if h.messages[i].ID == id && h.messages[i].lastDelta == delta {
			h.mu.Unlock()
			return id
		}
	}

	if id != "" {
		for i := range h.messages {
			if h.messages[i].ID == id {
				h.messages[i].lastDelta = delta
				if kind == "thinking" {
					if len(h.messages[i].ThinkingSegments) == 0 {
						h.messages[i].ThinkingSegments = append(h.messages[i].ThinkingSegments, ThinkingSegment{})
					}
					last := &h.messages[i].ThinkingSegments[len(h.messages[i].ThinkingSegments)-1]
					last.Text += delta
				} else {
					h.messages[i].Text += delta
				}
				h.messages[i].Pending = true
				h.dirty = true
				h.selActive = false
				h.selDragging = false
				if h.follow {
					h.offset = 0
				}
				h.mu.Unlock()
				h.invalidate()
				return id
			}
		}
	}
	newID := fmt.Sprintf("msg-%d", time.Now().UnixNano())
	msg := ChatMessage{
		ID:      newID,
		Role:    RoleAssistant,
		Pending: true,
		At:      time.Now(),
	}
	if kind == "thinking" {
		msg.ThinkingSegments = []ThinkingSegment{{Text: delta}}
	} else {
		msg.Text = delta
	}
	h.messages = append(h.messages, msg)
	h.dirty = true
	h.selActive = false
	h.selDragging = false
	if h.follow {
		h.offset = 0
	}
	h.mu.Unlock()
	h.invalidate()
	return newID
}

// Finalize clears the Pending flag on the given id.
func (h *ChatHistory) Finalize(id string) {
	h.PatchMessage(id, func(m *ChatMessage) { m.Pending = false })
}

// Clear empties the transcript.
func (h *ChatHistory) Clear() {
	h.mu.Lock()
	h.messages = nil
	h.offset = 0
	h.dirty = true
	h.selActive = false
	h.selDragging = false
	h.mu.Unlock()
	h.invalidate()
}

// Messages returns a snapshot of the transcript.
func (h *ChatHistory) Messages() []ChatMessage {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]ChatMessage, len(h.messages))
	copy(out, h.messages)
	return out
}

// ScrollBy moves the viewport by n lines (positive = up, negative = down).
// Stops auto-following the tail when user scrolls up.
func (h *ChatHistory) ScrollBy(n int64) {
	h.mu.Lock()
	h.scrollByLocked(n)
	h.mu.Unlock()
	h.invalidate()
}

func (h *ChatHistory) scrollByLocked(n int64) {
	total := int64(len(h.cachedAll))
	h.offset += n
	if h.offset < 0 {
		h.offset = 0
		h.follow = true
	} else {
		h.follow = false
	}
	if h.maxRows > 0 && h.offset > total-h.maxRows {
		if total >= h.maxRows {
			h.offset = total - h.maxRows
		} else {
			h.offset = 0
		}
	}
}

// FollowTail resets the viewport to the bottom.
func (h *ChatHistory) FollowTail() {
	h.mu.Lock()
	h.offset = 0
	h.follow = true
	h.mu.Unlock()
	h.invalidate()
}

// Render draws the transcript, clipping to MaxRows when set.
func (h *ChatHistory) Render(width int64) []string {
	if width < 1 {
		width = 1
	}
	h.mu.Lock()
	wasDirty := h.dirty
	if h.dirty || h.cachedWidth != width {
		h.cachedAll = h.renderAll(width)
		h.cachedWidth = width
		h.dirty = false
		// If content changed (was dirty), reset scroll if following tail.
		if wasDirty && h.follow {
			h.offset = 0
		}
		// Also clamp if old offset is now past the content end.
		if len(h.cachedAll) > 0 && h.offset > 0 {
			maxLines := int64(len(h.cachedAll))
			if maxLines > h.maxRows && h.maxRows > 0 && h.offset > maxLines-h.maxRows {
				h.offset = maxLines - h.maxRows
				if h.offset < 0 {
					h.offset = 0
				}
			} else if h.maxRows <= 0 || maxLines <= h.maxRows {
				h.offset = 0
			}
		}
	}
	all := h.cachedAll
	maxRows := h.maxRows
	offset := h.offset
	follow := h.follow
	h.mu.Unlock()

	if maxRows <= 0 || int64(len(all)) <= maxRows {
		return all
	}
	end := int64(len(all)) - offset
	if end > int64(len(all)) {
		end = int64(len(all))
	}
	start := end - maxRows
	if start < 0 {
		start = 0
		end = maxRows
	}
	visible := all[start:end]

	// Add scroll indicator when not auto-following
	if !follow && end < int64(len(all)) {
		indicator := h.theme.DimStyle.Render(fmt.Sprintf("^ %d more lines — End to follow", int64(len(all))-end))
		// Drop last visible line to keep within maxRows, prevent pushing
		// status bar off-screen.
		if int64(len(visible)) >= maxRows && len(visible) > 0 {
			visible = visible[:len(visible)-1]
		}
		visible = append([]string{indicator}, visible...)
	}

	// Pad every line to full width so the TUI diff engine's \x1b[2K
	// never leaves a partial column that could bleed into the next line.
	for i, ln := range visible {
		if core.VisibleWidth(ln) < width {
			visible[i] = core.PadToWidth(ln, width)
		}
	}

	return visible
}

// Invalidate drops the render cache.
func (h *ChatHistory) Invalidate() {
	h.mu.Lock()
	h.dirty = true
	h.mu.Unlock()
	h.invalidate()
}

type chatAppendMsg struct {
	Message ChatMessage
}

func (chatAppendMsg) MsgMarker() {}

type chatUpdateMsg struct {
	ID string
	Fn func(m *ChatMessage)
}

func (chatUpdateMsg) MsgMarker() {}

type chatDeltaMsg struct {
	ID    string
	Delta string
}

func (chatDeltaMsg) MsgMarker() {}

type chatFinalizeMsg struct {
	ID string
}

func (chatFinalizeMsg) MsgMarker() {}

type chatClearMsg struct{}

func (chatClearMsg) MsgMarker() {}

type chatScrollMsg struct {
	Lines int64
}

func (chatScrollMsg) MsgMarker() {}

// Update implements Updatable. It processes Msg values from the TUI event
// loop, enabling message-driven mutation of the chat history.
func (h *ChatHistory) Update(msg core.Msg) core.Cmd {
	switch m := msg.(type) {
	case chatAppendMsg:
		h.Append(m.Message)
	case chatUpdateMsg:
		h.PatchMessage(m.ID, m.Fn)
	case chatDeltaMsg:
		h.AppendDelta(m.ID, m.Delta)
	case chatFinalizeMsg:
		h.Finalize(m.ID)
	case chatClearMsg:
		h.Clear()
	case chatScrollMsg:
		h.ScrollBy(m.Lines)
	case core.WindowSizeMsg:
		h.Invalidate()
	case core.MouseMsg:
		h.handleMouse(m)
	}
	return nil
}

func (h *ChatHistory) handleMouse(m core.MouseMsg) {
	h.mu.Lock()

	needInvalidate := false

	switch m.Action {
	case core.MouseWheelUp:
		h.lastWheelAt = time.Now()
		h.scrollByLocked(3)
		needInvalidate = true
	case core.MouseWheelDown:
		h.lastWheelAt = time.Now()
		h.scrollByLocked(-3)
		needInvalidate = true
	case core.MousePress:
		// Left button press: check if clicking on thinking header to toggle collapse
		absLine := h.viewportRowToAbsoluteLocked(m.Row)
		if absLine < 0 {
			h.mu.Unlock()
			return
		}
		// If this press follows a wheel event within 400ms, it is part of a
		// scroll gesture (e.g. a trackpad two-finger slide that emits stray
		// press/release events). Suppress the whole gesture BEFORE any toggle
		// or selection so that scrolling never expands/collapses diffs or tool
		// groups and never starts an accidental selection.
		if time.Since(h.lastWheelAt) < 400*time.Millisecond {
			h.suppressGesture = true
			h.mu.Unlock()
			return
		}
		if h.tryToggleThinkingAtLineLocked(absLine) {
			h.dirty = true
			needInvalidate = true
			h.mu.Unlock()
			h.invalidate()
			return
		}
		// Start selection — convert viewport row to absolute line index
		mappedCol := h.mapMouseColToVisibleColLocked(absLine, m.Col)
		h.selDragging = true
		h.selActive = true
		h.selStart = selectionPos{line: absLine, col: mappedCol}
		h.selEnd = selectionPos{line: absLine, col: mappedCol}
		// Don't trigger render yet — selection is empty and invisible until drag motion.
	case core.MouseMotion:
		if h.suppressGesture {
			break
		}
		if h.selDragging {
			absLine := h.viewportRowToAbsoluteLocked(m.Row)
			if absLine >= 0 {
				mappedCol := h.mapMouseColToVisibleColLocked(absLine, m.Col)
				h.selEnd = selectionPos{line: absLine, col: mappedCol}
				h.dirty = true // force re-render to update selection highlight
				needInvalidate = true
			}
		}
	case core.MouseRelease:
		if h.suppressGesture {
			h.suppressGesture = false
			break
		}
		if h.selDragging {
			h.selDragging = false
			// If selection is empty (no movement), clear it
			if h.isSelectionEmptyLocked() {
				h.selActive = false
			}
			h.dirty = true // force re-render for final selection state
			needInvalidate = true
		}
	}

	h.mu.Unlock()

	if needInvalidate {
		h.invalidate()
	}
}

// viewportRowToAbsoluteLocked converts a viewport-relative row to an absolute
// line index in cachedAll. Returns -1 if the row is out of range.
func (h *ChatHistory) viewportRowToAbsoluteLocked(viewportRow int64) int64 {
	total := int64(len(h.cachedAll))
	if total == 0 || h.maxRows <= 0 || viewportRow < 0 {
		return -1
	}
	end := total - h.offset
	start := end - h.maxRows
	if start < 0 {
		start = 0
	}
	// Clamp offset if it's now past the valid range (content shrank).
	if total <= h.maxRows {
		h.offset = 0
		start = 0
	} else if h.offset > total-h.maxRows {
		h.offset = total - h.maxRows
		start = h.offset
	}
	// When the scrollback is scrolled up (!follow, offset > 0), Render inserts
	// a "^ N more lines" indicator row at viewport position 0 and drops the
	// last visible line. The indicator is not selectable — skip past it so
	// mouse rows map to the content actually displayed beneath it. Without
	// this adjustment every selection is off by one (clicking the first
	// content line selects the second, etc.).
	if !h.follow && h.offset > 0 {
		viewportRow--
		if viewportRow < 0 {
			return -1 // click landed on the indicator row itself
		}
	}
	absIdx := start + viewportRow
	if absIdx >= total {
		return -1
	}
	return absIdx
}

// mapMouseColToVisibleColLocked normalizes mouse columns into a stable
// visible-column coordinate for the target line. Continuation cells (the
// right half of a wide rune) are mapped back to the wide rune's start.
func (h *ChatHistory) mapMouseColToVisibleColLocked(absLine, mouseCol int64) int64 {
	if absLine < 0 || absLine >= int64(len(h.cachedAll)) {
		if mouseCol < 0 {
			return 0
		}
		return mouseCol
	}
	line := h.cachedAll[absLine]
	row := core.ParseLine(line)
	lineWidth := row.VisibleWidth()
	if mouseCol < 0 {
		mouseCol = 0
	}
	if mouseCol > lineWidth {
		mouseCol = lineWidth
	}
	if row.IsRaw() || len(row.Cells) == 0 {
		return mouseCol
	}
	idx := int(mouseCol)
	if idx >= len(row.Cells) {
		idx = len(row.Cells) - 1
	}
	if idx >= 0 && row.Cells[idx].IsContinuation() {
		for idx > 0 && row.Cells[idx].IsContinuation() {
			idx--
		}
		return int64(idx)
	}
	return mouseCol
}

// tryToggleThinkingAtLineLocked checks if the clicked line is within a thinking
// segment and toggles its collapsed state. Returns true if toggled.
func (h *ChatHistory) expandToolGroup(msgRangeIdx int) {
	if h.expandedGroups == nil {
		h.expandedGroups = make(map[int]bool)
	}
	r := h.cachedMsgRanges[msgRangeIdx]
	if r.toolGroup {
		if h.expandedGroups[r.msgIndex] {
			delete(h.expandedGroups, r.msgIndex)
		} else {
			h.expandedGroups[r.msgIndex] = true
		}
	}
	h.dirty = true
}

func (h *ChatHistory) tryToggleThinkingAtLineLocked(absLine int64) bool {
	// Find which message contains this line
	msgIdx := -1
	for i, r := range h.cachedMsgRanges {
		if absLine >= int64(r.startLine) && absLine < int64(r.endLine) {
			msgIdx = i
			break
		}
	}
	if msgIdx < 0 || msgIdx >= len(h.cachedMsgRanges) {
		return false
	}

	// Check if it's a tool group
	if r := &h.cachedMsgRanges[msgIdx]; r.toolGroup {
		h.expandToolGroup(msgIdx)
		h.dirty = true
		return true
	}

	msg := &h.messages[msgIdx]
	if msg.Role == RoleTool && msg.Collapsed {
		msg.Collapsed = false
		return true
	}
	if msg.Role == RoleTool && !msg.Collapsed {
		msg.Collapsed = true
		return true
	}
	if msg.Role == RoleAssistant && msg.Collapsed {
		msg.Collapsed = false
		return true
	}
	if msg.Role == RoleAssistant && !msg.Collapsed && msg.Meta == "diff" {
		msg.Collapsed = true
		return true
	}
	if msg.Role != RoleAssistant || len(msg.ThinkingSegments) == 0 {
		return false
	}

	// Calculate line offset within the message
	msgStart := h.cachedMsgRanges[msgIdx].startLine
	lineOffset := absLine - int64(msgStart)

	// Track line positions to find which thinking segment contains this line
	currentLine := int64(0)
	for i := range msg.ThinkingSegments {
		seg := &msg.ThinkingSegments[i]
		if seg.Text == "" {
			continue
		}

		// Header line (e.g., "◐ thinking")
		if lineOffset == currentLine {
			seg.Collapsed = !seg.Collapsed
			return true
		}
		currentLine++

		if !seg.Collapsed {
			// Content lines
			contentLines := int64(len(core.WrapAnsi(seg.Text, h.cachedWidth)))
			if lineOffset > currentLine && lineOffset <= currentLine+contentLines {
				seg.Collapsed = !seg.Collapsed
				return true
			}
			currentLine += contentLines + 1 // +1 for separator
		} else {
			// Collapsed line (summary)
			if lineOffset == currentLine {
				seg.Collapsed = !seg.Collapsed
				return true
			}
			currentLine++
		}
	}

	return false
}

func (h *ChatHistory) invalidate() {
	h.mu.Lock()
	cb := h.onInvalidate
	h.mu.Unlock()
	if cb != nil {
		cb()
	}
}

func (h *ChatHistory) messageAtViewportRow(row int64) *ChatMessage {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.messageAtViewportRowLocked(row)
}

func (h *ChatHistory) messageAtViewportRowLocked(row int64) *ChatMessage {
	total := int64(len(h.cachedAll))
	if total == 0 || h.maxRows <= 0 || row < 0 {
		return nil
	}
	end := total - h.offset
	start := end - h.maxRows
	if start < 0 {
		start = 0
	}
	idx := start + row
	if idx >= total {
		return nil
	}
	for _, r := range h.cachedMsgRanges {
		if int64(r.startLine) <= idx && idx < int64(r.endLine) {
			if r.msgIndex < len(h.messages) {
				msg := h.messages[r.msgIndex]
				return &msg
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

func (h *ChatHistory) isSelectionEmptyLocked() bool {
	return h.selStart.line == h.selEnd.line && h.selStart.col == h.selEnd.col
}

// GetSelectedText returns the currently selected text, or empty string if no selection.
func (h *ChatHistory) GetSelectedText() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.getSelectedTextLocked()
}

// ClearSelection clears the current text selection.
func (h *ChatHistory) ClearSelection() {
	h.mu.Lock()
	h.selActive = false
	h.selDragging = false
	h.mu.Unlock()
	h.invalidate()
}

func (h *ChatHistory) getSelectedTextLocked() string {
	if !h.selActive || h.isSelectionEmptyLocked() {
		return ""
	}

	total := int64(len(h.cachedAll))
	if total == 0 {
		return ""
	}

	topLine := h.selStart.line
	botLine := h.selEnd.line
	topCol := h.selStart.col
	botCol := h.selEnd.col

	// Normalize so topLine <= botLine, and when equal topCol <= botCol
	if topLine > botLine || (topLine == botLine && topCol > botCol) {
		topLine, botLine = botLine, topLine
		topCol, botCol = botCol, topCol
	}

	if topLine < 0 || topLine >= total || botLine < 0 || botLine >= total {
		return ""
	}

	var result []string
	for i := topLine; i <= botLine && i < total; i++ {
		line := h.cachedAll[i]
		var part string
		if i == topLine && i == botLine {
			part = core.SliceByColumn(line, topCol, botCol)
		} else if i == topLine {
			part = core.SliceByColumn(line, topCol, core.VisibleWidth(line))
		} else if i == botLine {
			part = core.SliceByColumn(line, 0, botCol)
		} else {
			part = line
		}
		result = append(result, core.StripAnsi(part))
	}

	return strings.Join(result, "\n")
}

func (h *ChatHistory) renderAll(width int64) []string {
	theme := h.theme
	var out []string
	var ranges []msgRange

	if len(h.messages) == 0 {
		// Empty history — show welcome
		pal := h.theme.UserStyle
		welcome := pal.Render("▌ ") + h.theme.SystemStyle.Render("Ready — type a message")
		return []string{"", "", "", welcome, "", "", ""}
	}

	for i := 0; i < len(h.messages); i++ {
		m := h.messages[i]

		// Detect a run of consecutive tool/system messages starting at i.
		if m.Role == RoleTool || m.Role == RoleSystem {
			groupEnd := i
			for j := i + 1; j < len(h.messages); j++ {
				r := h.messages[j].Role
				if r == RoleTool || r == RoleSystem {
					groupEnd = j
				} else {
					break
				}
			}
			// Group 2+ consecutive tools UNLESS we're still mid-turn:
			// the group extends to the end AND the last non-tool msg is pending.
			midTurn := groupEnd == len(h.messages)-1
			if midTurn {
				// Check if the last non-tool message is still streaming.
				for j := i - 1; j >= 0; j-- {
					if h.messages[j].Role != RoleTool && h.messages[j].Role != RoleSystem {
						midTurn = h.messages[j].Pending
						break
					}
				}
			}
			if groupEnd > i && !midTurn {
				// Render collapsed or expanded group
				bar := theme.ToolBorder.Render("▌ ")
				toolCount, sysCount := 0, 0
				for j := i; j <= groupEnd; j++ {
					if h.messages[j].Role == RoleTool {
						toolCount++
					} else {
						sysCount++
					}
				}

				if h.expandedGroups[i] {
					start := len(out)
					summary := fmt.Sprintf("[-] %d tools · %d msgs", toolCount, sysCount)
					if sysCount == 0 {
						summary = fmt.Sprintf("[-] %d tools", toolCount)
					}
					out = append(out, core.PadToWidth(bar+theme.DimStyle.Render(summary), width))
					for j := i; j <= groupEnd; j++ {
						out = append(out, h.renderMessage(h.messages[j], theme, width)...)
					}
					ranges = append(ranges, msgRange{
						startLine: start, endLine: len(out), msgIndex: i,
						toolGroup: true, groupFrom: i, groupTo: groupEnd,
					})
				} else {
					start := len(out)
					summary := fmt.Sprintf("[+] %d tools · %d msgs", toolCount, sysCount)
					if sysCount == 0 {
						summary = fmt.Sprintf("[+] %d tools", toolCount)
					}
					out = append(out, bar+theme.DimStyle.Render(summary))
					ranges = append(ranges, msgRange{
						startLine: start, endLine: len(out), msgIndex: i,
						toolGroup: true, groupFrom: i, groupTo: groupEnd,
					})
				}
				i = groupEnd
				continue
			}
		}

		if i > 0 {
			// Add spacing. Use a subtle separator between user/assistant turns.
			prev := h.messages[i-1]
			if (prev.Role == RoleUser && m.Role == RoleAssistant) ||
				(prev.Role == RoleAssistant && m.Role == RoleUser) {
				sep := theme.DimStyle.Render(strings.Repeat("─", int(width)))
				out = append(out, "", sep, "")
			} else if prev.Role == RoleTool || m.Role == RoleTool {
				// Tools are compact — no extra blank line.
			} else {
				out = append(out, "", "")
			}
		}
		start := len(out)
		out = append(out, trimBlankEdges(h.renderMessage(m, theme, width))...)
		ranges = append(ranges, msgRange{startLine: start, endLine: len(out), msgIndex: i})
	}
	h.cachedMsgRanges = ranges

	// Apply selection highlight
	if h.selActive && !h.isSelectionEmptyLocked() {
		h.applySelectionHighlightLocked(out, width)
	}

	return out
}

func (h *ChatHistory) applySelectionHighlightLocked(lines []string, width int64) {
	total := int64(len(lines))
	if total == 0 || h.maxRows <= 0 {
		return
	}

	// Compute the visible viewport range in absolute indices
	end := total - h.offset
	start := end - h.maxRows
	if start < 0 {
		start = 0
	}

	topLine := h.selStart.line
	botLine := h.selEnd.line
	topCol := h.selStart.col
	botCol := h.selEnd.col

	// Normalize so topLine <= botLine, and when equal topCol <= botCol
	if topLine > botLine || (topLine == botLine && topCol > botCol) {
		topLine, botLine = botLine, topLine
		topCol, botCol = botCol, topCol
	}

	// Clamp selection to visible viewport
	if botLine < start || topLine >= end {
		return
	}
	if topLine < start {
		topLine = start
		topCol = 0
	}
	if botLine >= end {
		botLine = end - 1
		botCol = core.VisibleWidth(lines[botLine])
	}

	selBg := h.theme.SelectedBg
	if selBg == "" {
		selBg = "\x1b[48;5;33m"
	}
	selStyle := core.ParseLine(selBg + " " + "\x1b[0m")
	if selStyle.IsRaw() || len(selStyle.Cells) == 0 {
		return
	}
	selectedStyle := selStyle.Cells[0].Style
	if selectedStyle.Bg.IsDefault() {
		return
	}

	for i := topLine; i <= botLine && i < end; i++ {
		if i < 0 || i >= total {
			continue
		}
		line := lines[i]
		row := core.ParseLine(line)
		if row.IsRaw() {
			continue
		}
		lineWidth := row.VisibleWidth()
		if lineWidth <= 0 {
			continue
		}

		fromCol := int64(0)
		toCol := lineWidth
		switch {
		case i == topLine && i == botLine:
			fromCol = topCol
			toCol = botCol
		case i == topLine:
			fromCol = topCol
		case i == botLine:
			toCol = botCol
		}

		if fromCol < 0 {
			fromCol = 0
		}
		if toCol < 0 {
			toCol = 0
		}
		if fromCol > lineWidth {
			fromCol = lineWidth
		}
		if toCol > lineWidth {
			toCol = lineWidth
		}
		if toCol < fromCol {
			toCol = fromCol
		}

		if fromCol != toCol {
			start := int(fromCol)
			endCol := int(toCol)
			if start < 0 {
				start = 0
			}
			if endCol > len(row.Cells) {
				endCol = len(row.Cells)
			}
			for c := start; c < endCol; c++ {
				row.Cells[c].Style = selectedStyle
			}
		}

		highlighted := core.SerializeRow(row)
		// Keep each highlighted row width-stable across drag updates.
		lines[i] = core.PadToWidth(core.TruncateToWidth(highlighted, lineWidth, ""), lineWidth)
	}
}

// trimBlankEdges removes leading and trailing blank (whitespace-only) lines
// from a rendered message block. Streamed assistant text often carries stray
// leading/trailing newlines which the markdown renderer turns into padded
// blank lines, inflating the vertical gap between turns. Internal blank lines
// (e.g. inside code blocks) are preserved.
func trimBlankEdges(lines []string) []string {
	start, end := 0, len(lines)
	for start < end && strings.TrimSpace(core.StripAnsi(lines[start])) == "" {
		start++
	}
	for end > start && strings.TrimSpace(core.StripAnsi(lines[end-1])) == "" {
		end--
	}
	return lines[start:end]
}

func (h *ChatHistory) renderMessage(m ChatMessage, theme ChatHistoryTheme, width int64) []string {
	switch m.Role {
	case RoleUser:
		bar := theme.UserStyle.Render("▌ ")
		body := bar + theme.UserStyle.Render(m.Text)
		return core.WrapAnsi(body, width)
	case RoleAssistant:
		// Collapsed assistant messages (e.g. collapsed diffs)
		if m.Collapsed && m.Text != "" {
			// Show first line as summary + expand hint
			firstLine := m.Text
			if idx := strings.IndexByte(firstLine, '\n'); idx > 0 {
				firstLine = firstLine[:idx]
			}
			if len(firstLine) > 80 {
				firstLine = firstLine[:77] + "..."
			}
			head := theme.ToolBorder.Render("▌") + " " + theme.DimStyle.Render(firstLine)
			lines := core.WrapAnsi(head, width)
			lines = append(lines, theme.DimStyle.Render("  ▸ expand"))
			return lines
		}

		var allLines []string

		// Render thinking segments first — delegated to the injected
		// ReasoningRenderer. The default implementation honours the
		// legacy Show/Mode policy; custom renderers can draw reasoning
		// anywhere (sidebar, overlay, etc.).
		if h.reasoningRenderer != nil {
			if rendered := h.reasoningRenderer.RenderThinking(m, width); len(rendered) > 0 {
				allLines = append(allLines, rendered...)
			}
		}

		// Render text content
		if m.Text != "" {
			md := component.NewMarkdown(m.Text)
			md.SetTheme(theme.MarkdownTheme)
			lines := md.Render(width)
			if m.Pending {
				if len(lines) == 0 {
					lines = []string{theme.DimStyle.Render("…")}
				} else {
					last := lines[len(lines)-1]
					lines[len(lines)-1] = last + theme.UserStyle.Render("▊")
				}
			}
			allLines = append(allLines, lines...)
		} else if len(m.ThinkingSegments) > 0 && m.Pending {
			// Only thinking, no text yet, show cursor
			if len(allLines) == 0 {
				allLines = []string{theme.DimStyle.Render("…")}
			} else {
				last := allLines[len(allLines)-1]
				allLines[len(allLines)-1] = last + theme.ThinkingStyle.Render("▊")
			}
		}

		if len(allLines) == 0 {
			allLines = []string{theme.DimStyle.Render("…")}
		}
		return allLines
	case RoleSystem:
		bar := theme.ToolBorder.Render("▌ ")
		return core.WrapAnsi(bar+theme.SystemStyle.Render(m.Text), width)
	case RoleTool:
		meta := ""
		if m.Duration > 0 {
			meta = " " + theme.DimStyle.Render(fmt.Sprintf("(%s)", m.Duration.Round(time.Millisecond)))
		}
		// Color the left bar based on tool status
		barColor := theme.ToolBorder
		if strings.Contains(m.Text, "done") || strings.Contains(m.Text, "✓") {
			barColor = theme.SuccessStyle
		} else if strings.Contains(m.Text, "failed") || strings.Contains(m.Text, "✗") {
			barColor = theme.ErrorStyle
		}
		bar := barColor.Render("▌")
		if m.Collapsed {
			summary := m.Text
			if len(summary) > 120 {
				summary = summary[:117] + "..."
			}
			head := bar + " [+] " + theme.ToolStyle.Render(theme.ToolPrefix+m.Meta) + " " + theme.DimStyle.Render(summary)
			return core.WrapAnsi(head, width)
		}
		head := bar + " " + theme.ToolStyle.Render(theme.ToolPrefix+m.Meta) + " " + m.Text + meta
		lines := core.WrapAnsi(head, width)
		return lines
	case RoleError:
		bar := theme.ErrorStyle.Render("▌ ")
		return core.WrapAnsi(bar+theme.ErrorStyle.Render(m.Text), width)
	case RoleDivider:
		ch := theme.DividerChar
		if ch == "" {
			ch = "─"
		}
		return []string{theme.DimStyle.Render(strings.Repeat(ch, int(width)))}
	default:
		return core.WrapAnsi(m.Text, width)
	}
}
