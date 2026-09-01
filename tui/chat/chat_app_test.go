package chat

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/covoyage/covonaut/tui/component"
	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/terminal"
	"github.com/covoyage/covonaut/tui/theme"
)

func newTestChatApp(t *testing.T, cfg ChatAppConfig) (*ChatApp, *terminal.VirtualTerminal) {
	t.Helper()
	vt := terminal.NewVirtualTerminal(80, 24)
	host := &testAppHost{vt: vt}
	cfg.Host = host
	app := NewChatApp(cfg)
	app.SetHost(host)
	if err := app.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { app.Stop() })
	return app, vt
}

type testAppHost struct {
	vt       *terminal.VirtualTerminal
	children []core.Component
	started  bool
	overlays []OverlayRef
}

func (h *testAppHost) Start() error              { h.started = true; return nil }
func (h *testAppHost) Stop() error               { h.started = false; return nil }
func (h *testAppHost) Done() <-chan struct{}     { ch := make(chan struct{}); close(ch); return ch }
func (h *testAppHost) AddChild(c core.Component) { h.children = append(h.children, c) }
func (h *testAppHost) Focus(c core.Component)    {}
func (h *testAppHost) RequestRender()            {}
func (h *testAppHost) PushOverlay(ov OverlayRef) { h.overlays = append(h.overlays, ov) }
func (h *testAppHost) RemoveOverlay(ov OverlayRef) bool {
	for i, o := range h.overlays {
		if o == ov {
			h.overlays = append(h.overlays[:i], h.overlays[i+1:]...)
			return true
		}
	}
	return false
}
func (h *testAppHost) TerminalSize() (cols, rows int64) { return h.vt.Size() }
func (h *testAppHost) EnableMouse(mode string)          {}
func (h *testAppHost) DisableMouse()                    {}

type stubComp struct{ lines []string }

func (s stubComp) Render(width int64) []string { return s.lines }
func (s stubComp) Invalidate()                 {}

func TestChatLayoutNeverTallerThanTerminal(t *testing.T) {
	vt := terminal.NewVirtualTerminal(40, 5)
	status := component.NewStatusBar()
	status.SetMode("status-line")
	l := &chatLayout{
		host:      &testAppHost{vt: vt},
		history:   NewChatHistory(),
		editor:    stubComp{lines: []string{"input"}},
		footer:    stubComp{lines: []string{"footer-line"}},
		statusBar: status,
	}
	l.history.Append(ChatMessage{Role: RoleAssistant, Text: "hello from history"})

	out := l.Render(40)
	if int64(len(out)) > 5 {
		t.Fatalf("layout taller than terminal: got %d rows\n%s", len(out), strings.Join(out, "\n"))
	}
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "footer-line") {
		t.Fatalf("footer dropped when terminal is short:\n%s", joined)
	}
	if !strings.Contains(joined, "status-line") {
		t.Fatalf("status bar dropped when terminal is short:\n%s", joined)
	}
}

func TestChatAppMessageDeltaStream(t *testing.T) {
	app, _ := newTestChatApp(t, ChatAppConfig{})

	app.onAgentStart(AgentStartChatEvent{})
	app.onMessageDelta(MessageDeltaChatEvent{Delta: "Hello, "})
	app.onMessageDelta(MessageDeltaChatEvent{Delta: "world!"})

	msgs := app.History().Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 streaming msg, got %d", len(msgs))
	}
	if msgs[0].Text != "Hello, world!" {
		t.Fatalf("text=%q", msgs[0].Text)
	}
	if !msgs[0].Pending {
		t.Fatalf("expected pending during stream")
	}

	app.onAgentEnd(AgentEndChatEvent{})
	msgs = app.History().Messages()
	if msgs[0].Pending {
		t.Fatalf("agent_end should finalize streaming message")
	}
}

func TestChatAppMessageDeltaRoutesThinking(t *testing.T) {
	app, _ := newTestChatApp(t, ChatAppConfig{})

	app.onAgentStart(AgentStartChatEvent{})
	app.onMessageDelta(MessageDeltaChatEvent{Delta: "inspect first", Kind: "thinking"})
	app.onMessageDelta(MessageDeltaChatEvent{Delta: "final answer", Kind: "text"})

	msgs := app.History().Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 streaming msg, got %d", len(msgs))
	}
	if msgs[0].Text != "final answer" {
		t.Fatalf("text = %q", msgs[0].Text)
	}
	if len(msgs[0].ThinkingSegments) != 1 || msgs[0].ThinkingSegments[0].Text != "inspect first" {
		t.Fatalf("thinking segments = %+v", msgs[0].ThinkingSegments)
	}
}

