package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
)

func TestFileStoreSaveLoad(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	ctx := context.Background()
	snap := agentcore.StateSnapshot{
		Messages: []agentcore.Message{{Role: "user", Content: "hello"}},
	}

	if err := fs.Save(ctx, "test-key", snap); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := fs.Load(ctx, "test-key")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(loaded.Messages) != 1 || loaded.Messages[0].Content != "hello" {
		t.Errorf("Load: got %+v, want hello message", loaded)
	}

	// Verify file exists on disk
	path := filepath.Join(dir, "test-key.json")
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file not on disk: %v", err)
	}
}

func TestFileStoreDelete(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	ctx := context.Background()
	snap := agentcore.StateSnapshot{}
	_ = fs.Save(ctx, "del-key", snap)

	if err := fs.Delete(ctx, "del-key"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := fs.Load(ctx, "del-key"); err == nil {
		t.Error("expected error loading deleted key")
	}

	// Delete non-existent should not error
	if err := fs.Delete(ctx, "nonexistent"); err != nil {
		t.Errorf("Delete nonexistent: %v", err)
	}
}

func TestFileStoreHas(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	ctx := context.Background()

	exists, err := fs.Has(ctx, "missing")
	if err != nil {
		t.Fatalf("Has missing: %v", err)
	}
	if exists {
		t.Error("Has missing: should be false")
	}

	_ = fs.Save(ctx, "present", agentcore.StateSnapshot{})
	exists, err = fs.Has(ctx, "present")
	if err != nil {
		t.Fatalf("Has present: %v", err)
	}
	if !exists {
		t.Error("Has present: should be true")
	}
}

func TestFileStoreList(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	ctx := context.Background()
	_ = fs.Save(ctx, "alpha", agentcore.StateSnapshot{})
	_ = fs.Save(ctx, "beta", agentcore.StateSnapshot{})

	keys, err := fs.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(keys) != 2 {
		t.Errorf("List: got %d keys, want 2: %v", len(keys), keys)
	}
}

func TestFileStoreNewCreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "store")
	fs, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("directory not created: %v", err)
	}
	_ = fs
}
