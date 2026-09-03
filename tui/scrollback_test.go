package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/covoyage/covonaut/tui/terminal"
)

// waitOutput polls until a predicate matches the terminal output or fails.
func waitOutput(t *testing.T, vt *terminal.VirtualTerminal, pred func(string) bool) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		out := vt.OutputString()
		if pred(out) {
			return out
		}
		time.Sleep(15 * time.Millisecond)
	}
	return vt.OutputString()
}

func TestScrollbackRendersIntoBottomRegion(t *testing.T) {
	// A 24-row terminal; the frame is 5 rows tall, so the scroll region must
	// be rows [20,24] (1-based) and the frame must be drawn there.
	vt := terminal.NewVirtualTerminal(80, 24)
	tui := NewTUI(vt, TUIOptions{Scrollback: true})
	tui.AddChild(&lineComp{lines: []string{"r0", "r1", "r2", "r3", "r4"}})
	if err := tui.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tui.Stop() })

	out := waitOutput(t, vt, func(o string) bool {
		return strings.Contains(o, "r0") && strings.Contains(o, "\x1b[20;24r")
	})
	// The frame must establish the bottom scroll region, hide the cursor,
	// home to the region top, and repaint the whole region.
	if !strings.Contains(out, "\x1b[20;24r") || !strings.Contains(out, "\x1b[20;1H") {
		t.Fatalf("scrollback frame missing bottom scroll region / cursor home:\n%s", out)
	}
	for _, want := range []string{"r0", "r1", "r2", "r3", "r4"} {
		if !strings.Contains(out, want) {
			t.Fatalf("frame row %q missing from scrollback render:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "\x1b[r") {
		t.Fatalf("frame must restore full scroll region with \\x1b[r:\n%s", out)
	}
}

func TestScrollbackWriteNativePushesAboveRegion(t *testing.T) {
	vt := terminal.NewVirtualTerminal(80, 24)
	tui := NewTUI(vt, TUIOptions{Scrollback: true})
	tui.AddChild(&lineComp{lines: []string{"live0", "live1"}})
	if err := tui.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tui.Stop() })
	// Wait for the first frame so liveRows is known (bottom 2 rows → 23).
	waitOutput(t, vt, func(o string) bool { return strings.Contains(o, "live0") })
	vt.ResetOutput()

	tui.WriteNativeScrollback([]string{"scrollback line"})
	out := waitOutput(t, vt, func(o string) bool { return strings.Contains(o, "scrollback line") })

	if !strings.Contains(out, "\x1b[23;1H") {
		t.Fatalf("scrollback write must home to region top (row 23):\n%s", out)
	}
	if !strings.Contains(out, "scrollback line") {
		t.Fatalf("scrollback content missing from output:\n%s", out)
	}
}

func TestScrollbackWriteDisabledWithoutOption(t *testing.T) {
	vt := terminal.NewVirtualTerminal(80, 24)
	tui := NewTUI(vt, TUIOptions{}) // no scrollback
	tui.AddChild(&lineComp{lines: []string{"x"}})
	if err := tui.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tui.Stop() })
	waitOutput(t, vt, func(o string) bool { return strings.Contains(o, "x") })
	vt.ResetOutput()

	tui.WriteNativeScrollback([]string{"should not appear"})
	time.Sleep(50 * time.Millisecond)
	if got := vt.OutputString(); strings.Contains(got, "should not appear") {
		t.Fatalf("scrollback write must be a no-op when scrollback mode is off:\n%s", got)
	}
}
