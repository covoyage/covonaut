package tui

import (
	"strings"
	"sync"
	"testing"
)

// recordingTerminal captures the order of escape sequences written to it and
// Kitty push/pop calls, so tests can assert that the Kitty push happens AFTER
// the alt-screen enter and the pop happens BEFORE the alt-screen leave.
type recordingTerminal struct {
	mu      sync.Mutex
	events  []string // ordered log of significant events
	started bool
}

func (r *recordingTerminal) Start(func([]byte), func()) error {
	r.mu.Lock()
	r.started = true
	r.mu.Unlock()
	return nil
}

func (r *recordingTerminal) Stop() error {
	r.mu.Lock()
	r.started = false
	r.mu.Unlock()
	return nil
}

func (r *recordingTerminal) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := string(p)
	switch {
	case strings.HasPrefix(s, "\x1b[?1049h"):
		r.events = append(r.events, "alt_enter")
	case strings.HasPrefix(s, "\x1b[?1049l"):
		r.events = append(r.events, "alt_leave")
	}
	return len(p), nil
}

func (r *recordingTerminal) Size() (int64, int64) { return 80, 24 }

func (r *recordingTerminal) HideCursor()      {}
func (r *recordingTerminal) ShowCursor()       {}
func (r *recordingTerminal) ClearLine()       {}
func (r *recordingTerminal) ClearFromCursor() {}
func (r *recordingTerminal) ClearScreen()     {}
func (r *recordingTerminal) MoveBy(int64)     {}
func (r *recordingTerminal) MoveTo(int64, int64) {}
func (r *recordingTerminal) PushKittyKeyboard() {
	r.mu.Lock()
	r.events = append(r.events, "kitty_push")
	r.mu.Unlock()
}
func (r *recordingTerminal) PopKittyKeyboard() {
	r.mu.Lock()
	r.events = append(r.events, "kitty_pop")
	r.mu.Unlock()
}

func (r *recordingTerminal) events_() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.events))
	copy(out, r.events)
	return out
}

// TestKittyPushAfterAltScreenEnter verifies that when using the alt screen,
// PushKittyKeyboard is called AFTER \x1b[?1049h (entering alt screen), not
// before. The Kitty keyboard protocol state is reset on alt-screen switch,
// so pushing before the switch is wasted and modifier-rich keys (e.g. Cmd+C
// on macOS) lose their modifiers.
func TestKittyPushAfterAltScreenEnter(t *testing.T) {
	term := &recordingTerminal{}
	tui := NewTUI(term, TUIOptions{AltScreen: true})
	if err := tui.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	_ = tui.Stop()

	events := term.events_()
	// Find alt_enter and kitty_push indices.
	altEnterIdx := indexOf(events, "alt_enter")
	if altEnterIdx < 0 {
		t.Fatalf("alt_enter not emitted; events=%v", events)
	}
	kittyPushIdx := indexOf(events, "kitty_push")
	if kittyPushIdx < 0 {
		t.Fatalf("kitty_push not emitted; events=%v", events)
	}
	if kittyPushIdx < altEnterIdx {
		t.Fatalf("kitty_push (idx %d) before alt_enter (idx %d); events=%v — "+
			"Kitty push must happen AFTER entering alt screen",
			kittyPushIdx, altEnterIdx, events)
	}
}

// TestKittyPopBeforeAltScreenLeave verifies that PopKittyKeyboard is called
// BEFORE \x1b[?1049l (leaving alt screen), so the pop targets the alt-screen
// push rather than the main-screen push.
func TestKittyPopBeforeAltScreenLeave(t *testing.T) {
	term := &recordingTerminal{}
	tui := NewTUI(term, TUIOptions{AltScreen: true})
	if err := tui.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	_ = tui.Stop()

	events := term.events_()
	kittyPopIdx := indexOf(events, "kitty_pop")
	if kittyPopIdx < 0 {
		t.Fatalf("kitty_pop not emitted; events=%v", events)
	}
	altLeaveIdx := indexOf(events, "alt_leave")
	if altLeaveIdx < 0 {
		t.Fatalf("alt_leave not emitted; events=%v", events)
	}
	if kittyPopIdx > altLeaveIdx {
		t.Fatalf("kitty_pop (idx %d) after alt_leave (idx %d); events=%v — "+
			"Kitty pop must happen BEFORE leaving alt screen",
			kittyPopIdx, altLeaveIdx, events)
	}
}

// TestNoKittyPushWithoutAltScreen verifies that without alt screen, the TUI
// does not call PushKittyKeyboard/PopKittyKeyboard (terminal.Start/Stop handle
// the single push/pop for the main screen).
func TestNoKittyPushWithoutAltScreen(t *testing.T) {
	term := &recordingTerminal{}
	tui := NewTUI(term, TUIOptions{AltScreen: false})
	if err := tui.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	_ = tui.Stop()

	events := term.events_()
	for _, e := range events {
		if e == "kitty_push" || e == "kitty_pop" {
			t.Fatalf("unexpected Kitty push/pop without alt screen; events=%v", events)
		}
	}
}

func indexOf(slice []string, want string) int {
	for i, v := range slice {
		if v == want {
			return i
		}
	}
	return -1
}