// TestChatAppAutoRetryDiscardsStalePartialStream reproduces a bug where a
// transient provider error mid-stream (followed by an automatic retry)
// caused the retried attempt's freshly streamed text to be appended after
// the first, failed attempt's partial text — rendering as visibly
// duplicated/concatenated text in the same message bubble (e.g. "Let me
// look at X... Let me look at X...").
func TestChatAppAutoRetryDiscardsStalePartialStream(t *testing.T) {
	app, _ := newTestChatApp(t, ChatAppConfig{})

	app.onAgentStart(AgentStartChatEvent{})
	app.onMessageDelta(MessageDeltaChatEvent{Delta: "Let me look at the docs."})

	msgs := app.History().Messages()
	if len(msgs) != 1 || msgs[0].Text != "Let me look at the docs." {
		t.Fatalf("expected 1 partial streaming msg, got %+v", msgs)
	}

	// The provider call failed and is being retried; the retry event should
	// discard the stale partial message rather than let the retried
	// attempt's deltas append onto it.
	app.onAutoRetry(AutoRetryChatEvent{Attempt: 1, MaxRetries: 3, Delay: time.Millisecond})

	msgs = app.History().Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected only the retry system message after discarding stale stream, got %+v", msgs)
	}
	if msgs[0].Role != RoleSystem {
		t.Fatalf("expected remaining message to be the retry system notice, got %+v", msgs[0])
	}

	// The retried attempt re-streams the assistant reply from scratch.
	app.onMessageDelta(MessageDeltaChatEvent{Delta: "Let me look at the docs."})

	msgs = app.History().Messages()
	if len(msgs) != 2 {
		t.Fatalf("expected retry system msg + fresh assistant msg, got %+v", msgs)
	}
	if msgs[1].Text != "Let me look at the docs." {
		t.Fatalf("retried stream text should not be duplicated, got %q", msgs[1].Text)
	}

	app.onAgentEnd(AgentEndChatEvent{})
}

// TestChatAppRepetitionRecoveryDiscardsStalePartialStream mirrors
// TestChatAppAutoRetryDiscardsStalePartialStream but for the
// repetition-recovery ladder (runLoop injecting a corrective steering nudge
// after detecting a degeneration loop): the stale in-flight streamed message
// must be discarded so the next attempt's deltas start a fresh bubble
// instead of concatenating onto the abandoned, degenerate partial text.
func TestChatAppRepetitionRecoveryDiscardsStalePartialStream(t *testing.T) {
	app, _ := newTestChatApp(t, ChatAppConfig{})

	app.onAgentStart(AgentStartChatEvent{})
	app.onMessageDelta(MessageDeltaChatEvent{Delta: "Let me look at the docs."})

	msgs := app.History().Messages()
	if len(msgs) != 1 || msgs[0].Text != "Let me look at the docs." {
		t.Fatalf("expected 1 partial streaming msg, got %+v", msgs)
	}

	app.onRepetitionRecovery(RepetitionRecoveryChatEvent{Kind: "stream", Attempt: 0, MaxAttempts: 2})

	msgs = app.History().Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected only the recovery system message after discarding stale stream, got %+v", msgs)
	}
	if msgs[0].Role != RoleSystem {
		t.Fatalf("expected remaining message to be the recovery system notice, got %+v", msgs[0])
	}

	// The retried attempt re-streams the assistant reply from scratch.
	app.onMessageDelta(MessageDeltaChatEvent{Delta: "Let me look at the docs."})

	msgs = app.History().Messages()
	if len(msgs) != 2 {
		t.Fatalf("expected recovery system msg + fresh assistant msg, got %+v", msgs)
	}
	if msgs[1].Text != "Let me look at the docs." {
		t.Fatalf("retried stream text should not be duplicated, got %q", msgs[1].Text)
	}

	app.onAgentEnd(AgentEndChatEvent{})
}

