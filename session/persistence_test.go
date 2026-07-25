package session

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestFlushAllDoesNotTruncateBeforeLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	original := []byte("original content\n")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	manager := newManager(Header{Type: EntryHeader, Version: CurrentVersion, ID: "test"}, path, true)

	lockFile, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lockFile.Close()
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- manager.flushAll() }()
	time.Sleep(50 * time.Millisecond)
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("file changed before lock acquisition: got %q want %q", got, original)
	}

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("flushAll: %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) == string(original) {
		t.Fatalf("file was not replaced after lock release: content=%q err=%v", got, err)
	}
}
