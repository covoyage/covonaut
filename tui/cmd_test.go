package tui

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/terminal"
)

// --- Test fixtures ---------------------------------------------------------

// recordingComponent captures every Msg its Update receives into a slice.
// Returning a Cmd from Update is supported via the nextCmd field.
type recordingComponent struct {
	mu      sync.Mutex
	received []core.Msg
	nextCmd  core.Cmd
}

func (r *recordingComponent) Render(int64) []string { return []string{""} }
func (r *recordingComponent) Invalidate()           {}
func (r *recordingComponent) Update(msg core.Msg) core.Cmd {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.received = append(r.received, msg)
	cmd := r.nextCmd
	r.nextCmd = nil // one-shot: avoid re-triggering on the result Msg
	return cmd
}

// setNextCmd sets the Cmd to return from the next Update call. Thread-safe:
// must be used instead of writing nextCmd directly, because Update runs on
// the event loop goroutine while tests set the field from the test goroutine.
func (r *recordingComponent) setNextCmd(cmd core.Cmd) {
	r.mu.Lock()
	r.nextCmd = cmd
	r.mu.Unlock()
}
func (r *recordingComponent) msgs() []core.Msg {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]core.Msg, len(r.received))
	copy(out, r.received)
	return out
}

// markerMsg is a minimal Msg carrying an int id, used to assert ordering.
type markerMsg struct{ id int }

func (markerMsg) MsgMarker() {}

func newTUIWith(comp core.Component) *TUI {
	t := NewTUI(terminal.NewVirtualTerminal(80, 24), TUIOptions{})
	t.AddChild(comp)
	t.Focus(comp)
	return t
}

// --- Batch tests -----------------------------------------------------------

// TestBatchRunsCmdsAsynchronously verifies that BatchMsg dispatches every
// Cmd concurrently and does not block the event loop on slow Cmds.
func TestBatchRunsCmdsAsynchronously(t *testing.T) {
	comp := &recordingComponent{}
	tui := newTUIWith(comp)
	if err := tui.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer tui.Stop()

	// Three Cmds each sleeping 50ms then emitting a marker. If they ran
	// synchronously total time would be ≥150ms; concurrently ≈50ms.
	slow := func(id int) core.Cmd {
		return func() core.Msg {
			time.Sleep(50 * time.Millisecond)
			return markerMsg{id}
		}
	}
	tui.SendMsg(core.BatchMsg{slow(1), slow(2), slow(3)})

	// Wait long enough for concurrent execution but not for serial.
	deadline := time.After(120 * time.Millisecond)
	for {
		select {
		case <-deadline:
			t.Fatalf("batch did not complete in time; msgs=%v", len(comp.msgs()))
		case <-time.After(10 * time.Millisecond):
		}
		if len(comp.msgs()) >= 3 {
			break
		}
	}

	ids := make(map[int]bool)
	for _, m := range comp.msgs() {
		if mm, ok := m.(markerMsg); ok {
			ids[mm.id] = true
		}
	}
	if len(ids) != 3 {
		t.Fatalf("expected 3 distinct markers, got %d: %v", len(ids), ids)
	}
}

// --- Sequence tests --------------------------------------------------------

// TestSequenceRunsInOrder verifies that SequenceMessage executes Cmds
// asynchronously but preserves order: the result Msgs arrive in sequence.
func TestSequenceRunsInOrder(t *testing.T) {
	comp := &recordingComponent{}
	tui := newTUIWith(comp)
	if err := tui.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer tui.Stop()

	cmds := make([]core.Cmd, 5)
	for i := 0; i < 5; i++ {
		i := i
		cmds[i] = func() core.Msg {
			// Tiny sleep so concurrency would scramble order if it
			// were running parallel — Sequence must NOT parallelize.
			time.Sleep(5 * time.Millisecond)
			return markerMsg{i}
		}
	}
	tui.SendMsg(core.SequenceMessage(cmds))

	deadline := time.After(500 * time.Millisecond)
	var ordered []int
	for {
		select {
		case <-deadline:
			t.Fatalf("sequence did not complete; got %d markers", len(ordered))
		case <-time.After(5 * time.Millisecond):
		}
		for _, m := range comp.msgs() {
			if mm, ok := m.(markerMsg); ok {
				if !containsInt(ordered, mm.id) {
					ordered = append(ordered, mm.id)
				}
			}
		}
		if len(ordered) == 5 {
			break
		}
	}

	for i, v := range ordered {
		if v != i {
			t.Fatalf("sequence order broken at %d: got %d, want %d (full: %v)", i, v, i, ordered)
		}
	}
}