func TestChatAppToolLifecycle(t *testing.T) {
	app, _ := newTestChatApp(t, ChatAppConfig{ShowTimings: true})

	app.onToolStart(ToolCallStartChatEvent{
		ToolCall: ToolCallInfo{ID: "t1", Name: "search"},
	})
	msgs := app.History().Messages()
	if len(msgs) != 1 || msgs[0].Meta != "search" {
		t.Fatalf("expected tool-start msg with name 'search', got %+v", msgs)
	}

	app.onToolEnd(ToolCallEndChatEvent{
		ToolCallID: "t1",
		ToolName:   "search",
		Duration:   50 * time.Millisecond,
	})
	msgs = app.History().Messages()
	if len(msgs) != 1 {
		t.Fatalf("tool-end should update in place, got %d msgs", len(msgs))
	}
	if !strings.Contains(msgs[0].Text, theme.SymbolCheck) {
		t.Fatalf("expected check mark in result: %q", msgs[0].Text)
	}
}

func TestChatAppToolError(t *testing.T) {
	app, _ := newTestChatApp(t, ChatAppConfig{})
	app.onToolStart(ToolCallStartChatEvent{
		ToolCall: ToolCallInfo{ID: "t1", Name: "x"},
	})
	app.onToolEnd(ToolCallEndChatEvent{
		ToolCallID: "t1", ToolName: "x", Err: errors.New("boom"),
	})
	msgs := app.History().Messages()
	if !strings.Contains(msgs[0].Text, "failed") {
		t.Fatalf("expected 'failed' in msg: %q", msgs[0].Text)
	}
}

