package tui

import (
	"testing"

	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/terminal"
)

// Input-priority lane routing: key/paste/mouse go to inputCh, everything else
// to msgCh, regardless of which sender path delivers them.
func TestInputPriorityRouting(t *testing.T) {
	tui := NewTUI(terminal.NewVirtualTerminal(80, 24))

	tui.sendMsgSafe(core.KeyMsg{Data: "a"})
	tui.sendMsgSafe(core.PasteMsg{Text: "x"})
	tui.sendMsgSafe(core.MouseMsg{Action: core.MousePress, Row: 1, Col: 1})
	tui.sendMsgSafe(core.TickMsg{}) // background lane

	// The three input events are queued in FIFO order on the input lane.
	want := 3
	got := 0
	for {
		select {
		case m := <-tui.inputCh:
			switch m.(type) {
			case core.KeyMsg, core.PasteMsg, core.MouseMsg:
				got++
			default:
				t.Fatalf("non-input msg %T on input lane", m)
			}
		default:
			if got != want {
				t.Fatalf("input lane had %d, want %d", got, want)
			}
			return
		}
	}
}

// Background msgs must not end up on the input lane either.
func TestInputPriorityBackgroundLane(t *testing.T) {
	tui := NewTUI(terminal.NewVirtualTerminal(80, 24))

	tui.sendMsgSafe(core.PasteMsg{Text: "z"})
	tui.sendMsgSafe(core.TickMsg{})
	tui.sendMsgSafe(core.KeyMsg{Data: "b"})
	tui.sendMsgSafe(core.PanicMsg{})

	got := 0
	for {
		select {
		case m := <-tui.msgCh:
			switch m.(type) {
			case core.TickMsg, core.PanicMsg:
				got++
			default:
				t.Fatalf("unexpected %T on background lane", m)
			}
		default:
			if got != 2 {
				t.Fatalf("background lane had %d, want 2", got)
			}
			return
		}
	}
}

// The event loop must still process input sent via the background sender path
// (execCmd results / SendMsg) — routing is by type, not by source.
func TestInputPriorityEventLoopProcessesTypedInput(t *testing.T) {
	tui := NewTUI(terminal.NewVirtualTerminal(80, 24))
	handled := make(chan struct{}, 1)
	catcher := &msgCatcher{t: t, handled: handled}
	tui.AddChild(catcher)
	tui.Focus(catcher)
	if err := tui.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer tui.Stop()

	tui.SendMsg(core.KeyMsg{Data: "x"})
	select {
	case <-handled:
	case <-tui.Done():
		t.Fatal("TUI stopped before handling")
	}
}

type msgCatcher struct {
	t       *testing.T
	handled chan struct{}
}

func (c *msgCatcher) Render(w int64) []string { return nil }
func (c *msgCatcher) Invalidate()             {}
func (c *msgCatcher) Update(msg core.Msg) core.Cmd {
	if k, ok := msg.(core.KeyMsg); ok && k.Data == "x" {
		select {
		case c.handled <- struct{}{}:
		default:
		}
	}
	return nil
}