func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// --- Panic recovery test ---------------------------------------------------

// TestPanicInCmdEmitsPanicMsg verifies that a panicking Cmd does not kill the
// event loop silently; a PanicMsg is delivered instead.
func TestPanicInCmdEmitsPanicMsg(t *testing.T) {
	comp := &recordingComponent{}
	tui := newTUIWith(comp)
	if err := tui.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer tui.Stop()

	// Trigger a Cmd via Update return.
	comp.setNextCmd(func() core.Msg {
		panic("boom")
	})
	tui.SendMsg(core.KeyMsg{Data: "x"}) // Update returns the panicking Cmd

	var got *core.PanicMsg
	deadline := time.After(200 * time.Millisecond)
	for got == nil {
		select {
		case <-deadline:
			t.Fatalf("no PanicMsg received; msgs=%v", len(comp.msgs()))
		case <-time.After(5 * time.Millisecond):
		}
		for _, m := range comp.msgs() {
			if pm, ok := m.(core.PanicMsg); ok {
				got = &pm
				break
			}
		}
	}

	if got.Err != "boom" {
		t.Fatalf("PanicMsg.Err = %v, want \"boom\"", got.Err)
	}
	if !strings.Contains(got.Stack, "panic") && !strings.Contains(got.Stack, "execCmd") {
		t.Fatalf("PanicMsg.Stack does not look like a stack trace: %q", got.Stack)
	}
}

// --- Concurrency sanity check ---------------------------------------------

// TestEventLoopSurvivesManyConcurrentCmds stress-tests the pump with many
// concurrent Cmds to ensure no deadlock or dropped messages.
func TestEventLoopSurvivesManyConcurrentCmds(t *testing.T) {
	comp := &recordingComponent{}
	tui := newTUIWith(comp)
	if err := tui.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer tui.Stop()

	const n = 50
	var count int32
	cmds := make([]core.Cmd, n)
	for i := 0; i < n; i++ {
		cmds[i] = func() core.Msg {
			atomic.AddInt32(&count, 1)
			return markerMsg{int(atomic.LoadInt32(&count))}
		}
	}
	tui.SendMsg(core.BatchMsg(cmds))

	deadline := time.After(1 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatalf("only %d/%d cmds ran", atomic.LoadInt32(&count), n)
		case <-time.After(10 * time.Millisecond):
		}
		if atomic.LoadInt32(&count) == int32(n) {
			break
		}
	}
}

// --- WithContext cancellation test ----------------------------------------

// TestWithContextDiscardsResultOnCancel verifies that WithContext drops the
// Cmd's result Msg when the context is cancelled while the Cmd is still
// running. The old implementation only checked ctx before running the Cmd,
// so a slow Cmd's result would leak through even after cancellation.
func TestWithContextDiscardsResultOnCancel(t *testing.T) {
	comp := &recordingComponent{}
	tui := newTUIWith(comp)
	if err := tui.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer tui.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	slowCmd := func() core.Msg {
		time.Sleep(100 * time.Millisecond)
		return markerMsg{99} // should be discarded
	}

	// Trigger the WithContext-wrapped Cmd via Update return.
	comp.setNextCmd(core.WithContext(ctx, slowCmd))
	tui.SendMsg(core.KeyMsg{Data: "x"})

	// Cancel almost immediately — the Cmd is still sleeping.
	time.Sleep(10 * time.Millisecond)
	cancel()

	// Wait past the Cmd's sleep so it completes; verify no markerMsg arrived.
	time.Sleep(150 * time.Millisecond)

	for _, m := range comp.msgs() {
		if mm, ok := m.(markerMsg); ok && mm.id == 99 {
			t.Fatalf("cancelled Cmd's result leaked through: %v", m)
		}
	}
}

