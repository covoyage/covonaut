package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMoveToolFile(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "old.txt"), []byte("hello"), 0644)

	tool := NewMoveTool(tmpDir, nil)

	args, _ := json.Marshal(map[string]any{"source": "old.txt", "dest": "new.txt"})
	result, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("move failed: %v", err)
	}

	r := result.(ToolResult)
	if !strings.Contains(r.Content, "Moved") {
		t.Errorf("expected move confirmation, got: %s", r.Content)
	}

	if _, err := os.Stat(filepath.Join(tmpDir, "new.txt")); os.IsNotExist(err) {
		t.Errorf("destination file should exist")
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "old.txt")); !os.IsNotExist(err) {
		t.Errorf("source file should not exist")
	}
}

func TestMoveToolCreateDirs(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "file.txt"), []byte("hello"), 0644)

	tool := NewMoveTool(tmpDir, nil)

	args, _ := json.Marshal(map[string]any{"source": "file.txt", "dest": "subdir/nested/file.txt"})
	result, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("move failed: %v", err)
	}

	r := result.(ToolResult)
	if !strings.Contains(r.Content, "Moved") {
		t.Errorf("expected move confirmation, got: %s", r.Content)
	}

	if _, err := os.Stat(filepath.Join(tmpDir, "subdir", "nested", "file.txt")); os.IsNotExist(err) {
		t.Errorf("nested destination should exist")
	}
}

func TestMoveToolSourceNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	tool := NewMoveTool(tmpDir, nil)

	args, _ := json.Marshal(map[string]any{"source": "missing.txt", "dest": "new.txt"})
	_, err := tool.Func(context.Background(), args)
	if err == nil {
		t.Fatal("expected error for missing source")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected not found error, got: %v", err)
	}
}
