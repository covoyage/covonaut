package core

import (
	"context"
	"time"
)

// Msg is the marker interface for all messages in the Elm-style event loop.
// External packages can implement Msg by embedding MsgBase or by defining
// a MsgMarker() method on their types.
type Msg interface{ MsgMarker() }

// MsgBase is a zero-value struct that external packages can embed to
// satisfy the Msg interface without writing their own method.
type MsgBase struct{}

func (MsgBase) MsgMarker() {}

type KeyMsg struct {
	Data string
}

func (KeyMsg) MsgMarker() {}

type PasteMsg struct {
	Text string
}

func (PasteMsg) MsgMarker() {}

type WindowSizeMsg struct {
	Width  int64
	Height int64
}

func (WindowSizeMsg) MsgMarker() {}

type TickMsg struct {
	Time time.Time
}

func (TickMsg) MsgMarker() {}

type QuitMsg struct{}

func (QuitMsg) MsgMarker() {}

// PanicMsg is emitted when a Cmd panics during execution. The event loop
// receives this instead of silently losing the Cmd's output, so the
// application can surface the failure (log it, show an error banner, etc.).
type PanicMsg struct {
	Err      interface{} // the recover() value
	Stack    string      // captured stack trace
	CmdIndex int         // meaningful only when emitted from a Batch/Sequence
}

func (PanicMsg) MsgMarker() {}

type MouseAction int64

const (
	MousePress MouseAction = iota
	MouseRelease
	MouseWheelUp
	MouseWheelDown
	MouseMotion
)

type MouseMsg struct {
	Action MouseAction
	Row    int64
	Col    int64
	Button int64
	Alt    bool
	Ctrl   bool
	Shift  bool
}

func (MouseMsg) MsgMarker() {}

type BatchMsg []Cmd

func (BatchMsg) MsgMarker() {}

type SequenceMessage []Cmd

func (SequenceMessage) MsgMarker() {}

type Cmd func() Msg

func Batch(cmds ...Cmd) Cmd {
	nonNil := make([]Cmd, 0, len(cmds))
	for _, c := range cmds {
		if c != nil {
			nonNil = append(nonNil, c)
		}
	}
	if len(nonNil) == 0 {
		return nil
	}
	if len(nonNil) == 1 {
		return nonNil[0]
	}
	return func() Msg {
		return BatchMsg(nonNil)
	}
}

func Sequence(cmds ...Cmd) Cmd {
	nonNil := make([]Cmd, 0, len(cmds))
	for _, c := range cmds {
		if c != nil {
			nonNil = append(nonNil, c)
		}
	}
	if len(nonNil) == 0 {
		return nil
	}
	if len(nonNil) == 1 {
		return nonNil[0]
	}
	return func() Msg {
		return SequenceMessage(nonNil)
	}
}

// Tick returns a Cmd that fires once after duration d. The timer is created
// when the Cmd runs (not when Tick is called), so the delay is measured from
// execution, not construction — avoiding drift when the Cmd is queued (e.g.
// inside Sequence) and avoiding timer leaks if the Cmd never runs.
func Tick(d time.Duration, fn func(time.Time) Msg) Cmd {
	return func() Msg {
		t := time.NewTimer(d)
		defer t.Stop()
		ts := <-t.C
		return fn(ts)
	}
}

// WithContext wraps cmd so that its result is discarded if ctx is cancelled
// before the Cmd completes. The Cmd itself runs in a goroutine; cancellation
// during a long-running Cmd cannot be forced by the framework (Go has no
// goroutine kill), so the Cmd SHOULD select on ctx.Done() itself for prompt
// cancellation. WithContext guarantees that no Msg is delivered after ctx is
// done, which prevents stale results from reaching the event loop.
func WithContext(ctx context.Context, cmd Cmd) Cmd {
	if cmd == nil {
		return nil
	}
	return func() Msg {
		if err := ctx.Err(); err != nil {
			return nil
		}
		done := make(chan Msg, 1)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					done <- PanicMsg{Err: r, Stack: ""}
				}
			}()
			done <- cmd()
		}()
		select {
		case <-ctx.Done():
			return nil
		case m := <-done:
			if m == nil {
				return nil
			}
			return CtxMessage{ctx: ctx, inner: m}
		}
	}
}

func Quit() Msg {
	return QuitMsg{}
}

type CtxMessage struct {
	ctx   context.Context
	inner Msg
}

func (CtxMessage) MsgMarker() {}

func (m CtxMessage) Inner() Msg { return m.inner }
