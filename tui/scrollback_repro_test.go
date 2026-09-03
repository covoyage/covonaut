package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/covoyage/covonaut/tui/chat"
	"github.com/covoyage/covonaut/tui/terminal"
)

// TestScrollbackTurnKeepsQuestionReadable walks one full turn through the TUI
// in scrollback mode: user prompt -> assistant streams -> finalize (emits the
// turn to native scrollback) -> tool calls appended. It verifies the question
// and the answer both reach the terminal's native scrollback output in order,
// so a long run of tool records can't push the user's prompt out of view.
func TestScrollbackTurnKeepsQuestionReadable(t *testing.T) {
	vt := terminal.NewVirtualTerminal(80, 24)
	tui := NewTUI(vt, TUIOptions{Scrollback: true})
	h := chat.NewChatHistory()
	h.SetScrollback(true, tui.WriteNativeScrollback)
	h.SetOnInvalidate(tui.RequestRender)
	tui.AddChild(h)
	if err := tui.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tui.Stop() })

	wait := func(what string) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if strings.Contains(vt.OutputString(), what) {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %q in output:\n%s", what, vt.OutputString())
	}

	h.Append(chat.ChatMessage{Role: chat.RoleUser, Text: "my question"})
	h.Append(chat.ChatMessage{Role: chat.RoleTool, Text: "done", Meta: "bash", ArgPreview: "ls"})
	wait("my question")

	tid := h.AppendDelta("", "the final answer")
	h.Append(chat.ChatMessage{Role: chat.RoleTool, Text: "done", Meta: "grep", ArgPreview: "code"})
	wait("the final answer")

	h.Finalize(tid)
	wait("the final answer")

	vt.ResetOutput()
	h.Append(chat.ChatMessage{Role: chat.RoleUser, Text: "follow up"})
	wait("the final answer")

	out := vt.OutputString()
	if i, j := strings.Index(out, "my question"), strings.Index(out, "the final answer"); i < 0 || j < 0 {
		t.Fatalf("turn should be emitted to native scrollback on the next prompt:\n%s", out)
	} else if i > j {
		t.Fatalf("question must be emitted before the answer:\n%s", out)
	}
}
