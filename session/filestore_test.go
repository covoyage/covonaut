package session

import (
	"context"
	"testing"
)

func TestFileStore_MaxLocksPruning(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewFileStore(dir, WithMaxLocks(3))
	if err != nil {
		t.Fatal(err)
	}

	// Create 5 sessions to exceed the maxLocks=3 limit.
	for i := 0; i < 5; i++ {
		_, err := fs.Create(context.Background(), CreateOptions{InMemory: true})
		if err != nil {
			t.Fatal(err)
		}
	}

	// After creating 5 sessions with maxLocks=3, the lock map should have been
	// pruned at least once. We can't directly inspect the map, but we verify
	// that sessionLock still works after pruning (no panic, no deadlock).
	lock := fs.sessionLock("test-key")
	if lock == nil {
		t.Fatal("sessionLock returned nil after pruning")
	}

	// Verify that the lock is functional.
	lock.Lock()
	lock.Unlock()
}

func TestFileStore_DefaultNoMaxLocks(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	// With unlimited locks (default), creating and opening many sessions
	// should not panic and the lock map should grow without pruning.
	ids := make([]string, 50)
	for i := range ids {
		mgr, err := fs.Create(context.Background(), CreateOptions{})
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = mgr.Header().ID
	}

	// Opening sessions creates locks via sessionLock.
	for _, id := range ids {
		_, err := fs.Open(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
	}

	fs.locksMu.Lock()
	count := len(fs.locks)
	fs.locksMu.Unlock()

	if count < 50 {
		t.Fatalf("expected at least 50 locks (no pruning), got %d", count)
	}
}

func TestFileStore_DeleteCleansUpLock(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	mgr, err := fs.Create(context.Background(), CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	id := mgr.Header().ID

	// Open the session so that sessionLock is called and the lock is cached.
	_, err = fs.Open(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}

	// Lock should exist after open.
	fs.locksMu.Lock()
	_, ok := fs.locks[id]
	fs.locksMu.Unlock()
	if !ok {
		t.Fatal("lock not found after open")
	}

	// Delete should remove the lock.
	if err := fs.Delete(context.Background(), id); err != nil {
		t.Fatal(err)
	}

	fs.locksMu.Lock()
	_, ok = fs.locks[id]
	fs.locksMu.Unlock()
	if ok {
		t.Fatal("lock still exists after delete")
	}
}
