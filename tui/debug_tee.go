package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// ---------------------------------------------------------------------------
// Debug frame tee — COVO_TUI_DEBUG=1 records every frame the renderer emits:
//
//	=== frame <n> mode=full|diff rows=<len(rows)> termRows=<h> alerts=[...] len=<bytes>
//	<raw ANSI byte stream of the frame>
//
// Alerts flag the two constructs that can desync a differential renderer
// from the real terminal: a frame taller than the terminal (would scroll)
// and a differential update addressed past the last row. Reproduce the
// artifact, then inspect the tail of the file for the frame that introduced
// it and the alerts around that moment.
// ---------------------------------------------------------------------------

var (
	teeFile   *os.File
	teeFrame  atomic.Int64
	teeActive bool
)

func teeInit() {
	if os.Getenv("COVO_TUI_DEBUG") == "" {
		return
	}
	dir := os.Getenv("COVO_TUI_DEBUG_DIR")
	if dir == "" {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return
		}
		dir = filepath.Join(home, ".covo-agent", "tui-debug")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}
	f, err := os.Create(filepath.Join(dir, fmt.Sprintf("covo-tui-%d.log", os.Getpid())))
	if err != nil {
		return
	}
	teeFile = f
	teeActive = true
}

// teeFrame records one frame's metadata and raw byte stream.
func (t *TUI) teeFrame(mode string, rows int, termRows int64, diffs int, maxDiffRow int64, buf []byte) {
	if !teeActive {
		return
	}
	var alerts []string
	if int64(rows) > termRows {
		alerts = append(alerts, fmt.Sprintf("FRAME_TALLER_THAN_TERMINAL rows=%d termRows=%d", rows, termRows))
	}
	if mode == "diff" && maxDiffRow > termRows {
		alerts = append(alerts, fmt.Sprintf("DIFF_ROW_PAST_END row=%d termRows=%d", maxDiffRow, termRows))
	}
	alertStr := "-"
	if len(alerts) > 0 {
		alertStr = "[" + strings.Join(alerts, "; ") + "]"
	}
	n := teeFrame.Add(1)
	var b strings.Builder
	fmt.Fprintf(&b, "=== frame %d mode=%s rows=%d termRows=%d diffs=%d alerts=%s len=%d\n",
		n, mode, rows, termRows, diffs, alertStr, len(buf))
	if teeFile != nil {
		_, _ = teeFile.WriteString(b.String())
		_, _ = teeFile.Write(buf)
		_, _ = teeFile.WriteString("\n")
	}
	if len(alerts) > 0 {
		_, _ = fmt.Fprintf(os.Stderr, "covo-tui-debug: %s\n", alertStr)
	}
}

func teeClose() {
	if teeFile != nil {
		_ = teeFile.Close()
		teeFile = nil
	}
	teeActive = false
}

// ---------------------------------------------------------------------------
// DSR probe — in debug mode, periodically park the cursor at home and ask
// the terminal to report its real cursor position (ESC[6n). A reply of
// row != 1 proves the real screen scrolled/shifted by (row-1) rows relative
// to the model — quantifying the desync exactly, with a timestamp.
// ---------------------------------------------------------------------------

var dsrReplyRe = regexp.MustCompile(`\x1b\[(\d+);(\d+)R`)

const dsrProbeInterval = 2 * time.Second

var lastDSRProbe time.Time

// maybeProbeCursor emits a home-position + DSR query when the probe interval
// has elapsed. Called from the event loop between frames.
func (t *TUI) maybeProbeCursor() {
	if !teeActive {
		return
	}
	if now := time.Now(); now.Sub(lastDSRProbe) >= dsrProbeInterval {
		lastDSRProbe = now
		_, _ = t.term.Write([]byte("\x1b[1;1H\x1b[6n"))
	}
}

// interceptDSRReply scans raw input for a DSR cursor-position reply, logs
// the real-vs-expected offset, and reports whether the bytes were consumed.
func interceptDSRReply(data []byte) (consumed []byte, handled bool) {
	if !teeActive {
		return data, false
	}
	if m := dsrReplyRe.FindIndex(data); m != nil && m[0] == 0 {
		row, _ := strconv.Atoi(strings.Split(string(data[m[0]:m[1]]), ";")[0][2:])
		col := strings.Split(string(data[m[0]:m[1]]), ";")[1]
		col = strings.TrimRight(col, "R")
		entry := fmt.Sprintf("=== DSR_REPLY t=%s real_row=%d real_col=%s expected_row=1\n",
			time.Now().Format("15:04:05.000"), row, col)
		if row != 1 {
			entry += fmt.Sprintf("!!! SCREEN_SHIFTED rows=%d — terminal grid diverged from model\n", row-1)
		}
		if teeFile != nil {
			_, _ = teeFile.WriteString(entry)
		}
		_, _ = fmt.Fprint(os.Stderr, "covo-tui-debug: "+entry)
		rest := data[m[1]:]
		return rest, true
	}
	return data, false
}