// TestChatAppEditorDiffExpanded verifies that the inline diff produced for
// editor tools (write_file, edit, ...) defaults to expanded (Collapsed=false)
// so the user can see changes without clicking, and only collapses on click.
func TestChatAppEditorDiffExpanded(t *testing.T) {
	cases := []struct {
		name     string
		toolName string
		result   string
	}{
		{
			name:     "write_file",
			toolName: "write_file",
			result:   `{"path":"foo.go","content":"package main\nfunc main(){}"}`,
		},
		{
			name:     "edit",
			toolName: "edit",
			result:   `{"path":"foo.go","diff":"@@ -1 +1 @@\n-old\n+new"}`,
		},
		{
			name:     "edit_block",
			toolName: "edit_block",
			result:   `{"path":"foo.go","diff":"@@ -1 +1 @@\n-old\n+new"}`,
		},
		{
			name:     "apply_patch",
			toolName: "apply_patch",
			result:   `{"path":"foo.go","patch":"@@ -1 +1 @@\n-old\n+new"}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app, _ := newTestChatApp(t, ChatAppConfig{})
			app.onToolStart(ToolCallStartChatEvent{
				ToolCall: ToolCallInfo{ID: "t1", Name: tc.toolName},
			})
			app.onToolEnd(ToolCallEndChatEvent{
				ToolCallID: "t1", ToolName: tc.toolName, Result: tc.result,
			})

			var diffMsg *ChatMessage
			msgs := app.History().Messages()
			for i := range msgs {
				if msgs[i].Meta == "diff" {
					diffMsg = &msgs[i]
					break
				}
			}
			if diffMsg == nil {
				t.Fatalf("%s: expected an inline diff message", tc.toolName)
			}
			if diffMsg.Collapsed {
				t.Fatalf("%s: diff should default to expanded (Collapsed=false)", tc.toolName)
			}
		})
	}
}

func TestChatAppEditorSubmit(t *testing.T) {
	var captured string
	app, _ := newTestChatApp(t, ChatAppConfig{
		OnSubmit: func(_ context.Context, in string) { captured = in },
	})
	app.editor.SetValue("hello")
	app.editor.Update(core.KeyMsg{Data: "\r"})

	if captured != "hello" {
		t.Fatalf("OnSubmit captured=%q want hello", captured)
	}
	msgs := app.History().Messages()
	if len(msgs) == 0 || msgs[0].Role != RoleUser {
		t.Fatalf("expected user echo in history, got %+v", msgs)
	}
	if app.editor.GetValue() != "" {
		t.Fatalf("editor should be cleared after submit; got %q", app.editor.GetValue())
	}
}

func TestChatAppBusyIdle(t *testing.T) {
	app, _ := newTestChatApp(t, ChatAppConfig{})
	app.Busy("working")
	if !app.loader.IsRunning() {
		t.Fatalf("loader should be running")
	}
	app.Idle()
	if app.loader.IsRunning() {
		t.Fatalf("loader should be stopped")
	}
}

func TestChatAppHoldSubmitDefersUntilReady(t *testing.T) {
	var captured string
	app, _ := newTestChatApp(t, ChatAppConfig{
		OnSubmit: func(_ context.Context, in string) { captured = in },
	})
	app.HoldSubmit()
	app.editor.SetValue("hello")
	app.editor.Update(core.KeyMsg{Data: "\r"})
	if captured != "" {
		t.Fatalf("submit while held captured=%q", captured)
	}
	if got := app.editor.GetValue(); got != "hello" {
		t.Fatalf("draft should remain while held, got %q", got)
	}
	app.SetReady()
	if captured != "hello" {
		t.Fatalf("SetReady should flush pending submit, got %q", captured)
	}
	if app.editor.GetValue() != "" {
		t.Fatalf("editor should clear after flush, got %q", app.editor.GetValue())
	}
}

func TestCtrlCPrefersCopyOverInterrupt(t *testing.T) {
	var interrupted bool
	app, _ := newTestChatApp(t, ChatAppConfig{
		OnInterrupt: func() { interrupted = true },
	})
	app.Busy("working") // agent "running"

	app.editor.Update(core.KeyMsg{Data: "hello"})
	app.editor.Render(40)                                                      // populate lastVisuals; default prompt "> " is 2 cols wide
	app.editor.Update(core.MouseMsg{Action: core.MousePress, Row: 0, Col: 2})  // buffer col 0
	app.editor.Update(core.MouseMsg{Action: core.MouseMotion, Row: 0, Col: 7}) // buffer col 5
	app.editor.Update(core.MouseMsg{Action: core.MouseRelease, Row: 0, Col: 7})
	if app.editor.GetSelectedText() != "hello" {
		t.Fatalf("setup: expected editor selection %q, got %q", "hello", app.editor.GetSelectedText())
	}

	// Mirrors the real TUI dispatch order (tui.go processMsg): the focused
	// component (the editor) receives every KeyMsg first, and non-focused
	// children (chatLayout, which owns the Ctrl/Cmd+C handling) receive it
	// afterward. A prior bug cleared the editor's mouse-drag selection
	// unconditionally on every keystroke it saw — including Ctrl+C itself —
	// so by the time chatLayout's handler ran, the selection was already
	// gone and nothing got copied.
	keyMsg := core.KeyMsg{Data: "\x03"} // Ctrl+C
	app.editor.Update(keyMsg)
	app.layout.Update(keyMsg)

	if interrupted {
		t.Fatalf("expected Ctrl+C to copy the active selection instead of interrupting")
	}
	if app.editor.GetSelectedText() != "hello" {
		t.Fatalf("expected selection to remain visible after copy (matching standard clipboard UX), got %q", app.editor.GetSelectedText())
	}
}

func TestCmdASelectsAllEditorText(t *testing.T) {
	app, _ := newTestChatApp(t, ChatAppConfig{})
	app.editor.Update(core.KeyMsg{Data: "hello world"})

	// Kitty CSI-u encoding for Cmd+A (Super+A): the same dual-dispatch path
	// as TestCtrlCPrefersCopyOverInterrupt — the focused editor sees the key
	// first, then chatLayout.
	keyMsg := core.KeyMsg{Data: "\x1b[97;9u"}
	app.editor.Update(keyMsg)
	app.layout.Update(keyMsg)

	if got := app.editor.GetSelectedText(); got != "hello world" {
		t.Fatalf("expected Cmd+A to select all editor text, got %q", got)
	}
}

func TestChatAppSubscribe(t *testing.T) {
	app, _ := newTestChatApp(t, ChatAppConfig{})

	adapter := &testSubscriber{handlers: make(map[ChatEventType]func(ChatEvent))}
	app.Subscribe(adapter)

	if len(adapter.handlers) != 15 {
		t.Fatalf("expected 15 handlers registered, got %d", len(adapter.handlers))
	}
	for _, et := range []ChatEventType{
		ChatEventAgentStart, ChatEventAgentEnd, ChatEventAgentError,
		ChatEventTurnStart, ChatEventTurnEnd, ChatEventMessageDelta, ChatEventMessageReset,
		ChatEventToolCallStart, ChatEventToolCallEnd,
		ChatEventHandoffStart, ChatEventHandoffEnd,
		ChatEventCompactionStart, ChatEventCompactionEnd,
		ChatEventAutoRetry, ChatEventRepetitionRecovery,
	} {
		if adapter.handlers[et] == nil {
			t.Errorf("handler for %s not registered", et)
		}
	}
}

func TestChatAppMessageResetRemovesFailedAttempt(t *testing.T) {
	app, _ := newTestChatApp(t, ChatAppConfig{})
	app.onMessageDelta(MessageDeltaChatEvent{Delta: "partial", AttemptID: "attempt-1"})
	if len(app.history.Messages()) != 1 {
		t.Fatalf("messages before reset = %#v", app.history.Messages())
	}
	app.onMessageReset(MessageResetChatEvent{AttemptID: "attempt-1"})
	if len(app.history.Messages()) != 0 {
		t.Fatalf("messages after reset = %#v", app.history.Messages())
	}
}

type testSubscriber struct {
	handlers map[ChatEventType]func(ChatEvent)
}

func (s *testSubscriber) On(eventType ChatEventType, handler func(ChatEvent)) {
	s.handlers[eventType] = handler
}

func TestIsPrimaryShortcutMod(t *testing.T) {
	tests := []struct {
		name string
		mods terminal.Modifier
		want bool
	}{
		{name: "ctrl", mods: terminal.ModCtrl, want: true},
		{name: "super", mods: terminal.ModSuper, want: true},
		{name: "meta", mods: terminal.ModMeta, want: true},
		{name: "none", mods: terminal.ModNone, want: false},
		{name: "alt only", mods: terminal.ModAlt, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isPrimaryShortcutMod(tc.mods); got != tc.want {
				t.Fatalf("isPrimaryShortcutMod(%v)=%v want=%v", tc.mods, got, tc.want)
			}
		})
	}
}

func TestIsCopyShortcut(t *testing.T) {
	tests := []struct {
		name string
		key  terminal.Key
		want bool
	}{
		{name: "cmd lowercase c", key: terminal.Key{Name: "c", Mods: terminal.ModSuper}, want: true},
		{name: "cmd uppercase C", key: terminal.Key{Name: "C", Mods: terminal.ModSuper | terminal.ModShift}, want: true},
		{name: "meta uppercase C", key: terminal.Key{Name: "C", Mods: terminal.ModMeta | terminal.ModShift}, want: true},
		{name: "ctrl c", key: terminal.Key{Name: "c", Mods: terminal.ModCtrl}, want: true},
		{name: "ctrl insert", key: terminal.Key{Name: "insert", Mods: terminal.ModCtrl}, want: true},
		{name: "plain y", key: terminal.Key{Name: "y", Mods: terminal.ModNone}, want: false},
		{name: "plain c", key: terminal.Key{Name: "c", Mods: terminal.ModNone}, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isCopyShortcut(tc.key); got != tc.want {
				t.Fatalf("isCopyShortcut(%+v)=%v want=%v", tc.key, got, tc.want)
			}
		})
	}
}

func TestFencedContentPreview(t *testing.T) {
	// Known language → language-fenced block.
	out := fencedContentPreview("/tmp/weather.py", "import os\nprint(os)\n")
	if !strings.Contains(out, "```python") {
		t.Errorf("expected python fence, got %q", out)
	}
	if !strings.Contains(out, "import os") {
		t.Errorf("content missing: %q", out)
	}

	// Unknown language → plain fallback with indent, no fence.
	out = fencedContentPreview("/tmp/notes.unknown", "hello\n")
	if strings.Contains(out, "```") {
		t.Errorf("unknown language should not fence, got %q", out)
	}
	if !strings.Contains(out, "  hello") {
		t.Errorf("expected indented plain preview, got %q", out)
	}

	// Truncation trailer outside the fence.
	content := strings.Repeat("line\n", 10)
	out = fencedContentPreview("/tmp/big.go", content)
	if !strings.Contains(out, "```go") {
		t.Errorf("expected go fence, got %q", out)
	}
	if !strings.Contains(out, "+5 lines") {
		t.Errorf("expected truncation trailer, got %q", out)
	}
	fenceCount := strings.Count(out, "```")
	if fenceCount != 2 {
		t.Errorf("expected exactly 2 fence markers, got %d: %q", fenceCount, out)
	}
}
