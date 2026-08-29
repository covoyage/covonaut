package tui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/covoyage/covonaut/tui/chat"
	"github.com/covoyage/covonaut/tui/terminal"
)

var ansiSeqRe = regexp.MustCompile("\x1b\\[[0-9;?]*[a-zA-Z]")

// screenRows rebuilds a logical screen (1-based row -> printable text) from
// the raw byte stream the TUI wrote to the terminal. It understands the two
// constructs the renderer emits: full-repaint rows separated by \r\n and
// differential updates addressed with "\x1b[<row>;1H" plus "\x1b[2K" erases.
func screenRows(t *testing.T, out []byte) map[int]string {
	t.Helper()
	rows := map[int]string{}
	row := 1
	i := 0
	for i < len(out) {
		b := out[i]
		if b == 0x1b {
			rest := string(out[i:])
			if strings.HasPrefix(rest, "\x1b[H") {
				row = 1
				i += 3
				continue
			}
			if m := regexp.MustCompile(`^\x1b\[(\d+);1H`).FindStringSubmatch(rest); m != nil {
				n, _ := strconv.Atoi(m[1])
				row = n
				i += len(m[0])
				continue
			}
			if strings.HasPrefix(rest, "\x1b[2K") {
				rows[row] = ""
				i += 4
				continue
			}
			if m := ansiSeqRe.FindStringIndex(rest); m != nil && m[0] == 0 {
				i += m[1]
				continue
			}
			i++
			continue
		}
		if b == '\r' {
			i++
			continue
		}
		if b == '\n' {
			row++
			i++
			continue
		}
		// Consume one printable UTF-8 rune.
		size := 1
		switch {
		case b >= 0xF0:
			size = 4
		case b >= 0xE0:
			size = 3
		case b >= 0xC0:
			size = 2
		}
		if i+size > len(out) {
			size = 1
		}
		rows[row] += string(out[i : i+size])
		i += size
	}
	return rows
}

// waitForOutput polls until the predicate matches the screen or times out.
func waitForScreen(t *testing.T, vt *terminal.VirtualTerminal, pred func(map[int]string) bool) map[int]string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var rows map[int]string
	for time.Now().Before(deadline) {
		rows = screenRows(t, vt.Output())
		if pred(rows) {
			return rows
		}
		time.Sleep(25 * time.Millisecond)
	}
	return rows
}

func dumpRows(rows map[int]string) string {
	max := 0
	for r := range rows {
		if r > max {
			max = r
		}
	}
	var b strings.Builder
	for r := 1; r <= max; r++ {
		if txt, ok := rows[r]; ok {
			fmt.Fprintf(&b, "%2d|%s\n", r, txt)
		}
	}
	return b.String()
}

// TestGhostTextStaysInEditorRow drives a real TUI render loop and asserts
// the inline ghost completion renders on the editor content row — never on
// the editor border row or the footer.
func TestGhostTextStaysInEditorRow(t *testing.T) {
	vt := terminal.NewVirtualTerminal(100, 24)
	app := NewTUI(vt, TUIOptions{})
	host := &tuiAppHost{TUI: app}
	chatApp := chat.NewChatAppWithHost(chat.ChatAppConfig{}, host)
	chatApp.SetHost(host)
	if err := chatApp.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = chatApp.Stop() }()

	const ghost = "find /Users/ning/projects/ai/grok-build -type f | head -50"
	chatApp.Editor().SetValue("# 分析项目结构和规模")
	chatApp.Editor().SetGhost(ghost)
	chatApp.Busy("继续中 ...")

	rows := waitForScreen(t, vt, func(rows map[int]string) bool {
		for _, txt := range rows {
			if strings.Contains(txt, "grok-build") {
				return true
			}
		}
		return false
	})

	valueRow, ghostRow, borderRows := -1, -1, map[int]bool{}
	for r, txt := range rows {
		if strings.Contains(txt, "分析项目结构") {
			valueRow = r
		}
		if strings.Contains(txt, "grok-build") {
			ghostRow = r
		}
		if strings.Count(txt, "─") > 10 {
			borderRows[r] = true
		}
	}
	t.Logf("valueRow=%d ghostRow=%d borders=%v\n%s", valueRow, ghostRow, borderKeys(borderRows), dumpRows(rows))

	if ghostRow == -1 {
		t.Fatalf("ghost text never rendered\n%s", dumpRows(rows))
	}
	if borderRows[ghostRow] {
		t.Fatalf("ghost text rendered on border row %d\n%s", ghostRow, dumpRows(rows))
	}
	if valueRow != -1 && ghostRow != valueRow && !borderRows[ghostRow] {
		// Ghost should share the value row (inline after cursor).
		t.Fatalf("ghost rendered on row %d, editor value on row %d\n%s", ghostRow, valueRow, dumpRows(rows))
	}

	// Idle frame: loader row disappears, editor shifts up one row; the
	// differential repaint must move the ghost cleanly with it.
	chatApp.Idle()
	time.Sleep(300 * time.Millisecond)
	rows = screenRows(t, vt.Output())
	valueRow, ghostRow = -1, -1
	borderRows = map[int]bool{}
	for r, txt := range rows {
		if strings.Contains(txt, "分析项目结构") {
			valueRow = r
		}
		if strings.Contains(txt, "grok-build") {
			ghostRow = r
		}
		if strings.Count(txt, "─") > 10 {
			borderRows[r] = true
		}
	}
	if ghostRow != -1 && borderRows[ghostRow] {
		t.Fatalf("idle frame: ghost rendered on border row %d\n%s", ghostRow, dumpRows(rows))
	}
	if ghostRow != -1 && ghostRow != valueRow {
		t.Fatalf("idle frame: ghost on row %d, value on row %d\n%s", ghostRow, valueRow, dumpRows(rows))
	}
}

func borderKeys(m map[int]bool) []int {
	out := []int{}
	for k := range m {
		out = append(out, k)
	}
	return out
}
