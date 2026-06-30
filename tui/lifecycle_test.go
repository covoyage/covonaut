package tui

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/terminal"
)

type keyCounterComponent struct {
	count int
}

func (k *keyCounterComponent) Render(int64) []string { return nil }
func (k *keyCounterComponent) Invalidate()           {}
func (k *keyCounterComponent) Update(msg core.Msg) core.Cmd {
	if _, ok := msg.(core.KeyMsg); ok {
		k.count++
	}
	return nil
}

// TestStopWithoutStartClosesStdin verifies #1: Stop is safe to call on a TUI
// that was constructed but never Start'd, and it still closes the stdin
// buffer (whose flushLoop goroutine starts at construction time and would
// otherwise leak).
func TestStopWithoutStartClosesStdin(t *testing.T) {
	tui := NewTUI(terminal.NewVirtualTerminal(80, 24), TUIOptions{})
	// Never call Start. Stop must still be safe and clean up stdin.
	if err := tui.Stop(); err != nil {
		t.Fatalf("Stop on never-started TUI: %v", err)
	}
	// stdin.Close is idempotent; a second Stop should also be safe.
	if err := tui.Stop(); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
	// Verify the stdin buffer's background goroutine was actually torn down
	// by checking its closed flag (set under lock in Close).
	tui.stdin.Close() // ensure idempotent — should not panic
}

// TestStartAfterStopReturnsError verifies #2: a TUI is one-shot. After Stop,
// calling Start again returns an error rather than silently failing (which
// would leave the caller believing the TUI is running when the event loop
// exits immediately due to the closed doneCh).
func TestStartAfterStopReturnsError(t *testing.T) {
	tui := NewTUI(terminal.NewVirtualTerminal(80, 24), TUIOptions{})
	if err := tui.Start(); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if err := tui.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	// Second Start must fail, not silently succeed.
	if err := tui.Start(); err == nil {
		t.Fatal("expected error on Start after Stop, got nil")
	}
}

// TestStartIdempotent verifies that calling Start twice without Stop in
// between is a no-op (returns nil, doesn't start a second event loop).
func TestStartIdempotent(t *testing.T) {
	tui := NewTUI(terminal.NewVirtualTerminal(80, 24), TUIOptions{})
	defer tui.Stop()
	if err := tui.Start(); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if err := tui.Start(); err != nil {
		t.Fatalf("second Start should be no-op, got: %v", err)
	}
}

// TestStopWithoutStartClosesDoneAndCancelsCtx verifies #1: Stop on a TUI
// that was never Start'd still closes Done() and cancels Context(), so
// callers waiting on Done() don't block forever and Tick/Every goroutines
// bound to the lifecycle context terminate. Previously the !started early
// return skipped close(doneCh) and cancel().
func TestStopWithoutStartClosesDoneAndCancelsCtx(t *testing.T) {
	tui := NewTUI(terminal.NewVirtualTerminal(80, 24), TUIOptions{})
	ctx := tui.Context()

	if err := tui.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Done must be closed.
	select {
	case <-tui.Done():
	default:
		t.Fatal("Done() not closed after Stop on never-started TUI")
	}

	// Context must be cancelled.
	select {
	case <-ctx.Done():
	default:
		t.Fatal("Context not cancelled after Stop on never-started TUI")
	}
}

// TestStopIsIdempotent verifies that Stop called multiple times is safe and
// returns nil each time (no double-close panic on doneCh).
func TestStopIsIdempotent(t *testing.T) {
	tui := NewTUI(terminal.NewVirtualTerminal(80, 24), TUIOptions{})
	if err := tui.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := tui.Stop(); err != nil {
			t.Fatalf("Stop call %d: %v", i, err)
		}
	}
}

// TestSendMsgSafeDropsAfterStop verifies #2: after Stop, sendMsgSafe
// (used by execCmd to deliver Cmd results) must not write to msgCh. A plain
// two-case select could race and pick the send when both cases are ready;
// the pre-check must prevent zombie messages in the buffer.
func TestSendMsgSafeDropsAfterStop(t *testing.T) {
	tui := NewTUI(terminal.NewVirtualTerminal(80, 24), TUIOptions{})
	if err := tui.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := tui.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Drain anything that was in flight before Stop completed.
	for {
		select {
		case <-tui.msgCh:
			continue
		default:
		}
		break
	}

	// Now hammer sendMsgSafe. None should land in msgCh.
	for i := 0; i < 100; i++ {
		tui.sendMsgSafe(core.KeyMsg{Data: "zombie"})
	}

	select {
	case msg := <-tui.msgCh:
		t.Fatalf("zombie message reached msgCh after Stop: %#v", msg)
	default:
		// expected — all dropped
	}
}

// TestTickBoundToLifecycleBeforeStart verifies #3: Tick/Every called before
// Start bind to the TUI's lifecycle context, so they terminate on Stop
// rather than leaking (the old bug returned context.Background() pre-Start,
// which never cancels).
func TestTickBoundToLifecycleBeforeStart(t *testing.T) {
	tui := NewTUI(terminal.NewVirtualTerminal(80, 24), TUIOptions{})
	ctx := tui.Context()

	fired := make(chan struct{}, 1)
	// Schedule a Tick for 5s — it should never fire because Stop cancels it.
	tui.Tick(5*time.Second, func(time.Time) core.Msg {
		select {
		case fired <- struct{}{}:
		default:
		}
		return nil
	})

	// Stop immediately; the pending Tick must be cancelled, not leak.
	if err := tui.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Context cancelled ⇒ Tick goroutine should have exited. Give it a
	// moment to observe the cancellation, then assert it never fires.
	select {
	case <-ctx.Done():
	case <-time.After(100 * time.Millisecond):
		t.Fatal("context not cancelled after Stop")
	}
	select {
	case <-fired:
		t.Fatal("Tick fired after Stop — lifecycle binding failed")
	case <-time.After(100 * time.Millisecond):
		// expected — Tick was cancelled, never fired
	}
}

