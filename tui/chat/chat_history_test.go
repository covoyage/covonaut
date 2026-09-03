package chat

import (
	"strings"
	"testing"

	"github.com/covoyage/covonaut/tui/core"
)

func rangeForMsg(h *ChatHistory, msgIndex int) (msgRange, bool) {
	for _, r := range h.cachedMsgRanges {
		if r.msgIndex == msgIndex && !r.toolGroup {
			return r, true
		}
	}
	for _, r := range h.cachedMsgRanges {
		if r.msgIndex == msgIndex {
			return r, true
		}
	}
	return msgRange{}, false
}

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

func TestChatHistoryEmptyAssistantPlaceholderGoneAfterFinalize(t *testing.T) {
	h := NewChatHistory()
	h.Append(ChatMessage{Role: RoleUser, Text: "青岛天气"})
	id := h.AppendDeltaWithKind("", "hidden-thought", "thinking")
	plain := core.StripAnsi(strings.Join(h.Render(40), "\n"))
	if !strings.Contains(plain, "…") && !strings.Contains(plain, "...") {
		t.Fatalf("pending empty assistant should show placeholder: %q", plain)
	}
	h.Finalize(id)
	h.Append(ChatMessage{Role: RoleTool, Meta: "skill", Text: "done"})
	h.Append(ChatMessage{Role: RoleTool, Meta: "web_fetch", Text: "done"})
	plain = core.StripAnsi(strings.Join(h.Render(40), "\n"))
	if strings.Contains(plain, "…") || strings.Contains(plain, "...") {
		t.Fatalf("placeholder should disappear after tools: %q", plain)
	}
	if !strings.Contains(plain, "skill") || !strings.Contains(plain, "web_fetch") {
		t.Fatalf("tools missing: %q", plain)
	}
}

func TestChatHistoryAssistantFooterChip(t *testing.T) {
	h := NewChatHistory()
	h.Append(ChatMessage{Role: RoleUser, Text: "hello"})
	id := h.Append(ChatMessage{Role: RoleAssistant, Text: "world", Pending: true, FooterChip: "code · gpt-4.1 · 5.5s"})

	plain := core.StripAnsi(strings.Join(h.Render(40), "\n"))
	if strings.Contains(plain, "▸") || strings.Contains(plain, "5.5s") {
		t.Fatalf("chip should stay hidden while pending: %q", plain)
	}

	h.Finalize(id)
	plain = core.StripAnsi(strings.Join(h.Render(40), "\n"))
	if !strings.Contains(plain, "world") {
		t.Fatalf("assistant text missing: %q", plain)
	}
	if !strings.Contains(plain, "▸ code · gpt-4.1 · 5.5s") {
		t.Fatalf("completion chip missing: %q", plain)
	}
}

