package chat

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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
	for _, r := range "hello" {
		app.editor.Update(core.KeyMsg{Data: string(r)})
	}
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

	if len(adapter.handlers) != 13 {
		t.Fatalf("expected 13 handlers registered, got %d", len(adapter.handlers))
	}
	for _, et := range []ChatEventType{
		ChatEventAgentStart, ChatEventAgentEnd, ChatEventAgentError,
		ChatEventTurnStart, ChatEventTurnEnd, ChatEventMessageDelta,
		ChatEventToolCallStart, ChatEventToolCallEnd,
		ChatEventHandoffStart, ChatEventHandoffEnd,
		ChatEventCompactionStart, ChatEventCompactionEnd,
		ChatEventAutoRetry,
	} {
		if adapter.handlers[et] == nil {
			t.Errorf("handler for %s not registered", et)
		}
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