// TestWithContextDeliversResultWhenNotCancelled verifies the happy path: if
// ctx is not cancelled, the Cmd's result is delivered (unwrapped from
// CtxMessage by processMsg) to the focused component's Update.
func TestWithContextDeliversResultWhenNotCancelled(t *testing.T) {
	comp := &recordingComponent{}
	tui := newTUIWith(comp)
	if err := tui.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer tui.Stop()

	ctx := context.Background()
	comp.setNextCmd(core.WithContext(ctx, func() core.Msg {
		return markerMsg{42}
	}))
	tui.SendMsg(core.KeyMsg{Data: "x"})

	deadline := time.After(200 * time.Millisecond)
	for {
		select {
		case <-deadline:
			t.Fatalf("markerMsg{42} not delivered; msgs=%v", len(comp.msgs()))
		case <-time.After(5 * time.Millisecond):
		}
		found := false
		for _, m := range comp.msgs() {
			if mm, ok := m.(markerMsg); ok && mm.id == 42 {
				found = true
			}
		}
		if found {
			break
		}
	}
}

// --- TUI.Every periodicity test -------------------------------------------

// TestTUIEveryFiresRepeatedly verifies that TUI.Every delivers multiple Msgs
// over time, proving it is genuinely periodic (the old core.Every only fired
// once). Also verifies it stops when the TUI stops.
func TestTUIEveryFiresRepeatedly(t *testing.T) {
	comp := &recordingComponent{}
	tui := newTUIWith(comp)
	if err := tui.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	var ticks int32
	tui.Every(20*time.Millisecond, func(time.Time) core.Msg {
		return TickMarkerMsg{N: atomic.AddInt32(&ticks, 1)}
	})

	// Wait for at least 3 ticks — would be impossible if Every only fired once.
	deadline := time.After(300 * time.Millisecond)
	for {
		select {
		case <-deadline:
			t.Fatalf("only %d ticks, expected >=3", atomic.LoadInt32(&ticks))
		case <-time.After(10 * time.Millisecond):
		}
		if atomic.LoadInt32(&ticks) >= 3 {
			break
		}
	}

	// Stop the TUI and verify no more ticks arrive.
	tui.Stop()
	countAtStop := atomic.LoadInt32(&ticks)
	time.Sleep(80 * time.Millisecond)
	if got := atomic.LoadInt32(&ticks); got > countAtStop {
		t.Fatalf("ticks continued after Stop: %d -> %d", countAtStop, got)
	}
}

// TickMarkerMsg distinguishes TUI.Every ticks from other Msgs in the recorder.
type TickMarkerMsg struct{ N int32 }

func (TickMarkerMsg) MsgMarker() {}

// --- TUI.Tick one-shot test -----------------------------------------------

// TestTUITickFiresOnce verifies TUI.Tick delivers exactly one Msg and does
// not leak after TUI stops.
func TestTUITickFiresOnce(t *testing.T) {
	comp := &recordingComponent{}
	tui := newTUIWith(comp)
	if err := tui.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer tui.Stop()

	var fired int32
	tui.Tick(30*time.Millisecond, func(time.Time) core.Msg {
		atomic.AddInt32(&fired, 1)
		return markerMsg{7}
	})

	deadline := time.After(200 * time.Millisecond)
	for {
		select {
		case <-deadline:
			t.Fatalf("tick did not fire; fired=%d", atomic.LoadInt32(&fired))
		case <-time.After(5 * time.Millisecond):
		}
		if atomic.LoadInt32(&fired) == 1 {
			break
		}
	}

	// Wait a bit more to ensure it doesn't fire again (one-shot, not periodic).
	time.Sleep(60 * time.Millisecond)
	if got := atomic.LoadInt32(&fired); got != 1 {
		t.Fatalf("Tick fired %d times, expected 1", got)
	}
}

