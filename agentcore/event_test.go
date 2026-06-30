package agentcore

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestEventBus_EmitNonBlocking(t *testing.T) {
	eb := NewEventBus()
	defer eb.Close()

	// Fill the buffer to capacity
	for i := 0; i < defaultEventBufSize; i++ {
		eb.Emit(&AgentStartEvent{baseEvent: baseEvent{Kind: EventAgentStart}})
	}

	// This emit should not block even though the buffer is full
	done := make(chan struct{})
	go func() {
		eb.Emit(&AgentStartEvent{baseEvent: baseEvent{Kind: EventAgentStart}})
		close(done)
	}()

	select {
	case <-done:
		// Success: Emit returned without blocking
	case <-time.After(time.Second):
		t.Fatal("Emit blocked when buffer was full — should have dropped the event instead")
	}
}

func TestEventBus_EmitAfterClose(t *testing.T) {
	eb := NewEventBus()
	eb.Close()

	// Emit on a closed bus must not panic
	eb.Emit(&AgentStartEvent{baseEvent: baseEvent{Kind: EventAgentStart}})
}

func TestEventBus_DrainEventuallyCompletes(t *testing.T) {
	eb := NewEventBus()
	defer eb.Close()

	var received atomic.Int32
	eb.On(EventAgentStart, func(e Event) {
		received.Add(1)
	})

	const n = 10
	for i := 0; i < n; i++ {
		eb.Emit(&AgentStartEvent{baseEvent: baseEvent{Kind: EventAgentStart}})
	}

	eb.Drain()

	if got := received.Load(); got != n {
		t.Fatalf("received %d events, want %d", got, n)
	}
}

func TestEventBus_DrainAfterClose_NoPanic(t *testing.T) {
	eb := NewEventBus()

	// Fill the buffer so Drain would need to wait
	for i := 0; i < defaultEventBufSize; i++ {
		eb.Emit(&AgentStartEvent{baseEvent: baseEvent{Kind: EventAgentStart}})
	}

	eb.Close()

	// Drain on a closed bus must not panic or spin forever
	done := make(chan struct{})
	go func() {
		eb.Drain()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(time.Second):
		t.Fatal("Drain blocked after Close — should have returned immediately")
	}
}

// TestEventBus_HandlerPanicDoesNotKillDispatch verifies #2: a panicking
// handler is recovered, logged to stderr, and does NOT take down the
// dispatch goroutine. Subsequent events must still be delivered to other
// handlers and to the same handler (if it re-registers). Without recover,
// the dispatch goroutine would exit, close(done), and silently drop every
// future event — the event bus would be permanently dead.
func TestEventBus_HandlerPanicDoesNotKillDispatch(t *testing.T) {
	eb := NewEventBus()
	defer eb.Close()

	var got atomic.Int32

	// First global handler panics.
	eb.OnAll(func(e Event) {
		panic("boom from handler")
	})
	// Second global handler must still run despite the first panicking.
	eb.OnAll(func(e Event) {
		got.Add(1)
	})

	eb.Emit(&AgentStartEvent{baseEvent: baseEvent{Kind: EventAgentStart}})
	eb.Emit(&AgentStartEvent{baseEvent: baseEvent{Kind: EventAgentStart}})
	eb.Drain()

	// Both events should have been delivered to the second handler.
	if got := got.Load(); got != 2 {
		t.Fatalf("second handler received %d events, want 2 (dispatch died on panic?)", got)
	}
}