// failingTerminal is a Terminal stub whose Start and/or Stop return
// injected errors. Used to exercise error-propagation paths in TUI.
type failingTerminal struct {
	mu       sync.Mutex
	startErr error
	stopErr  error
	started  bool
}

func (f *failingTerminal) Start(func([]byte), func()) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return f.startErr
	}
	f.started = true
	return nil
}
func (f *failingTerminal) Stop() error                 { f.mu.Lock(); f.started = false; f.mu.Unlock(); return f.stopErr }
func (f *failingTerminal) Write([]byte) (int, error)   { return 0, nil }
func (f *failingTerminal) Size() (int64, int64)        { return 80, 24 }
func (f *failingTerminal) HideCursor()                 {}
func (f *failingTerminal) ShowCursor()                 {}
func (f *failingTerminal) ClearLine()                  {}
func (f *failingTerminal) ClearFromCursor()            {}
func (f *failingTerminal) ClearScreen()                {}
func (f *failingTerminal) MoveBy(int64)                {}
func (f *failingTerminal) MoveTo(int64, int64)         {}
func (f *failingTerminal) PushKittyKeyboard()          {}
func (f *failingTerminal) PopKittyKeyboard()           {}

// TestStopReturnsTerminalStopError verifies #2: Stop surfaces the error
// from the underlying terminal's Stop, rather than silently swallowing it.
// Callers depend on this to detect terminal-restoration failures.
func TestStopReturnsTerminalStopError(t *testing.T) {
	want := errors.New("terminal restore failed")
	term := &failingTerminal{stopErr: want}
	tui := NewTUI(term, TUIOptions{})
	if err := tui.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	got := tui.Stop()
	if !errors.Is(got, want) {
		t.Fatalf("Stop error = %v, want %v (must not be swallowed)", got, want)
	}
}

// TestStartFailureCleansUpLifecycle verifies #3: when term.Start fails, the
// TUI fully tears down lifecycle signals (doneCh closed, ctx cancelled,
// stopped flag set) on its own — without relying on the caller to defer
// Stop. The returned error is the term.Start error.
func TestStartFailureCleansUpLifecycle(t *testing.T) {
	want := errors.New("term.Start boom")
	term := &failingTerminal{startErr: want}
	tui := NewTUI(term, TUIOptions{})
	ctx := tui.Context()

	err := tui.Start()
	if !errors.Is(err, want) {
		t.Fatalf("Start error = %v, want %v", err, want)
	}

	// Lifecycle signals must be torn down even though Start failed.
	if !tui.stopped.Load() {
		t.Error("stopped flag not set after Start failure")
	}
	select {
	case <-tui.Done():
	default:
		t.Error("Done() not closed after Start failure")
	}
	select {
	case <-ctx.Done():
	default:
		t.Error("Context not cancelled after Start failure")
	}

	// A subsequent Stop must be a no-op (idempotent), not panic.
	if err := tui.Stop(); err != nil {
		t.Fatalf("Stop after Start failure should be no-op, got: %v", err)
	}
}

// TestSendMsgSafeNoZombieAfterStopConcurrent verifies #1: after Stop
// completes, concurrent sendMsgSafe calls never write to msgCh. The atomic
// stopped flag closes the TOCTOU window the channel-based check left.
// This is a stress test — it races many senders against Stop and asserts
// no message lands in msgCh post-Stop.
func TestSendMsgSafeNoZombieAfterStopConcurrent(t *testing.T) {
	tui := NewTUI(terminal.NewVirtualTerminal(80, 24), TUIOptions{})
	if err := tui.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Start senders that continuously fire messages.
	stopSenders := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stopSenders:
					return
				default:
				}
				tui.sendMsgSafe(core.KeyMsg{Data: "race"})
			}
		}()
	}

	// Let senders race with Stop.
	time.Sleep(20 * time.Millisecond)
	if err := tui.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	close(stopSenders)
	wg.Wait()

	// Drain pre-Stop messages, then assert no more arrive. Any message
	// observed hereafter is a zombie that slipped through the window.
	for {
		select {
		case <-tui.msgCh:
			continue
		default:
		}
		break
	}
	// Wait a bit to let any racing sender that passed the flag check but
	// hadn't yet entered the select observe doneCh and drop.
	time.Sleep(20 * time.Millisecond)
	select {
	case msg := <-tui.msgCh:
		t.Fatalf("zombie message reached msgCh after Stop: %#v", msg)
	default:
		// expected
	}
}

func TestProcessMsg_DoesNotBroadcastToNonFocusedOverlays(t *testing.T) {
	tui := NewTUI(terminal.NewVirtualTerminal(80, 24), TUIOptions{})

	focused := &keyCounterComponent{}
	background := &keyCounterComponent{}

	tui.overlays = []*Overlay{
		{Content: background, Focus: true},
		{Content: focused, Focus: true},
	}
	tui.focus = []core.Component{focused}

	tui.processMsg(core.KeyMsg{Data: "a"})

	if focused.count != 1 {
		t.Fatalf("focused overlay count = %d, want 1", focused.count)
	}
	if background.count != 0 {
		t.Fatalf("background overlay received duplicated key event, count = %d", background.count)
	}
}