// --- TUI.Context cancellation on Stop test --------------------------------

// TestContextCancelledOnStop verifies that the TUI's context is cancelled
// when Stop is called, which is what lets Tick/Every/WithContext goroutines
// exit promptly instead of leaking.
func TestContextCancelledOnStop(t *testing.T) {
	tui := NewTUI(terminal.NewVirtualTerminal(80, 24), TUIOptions{})
	if err := tui.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	ctx := tui.Context()
	if err := ctx.Err(); err != nil {
		t.Fatalf("ctx should be alive before Stop, got %v", err)
	}
	if err := tui.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	select {
	case <-ctx.Done():
		// expected
	default:
		t.Fatal("ctx not cancelled after Stop")
	}
}

// TestNonFocusedComponentCmdIsExecuted verifies #4: a non-focused child's
// Update return value (Cmd) is executed, not silently dropped. Previously
// only the focused component's Cmd was dispatched, creating an inconsistent
// contract where background components couldn't emit events via Cmds.
func TestNonFocusedComponentCmdIsExecuted(t *testing.T) {
	focused := &recordingComponent{}
	background := &recordingComponent{}
	tui := NewTUI(terminal.NewVirtualTerminal(80, 24), TUIOptions{})
	tui.AddChild(focused)
	tui.AddChild(background)
	tui.Focus(focused)
	if err := tui.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer tui.Stop()

	// The background (non-focused) component returns a Cmd that emits a
	// marker. Under the old contract this was dropped; now it must fire.
	background.setNextCmd(func() core.Msg {
		return markerMsg{77}
	})
	tui.SendMsg(core.KeyMsg{Data: "x"})

	deadline := time.After(200 * time.Millisecond)
	for {
		select {
		case <-deadline:
			t.Fatalf("background Cmd's markerMsg{77} not delivered; msgs=%v", len(background.msgs()))
		case <-time.After(5 * time.Millisecond):
		}
		found := false
		for _, m := range background.msgs() {
			if mm, ok := m.(markerMsg); ok && mm.id == 77 {
				found = true
			}
		}
		if found {
			break
		}
	}
}

// TestSequenceMessageSkipsLeadingNil verifies #4: an externally-built
// SequenceMessage containing a leading nil Cmd is handled gracefully — the
// nil is skipped (mirroring BatchMsg's guard) instead of panicking through
// the recover path. Subsequent real Cmds still execute in order.
func TestSequenceMessageSkipsLeadingNil(t *testing.T) {
	comp := &recordingComponent{}
	tui := NewTUI(terminal.NewVirtualTerminal(80, 24), TUIOptions{})
	tui.AddChild(comp)
	tui.Focus(comp)
	if err := tui.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer tui.Stop()

	// Sequence with a leading nil, then a real Cmd that emits a marker.
	// core.Sequence would filter the nil, but a directly-built
	// SequenceMessage bypasses that filter — this test exercises the
	// defensive guard added to processMsg.
	tui.SendMsg(core.SequenceMessage{
		nil,
		func() core.Msg { return markerMsg{123} },
	})

	deadline := time.After(200 * time.Millisecond)
	for {
		got := false
		for _, m := range comp.msgs() {
			if mm, ok := m.(markerMsg); ok && mm.id == 123 {
				got = true
			}
		}
		if got {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("markerMsg{123} from Sequence after leading nil not delivered")
		case <-time.After(5 * time.Millisecond):
		}
	}
}
