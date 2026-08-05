package terminal

import (
	"os"
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