func TestChatHistoryPatchLastAssistantReply(t *testing.T) {
	h := NewChatHistory()
	h.Append(ChatMessage{Role: RoleAssistant, Text: "first"})
	h.Append(ChatMessage{Role: RoleTool, Meta: "bash", Text: "done"})
	pendingID := h.Append(ChatMessage{Role: RoleAssistant, Text: "second", Pending: true})
	if !h.PatchLastAssistantReply(func(m *ChatMessage) {
		m.FooterChip = "code · gpt-4.1 · 1.2s"
	}) {
		t.Fatal("expected patch to succeed")
	}
	msgs := h.Messages()
	if msgs[len(msgs)-1].FooterChip != "code · gpt-4.1 · 1.2s" {
		t.Fatalf("patched wrong message: %+v", msgs)
	}
	plain := core.StripAnsi(strings.Join(h.Render(40), "\n"))
	if strings.Contains(plain, "▸") {
		t.Fatalf("chip should stay hidden while pending: %q", plain)
	}
	h.Finalize(pendingID)
	plain = core.StripAnsi(strings.Join(h.Render(40), "\n"))
	if !strings.Contains(plain, "▸ code · gpt-4.1 · 1.2s") {
		t.Fatalf("chip missing after finalize: %q", plain)
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

func TestChatHistoryPendingCursorDoesNotExceedWidth(t *testing.T) {
	h := NewChatHistory()
	h.Append(ChatMessage{
		Role:    RoleAssistant,
		Text:    strings.Repeat("x", 80),
		Pending: true,
	})
	const width int64 = 40
	lines := h.Render(width)
	if len(lines) == 0 {
		t.Fatal("expected rendered lines")
	}
	foundCursor := false
	for _, ln := range lines {
		if w := core.VisibleWidth(ln); w > width {
			t.Fatalf("pending line wider than viewport: width=%d line=%q", w, ln)
		}
		if strings.Contains(ln, "▊") {
			foundCursor = true
		}
	}
	if !foundCursor {
		t.Fatalf("pending cursor missing from render: %q", strings.Join(lines, "\n"))
	}
}

func TestAppendStreamingCursorFitsWidth(t *testing.T) {
	line := strings.Repeat("a", 40)
	got := appendStreamingCursor(line, "▊", 40)
	if core.VisibleWidth(got) > 40 {
		t.Fatalf("cursor overflow: width=%d got=%q", core.VisibleWidth(got), got)
	}
	if !strings.Contains(got, "▊") {
		t.Fatalf("cursor missing: %q", got)
	}
}

func TestChatHistoryShortTranscriptFitsWidth(t *testing.T) {
	h := NewChatHistory()
	h.Append(ChatMessage{Role: RoleUser, Text: "hi"})
	const width int64 = 24
	for _, ln := range h.Render(width) {
		if w := core.VisibleWidth(ln); w > width {
			t.Fatalf("short transcript line wider than viewport: width=%d line=%q", w, ln)
		}
	}
}

func TestChatHistoryUserMarkdown(t *testing.T) {
	h := NewChatHistory()
	h.Append(ChatMessage{Role: RoleUser, Text: "see `foo.ts`:\n\n- first\n- second"})
	plain := core.StripAnsi(strings.Join(h.Render(40), "\n"))
	if !strings.Contains(plain, "foo.ts") {
		t.Fatalf("inline code missing: %q", plain)
	}
	if strings.Contains(plain, "`foo.ts`") {
		t.Fatalf("literal backticks leaked: %q", plain)
	}
	if !strings.Contains(plain, "first") || !strings.Contains(plain, "second") {
		t.Fatalf("list items missing: %q", plain)
	}
	if strings.Contains(plain, "- first") {
		t.Fatalf("literal list marker leaked: %q", plain)
	}
	if !strings.Contains(plain, "▌") {
		t.Fatalf("user bar missing: %q", plain)
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
	h.cachedTotal = 1
	h.startLine = 0
	h.cachedMsgRanges = []msgRange{{startLine: 0, endLine: 1, msgIndex: 0}}
	h.layoutLines = [][]string{{"中a"}}

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
	total := h.cachedTotal
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

	total := h.cachedTotal
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

func TestLatestMessageSelectionAfterGroupedTools(t *testing.T) {
	h := NewChatHistory()
	h.Append(ChatMessage{Role: RoleUser, Text: "run checks"})
	h.Append(ChatMessage{Role: RoleTool, Text: "first tool result", Collapsed: true})
	h.Append(ChatMessage{Role: RoleTool, Text: "second tool result", Collapsed: true})
	h.Append(ChatMessage{Role: RoleAssistant, Text: "latest answer is selectable"})
	// Explicitly collapse the tool group
	h.mu.Lock()
	h.expandedGroups[1] = false
	h.mu.Unlock()
	h.SetMaxRows(100)
	_ = h.Render(80)

	latest, ok := rangeForMsg(h, 3)
	if !ok {
		t.Fatal("missing latest assistant range after grouped tools")
	}
	row := int64(latest.startLine)
	h.Update(core.MouseMsg{Action: core.MousePress, Button: 0, Row: row, Col: 0})
	h.Update(core.MouseMsg{Action: core.MouseMotion, Button: 0, Row: row, Col: 6})
	h.Update(core.MouseMsg{Action: core.MouseRelease, Button: 0, Row: row, Col: 6})

	if selected := h.GetSelectedText(); selected == "" {
		t.Fatal("latest assistant message was not selectable after grouped tools")
	}
	if h.messages[2].Collapsed != true {
		t.Fatal("selecting latest assistant toggled an earlier tool message")
	}
}

func TestToggleHitTestingUsesMessageIndexAfterGroupedTools(t *testing.T) {
	h := NewChatHistory()
	h.Append(ChatMessage{Role: RoleUser, Text: "run checks"})
	h.Append(ChatMessage{Role: RoleTool, Text: "first tool result", Collapsed: true})
	h.Append(ChatMessage{Role: RoleTool, Text: "second tool result", Collapsed: true})
	h.Append(ChatMessage{Role: RoleAssistant, Text: "plain answer"})
	h.Append(ChatMessage{Role: RoleAssistant, Text: "diff answer", Meta: "diff"})
	// Explicitly collapse the tool group
	h.mu.Lock()
	h.expandedGroups[1] = false
	h.mu.Unlock()
	h.SetMaxRows(100)
	_ = h.Render(80)

	groupRange, ok := rangeForMsg(h, 1)
	if !ok {
		t.Fatal("missing tool group range")
	}
	if !h.tryToggleThinkingAtLineLocked(int64(groupRange.startLine)) {
		t.Fatal("tool group header did not toggle")
	}
	plainRange, ok := rangeForMsg(h, 3)
	if !ok {
		t.Fatal("missing plain assistant range")
	}
	if h.tryToggleThinkingAtLineLocked(int64(plainRange.startLine)) {
		t.Fatal("plain assistant message unexpectedly toggled")
	}
	diffRange, ok := rangeForMsg(h, 4)
	if !ok {
		t.Fatal("missing diff assistant range")
	}
	if !h.tryToggleThinkingAtLineLocked(int64(diffRange.startLine)) {
		t.Fatal("diff assistant message did not toggle")
	}
	if !h.messages[4].Collapsed {
		t.Fatal("diff toggle changed the wrong message")
	}
}

func TestChatHistoryScrollbackFinalizesToScrollback(t *testing.T) {
	h := NewChatHistory()
	var emitted []string
	emit := func(lines []string) { emitted = append(emitted, lines...) }
	h.SetScrollback(true, emit)

	h.Append(ChatMessage{Role: RoleUser, Text: "intro question"})
	id := h.AppendDelta("", "Hello, world")
	h.Finalize(id)

	live := core.StripAnsi(strings.Join(h.Render(40), "\n"))
	if !strings.Contains(live, "Hello, world") {
		t.Fatalf("current turn must stay in live viewport after finalize: %q", live)
	}
	if len(emitted) != 0 {
		t.Fatalf("finalize must not archive the current turn: %q", emitted)
	}

	h.Append(ChatMessage{Role: RoleUser, Text: "next question"})
	if len(emitted) == 0 {
		t.Fatal("expected previous turn to emit to scrollback on the next prompt")
	}
	plain := core.StripAnsi(strings.Join(emitted, "\n"))
	if !strings.Contains(plain, "Hello, world") {
		t.Fatalf("scrollback emission missing message text: %q", plain)
	}
	live = core.StripAnsi(strings.Join(h.Render(40), "\n"))
	if strings.Contains(live, "Hello, world") {
		t.Fatalf("archived answer should leave the live viewport: %q", live)
	}
	if !strings.Contains(live, "next question") {
		t.Fatalf("new prompt missing from live viewport: %q", live)
	}
}

func TestChatHistoryScrollbackOnlyEmitsOnce(t *testing.T) {
	h := NewChatHistory()
	var emissionCount int
	emit := func(lines []string) { emissionCount++ }
	h.SetScrollback(true, emit)

	h.Append(ChatMessage{Role: RoleUser, Text: "q1"})
	id := h.AppendDelta("", "first answer")
	h.Finalize(id)
	h.Finalize(id) // idempotent — must not re-emit
	if emissionCount != 0 {
		t.Fatalf("finalize must not emit the current turn, got %d", emissionCount)
	}

	h.Append(ChatMessage{Role: RoleUser, Text: "q2"})
	h.Append(ChatMessage{Role: RoleUser, Text: "q3"})
	if emissionCount != 2 {
		t.Fatalf("expected exactly 2 scrollback emissions (one per archived turn), got %d", emissionCount)
	}
}

func TestChatHistoryScrollbackPendingSharedBetweenUserTurn(t *testing.T) {
	h := NewChatHistory()
	var emitted []string
	emit := func(lines []string) { emitted = append(emitted, lines...) }
	h.SetScrollback(true, emit)

	// First turn: user asks, assistant streams and finalizes — stay live.
	h.Append(ChatMessage{Role: RoleUser, Text: "q1"})
	id1 := h.AppendDelta("", "answer one")
	h.Finalize(id1)
	plain := core.StripAnsi(strings.Join(h.Render(40), "\n"))
	if !strings.Contains(plain, "answer one") {
		t.Fatalf("current answer missing from live viewport: %q", plain)
	}

	// Second turn: new user message archives the previous answer.
	h.Append(ChatMessage{Role: RoleUser, Text: "q2"})
	plain = core.StripAnsi(strings.Join(h.Render(40), "\n"))
	if !strings.Contains(plain, "q2") {
		t.Fatalf("new user prompt missing from live viewport after scrollback: %q", plain)
	}
	if strings.Contains(plain, "answer one") {
		t.Fatalf("previous turn leaked back into viewport: %q", plain)
	}
}

func TestChatHistoryScrollbackEmitsTurnWithoutClippingQuestion(t *testing.T) {
	h := NewChatHistory()
	var emitted []string
	emit := func(lines []string) { emitted = append(emitted, lines...) }
	h.SetScrollback(true, emit)

	h.Append(ChatMessage{Role: RoleUser, Text: "which dynasty came after tang"})
	id := h.AppendDelta("", "the tang fell")
	for i := 0; i < 30; i++ {
		h.Append(ChatMessage{Role: RoleTool, Text: "done", Meta: "bash", ArgPreview: "ls"})
	}
	h.Append(ChatMessage{Role: RoleTool, Text: "done", Meta: "grep", ArgPreview: "dynasty"})
	if !strings.Contains(strings.Join(h.Render(40), "\n"), "dynasty") {
		t.Fatal("prereq: tool records present")
	}
	h.Finalize(id)

	live := core.StripAnsi(strings.Join(h.Render(40), "\n"))
	if !strings.Contains(live, "which dynasty came after tang") {
		t.Fatalf("current prompt must stay in live viewport: %q", live)
	}
	if !strings.Contains(live, "the tang fell") {
		t.Fatalf("current answer must stay in live viewport: %q", live)
	}

	h.Append(ChatMessage{Role: RoleUser, Text: "follow up"})
	plain := core.StripAnsi(strings.Join(emitted, "\n"))
	if !strings.Contains(plain, "which dynasty came after tang") {
		t.Fatalf("user prompt must be emitted to scrollback so the question is not lost: %q", plain)
	}
	if !strings.Contains(plain, "the tang fell") {
		t.Fatalf("finalized answer missing from scrollback emission: %q", plain)
	}
	qIdx := strings.Index(plain, "which dynasty came after tang")
	aIdx := strings.Index(plain, "the tang fell")
	if qIdx < 0 || aIdx < 0 || qIdx > aIdx {
		t.Fatalf("prompt should precede answer in scrollback: %q", plain)
	}
	live = core.StripAnsi(strings.Join(h.Render(40), "\n"))
	if strings.Contains(live, "which dynasty came after tang") {
		t.Fatalf("emitted prompt should be excluded from live viewport: %q", live)
	}
	if strings.Contains(live, "the tang fell") {
		t.Fatalf("emitted answer should be excluded from live viewport: %q", live)
	}
	if !strings.Contains(live, "follow up") {
		t.Fatalf("new prompt missing from live viewport: %q", live)
	}
}

func TestChatHistoryScrollbackEachTurnEmitsItsOwnPromptOnce(t *testing.T) {
	h := NewChatHistory()
	var emitted []string
	emit := func(lines []string) { emitted = append(emitted, lines...) }
	h.SetScrollback(true, emit)

	h.Append(ChatMessage{Role: RoleUser, Text: "first question"})
	id1 := h.AppendDelta("", "first answer")
	h.Finalize(id1)
	h.Finalize(id1)

	h.Append(ChatMessage{Role: RoleUser, Text: "second question"})
	id2 := h.AppendDelta("", "second answer")
	h.Finalize(id2)
	h.Append(ChatMessage{Role: RoleUser, Text: "third question"})

	plain := core.StripAnsi(strings.Join(emitted, "\n"))
	for _, want := range []string{"first question", "first answer", "second question", "second answer"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("turn content %q missing from scrollback emission: %q", want, plain)
		}
	}
	if strings.Contains(plain, "third question") {
		t.Fatalf("current prompt must not be archived yet: %q", plain)
	}
	if strings.Count(plain, "first question") != 1 || strings.Count(plain, "second question") != 1 {
		t.Fatalf("each prompt must emit exactly once, got: %q", plain)
	}
}

func TestChatHistoryScrollbackEmptyAnswerStillEmitsPrompt(t *testing.T) {
	h := NewChatHistory()
	var emitted []string
	emit := func(lines []string) { emitted = append(emitted, lines...) }
	h.SetScrollback(true, emit)

	h.Append(ChatMessage{Role: RoleUser, Text: "run my failing test"})
	id := h.Append(ChatMessage{Role: RoleAssistant})
	h.Finalize(id)

	live := core.StripAnsi(strings.Join(h.Render(40), "\n"))
	if !strings.Contains(live, "run my failing test") {
		t.Fatalf("user prompt must stay in live viewport when the turn answers nothing: %q", live)
	}
	h.Append(ChatMessage{Role: RoleUser, Text: "try again"})
	plain := core.StripAnsi(strings.Join(emitted, "\n"))
	if !strings.Contains(plain, "run my failing test") {
		t.Fatalf("user prompt should still be emitted when the turn answers nothing: %q", plain)
	}
}

func TestChatHistoryScrollbackToolCallsOnlyEmitsPrompt(t *testing.T) {
	h := NewChatHistory()
	var emitted []string
	emit := func(lines []string) { emitted = append(emitted, lines...) }
	h.SetScrollback(true, emit)

	h.Append(ChatMessage{Role: RoleUser, Text: "list files"})
	for i := 0; i < 5; i++ {
		h.Append(ChatMessage{Role: RoleTool, Text: "done", Meta: "bash", ArgPreview: "ls"})
	}
	h.Render(40)
	h.emitPendingTurn()

	live := core.StripAnsi(strings.Join(h.Render(40), "\n"))
	if !strings.Contains(live, "list files") {
		t.Fatalf("current prompt must stay in live viewport when the agent produced only tools: %q", live)
	}
	if !strings.Contains(live, "done") {
		t.Fatalf("tool records should remain in live viewport: %q", live)
	}

	h.Append(ChatMessage{Role: RoleUser, Text: "next"})
	plain := core.StripAnsi(strings.Join(emitted, "\n"))
	if !strings.Contains(plain, "list files") {
		t.Fatalf("user prompt must be emitted to scrollback even when agent produced only tools: %q", plain)
	}
}

func TestChatHistoryVirtualizedViewportKeepsOffscreenText(t *testing.T) {
	h := NewChatHistory()
	h.Append(ChatMessage{Role: RoleUser, Text: "first question"})
	h.Append(ChatMessage{Role: RoleAssistant, Text: "first answer that should remain in the tree"})
	for i := 0; i < 40; i++ {
		h.Append(ChatMessage{Role: RoleSystem, Text: "row"})
	}
	h.SetMaxRows(5)
	live := core.StripAnsi(strings.Join(h.Render(40), "\n"))
	if strings.Contains(live, "first question") {
		t.Fatalf("offscreen prompt leaked into the 5-row viewport: %q", live)
	}
	if int64(len(h.cachedAll)) > 5 {
		t.Fatalf("viewport cache should stay at most 5 lines, got %d", len(h.cachedAll))
	}
	if h.LineCount() <= 5 {
		t.Fatalf("virtual line count should exceed the viewport, got %d", h.LineCount())
	}

	h.ScrollBy(h.LineCount())
	_ = h.Render(40)
	live = core.StripAnsi(strings.Join(h.Render(40), "\n"))
	if !strings.Contains(live, "first question") {
		t.Fatalf("scrolling up should reveal the offscreen prompt: %q", live)
	}
}
