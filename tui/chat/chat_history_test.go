package chat

import (
	"strings"
	"testing"

	"github.com/covoyage/covonaut/tui/core"
)

func TestChatHistoryAppendAndRender(t *testing.T) {
	h := NewChatHistory()
	h.Append(ChatMessage{Role: RoleUser, Text: "hello"})
	h.Append(ChatMessage{Role: RoleAssistant, Text: "world"})

	lines := h.Render(40)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "hello") {
		t.Fatalf("user message missing: %q", joined)
	}
	if !strings.Contains(joined, "world") {
		t.Fatalf("assistant message missing: %q", joined)
	}
}

func TestChatHistoryAppendDelta(t *testing.T) {
	h := NewChatHistory()
	id := h.AppendDelta("", "Hello, ")
	if id == "" {
		t.Fatalf("no id returned")
	}
	h.AppendDelta(id, "world!")
	msgs := h.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 msg, got %d", len(msgs))
	}
	if msgs[0].Text != "Hello, world!" {
		t.Fatalf("text=%q", msgs[0].Text)
	}
	if !msgs[0].Pending {
		t.Fatalf("expected pending")
	}
	h.Finalize(id)
	msgs = h.Messages()
	if msgs[0].Pending {
		t.Fatalf("finalize should clear pending")
	}
}

func TestChatHistoryViewportClipping(t *testing.T) {
	h := NewChatHistory()
	for i := 0; i < 20; i++ {
		h.Append(ChatMessage{Role: RoleSystem, Text: "line"})
	}
	h.SetMaxRows(5)
	lines := h.Render(20)
	if int64(len(lines)) != 5 {
		t.Fatalf("viewport should clip to 5 rows; got %d", len(lines))
	}
}

func TestChatHistoryScroll(t *testing.T) {
	h := NewChatHistory()
	for i := 0; i < 30; i++ {
		h.Append(ChatMessage{Role: RoleSystem, Text: "row"})
	}
	h.SetMaxRows(5)
	_ = h.Render(20)
	h.ScrollBy(10)
	if h.follow {
		t.Fatalf("scroll-up should stop following tail")
	}
	h.FollowTail()
	if !h.follow {
		t.Fatalf("FollowTail should re-enable following")
	}
}

func TestSelectionHighlightKeepsVisibleWidthStable(t *testing.T) {
	h := NewChatHistory()
	h.maxRows = 10
	h.selActive = true
	h.selStart = selectionPos{line: 0, col: 1}

	origLine := "\x1b[38;5;245m▌\x1b[0m assistant: hello world"
	origWidth := int64(core.VisibleWidth(origLine))

	for endCol := int64(1); endCol <= origWidth; endCol++ {
		h.selEnd = selectionPos{line: 0, col: endCol}
		lines := []string{origLine}
		h.applySelectionHighlightLocked(lines, 120)
		gotWidth := int64(core.VisibleWidth(lines[0]))
		if gotWidth != origWidth {
			t.Fatalf("visible width changed at endCol=%d: got=%d want=%d", endCol, gotWidth, origWidth)
		}
	}
}

func TestSelectionHighlightWidthStableOnCJKAndEmoji(t *testing.T) {
	h := NewChatHistory()
	h.maxRows = 10
	h.selActive = true
	h.selStart = selectionPos{line: 0, col: 0}

	line := "中🙂文 abc"
	origWidth := int64(core.VisibleWidth(line))

	for endCol := int64(0); endCol <= origWidth; endCol++ {
		h.selEnd = selectionPos{line: 0, col: endCol}
		lines := []string{line}
		h.applySelectionHighlightLocked(lines, 120)
		gotWidth := int64(core.VisibleWidth(lines[0]))
		if gotWidth != origWidth {
			t.Fatalf("cjk/emoji width changed at endCol=%d: got=%d want=%d", endCol, gotWidth, origWidth)
		}
	}
}

func TestSelectionHighlightWidthStableWhenBoundaryMovesBackAndForth(t *testing.T) {
	h := NewChatHistory()
	h.maxRows = 10
	h.selActive = true
	h.selStart = selectionPos{line: 0, col: 0}

	line := "\x1b[38;5;245m彩色\x1b[0m mixed 中🙂 text"
	origWidth := int64(core.VisibleWidth(line))

	sequence := []int64{0, 2, 5, 9, 6, 3, 8, 1, origWidth, 0, 4, 7, 2}
	for _, endCol := range sequence {
		h.selEnd = selectionPos{line: 0, col: endCol}
		lines := []string{line}
		h.applySelectionHighlightLocked(lines, 120)
		gotWidth := int64(core.VisibleWidth(lines[0]))
		if gotWidth != origWidth {
			t.Fatalf("boundary move changed width at endCol=%d: got=%d want=%d", endCol, gotWidth, origWidth)
		}
	}
}

