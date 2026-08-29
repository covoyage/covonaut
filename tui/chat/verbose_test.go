package chat

import (
	"strings"
	"testing"
	"time"

	"github.com/covoyage/covonaut/tui/core"
)

func TestToolArgPreview(t *testing.T) {
	args := `{"command":"grep -rn TODO internal/","other":1}`
	if got := ToolArgPreview(args, 48); got != "grep -rn TODO internal/" {
		t.Fatalf("ToolArgPreview = %q", got)
	}
	long := `{"file_path":"` + strings.Repeat("x", 80) + `"}`
	got := ToolArgPreview(long, 10)
	if !strings.HasSuffix(got, "…") || len([]rune(got)) != 11 {
		t.Fatalf("ToolArgPreview long = %q, want truncated", got)
	}
	if got := ToolArgPreview("", 10); got != "" {
		t.Fatalf("ToolArgPreview empty = %q", got)
	}
}

func TestVerboseExpandsCollapsedTool(t *testing.T) {
	h := NewChatHistory()
	h.Append(ChatMessage{
		ID:         "t1",
		Role:       RoleTool,
		Meta:       "bash",
		ArgPreview: "go test ./...",
		Text:       "✓ done",
		Collapsed:  true,
		Detail:     "ok  \nok  \nPASS",
	})

	collapsed := strings.Join(h.Render(100), "\n")
	if !strings.Contains(collapsed, "[+]") {
		t.Fatalf("expected collapsed head, got:\n%s", collapsed)
	}
	if !strings.Contains(collapsed, "(go test ./...)") {
		t.Fatalf("expected arg preview in head, got:\n%s", collapsed)
	}
	if strings.Contains(collapsed, "PASS") {
		t.Fatalf("detail must stay hidden outside verbose mode, got:\n%s", collapsed)
	}

	h.ToggleVerbose()
	verbose := strings.Join(h.Render(100), "\n")
	if strings.Contains(verbose, "[+]") {
		t.Fatalf("verbose mode must expand collapsed head, got:\n%s", verbose)
	}
	if !strings.Contains(verbose, "PASS") {
		t.Fatalf("verbose mode must reveal detail, got:\n%s", verbose)
	}
}

func TestToolArgFitsWidth(t *testing.T) {
	h := NewChatHistory()
	long := strings.Repeat("arg-", 60) // 240 runes
	h.Append(ChatMessage{
		Role:       RoleTool,
		Meta:       "bash",
		ArgPreview: long,
		Text:       "✓ done",
		Collapsed:  true,
	})

	// Narrow window: the head must fit and the argument gets truncated.
	narrow := h.Render(60)
	var toolLine string
	for _, ln := range narrow {
		if strings.Contains(ln, "bash") {
			toolLine = ln
			break
		}
	}
	if toolLine == "" {
		t.Fatalf("tool head not rendered:\n%s", strings.Join(narrow, "\n"))
	}
	if w := core.VisibleWidth(toolLine); w > 60 {
		t.Fatalf("tool head width %d exceeds 60:\n%s", w, toolLine)
	}
	if strings.Contains(toolLine, long) {
		t.Fatalf("narrow window should truncate the argument:\n%s", toolLine)
	}

	// Wide window: the full argument fits.
	wide := h.Render(400)
	joined := strings.Join(wide, "\n")
	if !strings.Contains(joined, long) {
		t.Fatalf("wide window should show the full argument")
	}
}

func TestClickTogglesToolDetail(t *testing.T) {
	h := NewChatHistory()
	h.Append(ChatMessage{Role: RoleUser, Text: "run tests"})
	h.Append(ChatMessage{
		Role:       RoleTool,
		Meta:       "bash",
		ArgPreview: "go test ./...",
		Text:       "✓ done",
		Collapsed:  true,
		Detail:     "PASS\nok  internal/tui",
	})

	h.Render(80)

	// Locate the tool head row in the cached render.
	row := -1
	for i, ln := range h.cachedAll {
		if strings.Contains(ln, "bash") {
			row = i
			break
		}
	}
	if row < 0 {
		t.Fatalf("tool head not found in cache:\n%s", strings.Join(h.cachedAll, "\n"))
	}

	if !h.tryToggleToolDetailAtLineLocked(int64(row)) {
		t.Fatal("click on tool row should be consumed")
	}
	h.Invalidate()
	out := strings.Join(h.Render(80), "\n")
	if !strings.Contains(out, "PASS") {
		t.Fatalf("detail should be visible after click:\n%s", out)
	}

	if !h.tryToggleToolDetailAtLineLocked(int64(row)) {
		t.Fatal("second click should be consumed too")
	}
	h.Invalidate()
	out = strings.Join(h.Render(80), "\n")
	if strings.Contains(out, "PASS") {
		t.Fatalf("detail should hide after second click:\n%s", out)
	}

	// Non-tool rows are not consumed (fall through to text selection).
	if h.tryToggleToolDetailAtLineLocked(0) {
		t.Fatal("click on non-tool row should not be consumed")
	}
}

func TestDisplayLimitsOverride(t *testing.T) {
	app, _ := newTestChatApp(t, ChatAppConfig{
		Limits: DisplayLimits{ToolArgMaxRunes: 8, ToolResultMaxLines: 2},
	})
	app.onToolStart(ToolCallStartChatEvent{ToolCall: ToolCallInfo{
		ID:        "1",
		Name:      "bash",
		Arguments: `{"command":"` + strings.Repeat("x", 50) + `"}`,
	}})
	msgs := app.History().Messages()
	if len(msgs) == 0 {
		t.Fatal("no tool message")
	}
	if got := len([]rune(msgs[0].ArgPreview)); got != 9 { // 8 runes + …
		t.Fatalf("ArgPreview runes = %d, want 9 (capped by ToolArgMaxRunes)", got)
	}

	app.onToolEnd(ToolCallEndChatEvent{ToolCallID: "1", Result: strings.Repeat("line\n", 10)})
	msgs = app.History().Messages()
	detail := msgs[0].Detail
	if lines := strings.Count(detail, "\n") + 1; lines != 3 { // 2 stored + marker
		t.Fatalf("detail lines = %d, want 3 (capped by ToolResultMaxLines)", lines)
	}
}

func TestStaleGhostDroppedAfterSubmitClear(t *testing.T) {
	var requests int
	app, _ := newTestChatApp(t, ChatAppConfig{
		OnGhostRequest: func(prompt string, cb func(string)) {
			requests++
			go func() {
				time.Sleep(40 * time.Millisecond)
				cb("GHOST")
			}()
		},
	})

	// Draft typed, then submitted (the submit path clears the editor).
	app.Editor().SetValue("分析项目结构")
	app.Editor().SetValue("")
	time.Sleep(120 * time.Millisecond) // let the stale completion land
	if got := app.Editor().GhostText(); got != "" {
		t.Fatalf("stale ghost must not appear after submit-clear, got %q", got)
	}

	// Positive control: a fresh draft still receives its own completion.
	app.Editor().SetValue("hello")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if app.Editor().GhostText() == "GHOST" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("fresh ghost never appeared; requests=%d", requests)
}

