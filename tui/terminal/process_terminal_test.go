package terminal

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestReadLoopStopsWhenPrivateInputCloses(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	readFile, err := duplicateInput(reader)
	if err != nil {
		t.Fatal(err)
	}
	terminal := &ProcessTerminal{
		readFile: readFile,
		stopRead: make(chan struct{}),
		readDone: make(chan struct{}),
	}
	go terminal.readLoop()
	close(terminal.stopRead)
	_ = cancelRead(readFile)
	_ = readFile.Close()
	select {
	case <-terminal.readDone:
	case <-time.After(time.Second):
		t.Fatal("read loop did not stop after closing private input")
	}
}

// TestReadLoopSwallowsLateProbeReplies is the regression test for the
// ">|ghostty 1.3.1" garbage in the composer: a terminal answers the DA and
// XTVERSION queries on separate reads, so the DCS payload lands after the
// probe "completed". The lax probe must swallow it instead of forwarding it
// to the editor as fake keystrokes.
func TestReadLoopSwallowsLateProbeReplies(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	readFile, err := duplicateInput(reader)
	if err != nil {
		t.Fatal(err)
	}
	probe := newProbeState(time.Now().Add(-time.Second)) // expired
	probe.lax = true                                     // already finalized

	var mu sync.Mutex
	var got []byte
	pt := &ProcessTerminal{
		readFile: readFile,
		stopRead: make(chan struct{}),
		readDone: make(chan struct{}),
		probe:    probe,
		onInput: func(b []byte) {
			mu.Lock()
			got = append(got, b...)
			mu.Unlock()
		},
	}
	go pt.readLoop()
	t.Cleanup(func() {
		close(pt.stopRead)
		_ = cancelRead(readFile)
		_ = readFile.Close()
		<-pt.readDone
	})

	// ghostty's reply stream: DA1 reply, then a late, separately-delivered
	// XTVERSION DCS carrying the "|ghostty 1.3.1" payload, then user text.
	_, _ = writer.Write([]byte("\x1b[?1;2c\x1bP>|ghostty 1.3.1\x1b\\"))
	_, _ = writer.Write([]byte("abc"))

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		seen := string(got)
		mu.Unlock()
		if strings.Contains(seen, "abc") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if got := string(got); got != "abc" {
		t.Fatalf("editor received %q, want only \"abc\" (probe replies must be swallowed)", got)
	}
}

func TestUpdateSizeReportsOnlyChanges(t *testing.T) {
	terminal := &ProcessTerminal{}
	if !terminal.updateSize(120, 40) {
		t.Fatal("first size should be reported as changed")
	}
	if terminal.updateSize(120, 40) {
		t.Fatal("identical size should not trigger resize")
	}
	if !terminal.updateSize(121, 40) {
		t.Fatal("changed columns should trigger resize")
	}
}