func TestMapMouseColToVisibleColSnapsWideContinuation(t *testing.T) {
	h := NewChatHistory()
	h.cachedAll = []string{"中a"}

	if got := h.mapMouseColToVisibleColLocked(0, 1); got != 0 {
		t.Fatalf("continuation col should snap to wide rune start: got=%d want=0", got)
	}
}

func TestSelectionHighlightUsesUniformStyleOverStyledText(t *testing.T) {
	h := NewChatHistory()
	h.maxRows = 10
	h.selActive = true
	h.selStart = selectionPos{line: 0, col: 0}
	h.selEnd = selectionPos{line: 0, col: 5}

	line := "\x1b[31mAB\x1b[0m\x1b[32mCD\x1b[0mE"
	lines := []string{line}
	h.applySelectionHighlightLocked(lines, 80)

	row := core.ParseLine(lines[0])
	if row.IsRaw() {
		t.Fatalf("expected parsed row, got raw")
	}
	if len(row.Cells) < 5 {
		t.Fatalf("unexpected rendered cell count: %d", len(row.Cells))
	}
	base := row.Cells[0].Style
	for i := 1; i < 5; i++ {
		if !row.Cells[i].Style.Equal(base) {
			t.Fatalf("selected styles are not uniform at col=%d", i)
		}
	}
}

// TestViewportRowToAbsoluteWithScrollIndicator verifies that when the history
// is scrolled up (!follow, offset > 0), Render inserts a "^ N more lines"
// indicator at viewport row 0, and viewportRowToAbsoluteLocked correctly
// skips it so mouse selections map to the content actually displayed.
//
// Without the fix, every row is off by one: clicking the first visible
// content line selects the second, etc.
func TestViewportRowToAbsoluteWithScrollIndicator(t *testing.T) {
	h := NewChatHistory()
	for i := 0; i < 20; i++ {
		h.Append(ChatMessage{Role: RoleSystem, Text: "row"})
	}
	h.SetMaxRows(5)

	// Populate cachedAll.
	_ = h.Render(40)

	// Scroll up so the indicator row appears.
	h.ScrollBy(3)
	if h.follow || h.offset == 0 {
		t.Fatalf("precondition: expected !follow and offset>0; follow=%v offset=%d", h.follow, h.offset)
	}

	// Row 0 is the indicator row — not selectable.
	if got := h.viewportRowToAbsoluteLocked(0); got != -1 {
		t.Fatalf("indicator row (0) should be unselectable; got absLine=%d", got)
	}

	// Row 1 maps to the first visible content line. Compute expected via
	// the same formula Render uses (minus the indicator skip).
	total := int64(len(h.cachedAll))
	end := total - h.offset
	start := end - h.maxRows
	if start < 0 {
		start = 0
	}
	wantFirst := start
	if got := h.viewportRowToAbsoluteLocked(1); got != wantFirst {
		t.Fatalf("row 1 should map to first content line %d; got %d", wantFirst, got)
	}

	// Row 2 maps to the second visible content line.
	if got := h.viewportRowToAbsoluteLocked(2); got != wantFirst+1 {
		t.Fatalf("row 2 should map to content line %d; got %d", wantFirst+1, got)
	}
}

// TestViewportRowToAbsoluteNoIndicatorWhenFollowingTail verifies that when
// following the tail (offset == 0, no indicator row), the mapping is direct
// with no row-skip.
func TestViewportRowToAbsoluteNoIndicatorWhenFollowingTail(t *testing.T) {
	h := NewChatHistory()
	for i := 0; i < 20; i++ {
		h.Append(ChatMessage{Role: RoleSystem, Text: "row"})
	}
	h.SetMaxRows(5)
	_ = h.Render(40)

	// No scroll — following tail, no indicator row.
	if !h.follow || h.offset != 0 {
		t.Fatalf("precondition: expected follow=true offset=0; follow=%v offset=%d", h.follow, h.offset)
	}

	total := int64(len(h.cachedAll))
	end := total - h.offset
	start := end - h.maxRows
	if start < 0 {
		start = 0
	}

	// Row 0 maps directly to the first visible content line (no indicator).
	if got := h.viewportRowToAbsoluteLocked(0); got != start {
		t.Fatalf("row 0 should map to content line %d; got %d", start, got)
	}
}
