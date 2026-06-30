package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeleteToolFile(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(testFile, []byte("hello"), 0644)

	tool := NewDeleteTool(tmpDir, nil)

	args, _ := json.Marshal(map[string]any{"path": "test.txt"})
	result, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	r := result.(ToolResult)
	if !strings.Contains(r.Content, "Deleted file") {
		t.Errorf("expected delete confirmation, got: %s", r.Content)
	}

	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		t.Errorf("file should have been deleted")
	}
}

func TestDeleteToolDirRequiresConfirm(t *testing.T) {
	tmpDir := t.TempDir()
	os.MkdirAll(filepath.Join(tmpDir, "subdir"), 0755)

	tool := NewDeleteTool(tmpDir, nil)

	args, _ := json.Marshal(map[string]any{"path": "subdir"})
	_, err := tool.Func(context.Background(), args)
	if err == nil {
		t.Fatal("expected error for directory without confirm")
	}
	if !strings.Contains(err.Error(), "confirm=true") {
		t.Errorf("expected confirm hint, got: %v", err)
	}
}

func TestDeleteToolDirWithConfirm(t *testing.T) {
	tmpDir := t.TempDir()
	subdir := filepath.Join(tmpDir, "subdir")
	os.MkdirAll(subdir, 0755)
	os.WriteFile(filepath.Join(subdir, "file.txt"), []byte("x"), 0644)

	tool := NewDeleteTool(tmpDir, nil)

	args, _ := json.Marshal(map[string]any{"path": "subdir", "confirm": true})
	result, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	r := result.(ToolResult)
	if !strings.Contains(r.Content, "Deleted directory") {
		t.Errorf("expected delete confirmation, got: %s", r.Content)
	}
}

func TestDeleteToolNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	tool := NewDeleteTool(tmpDir, nil)

	args, _ := json.Marshal(map[string]any{"path": "nonexistent.txt"})
	_, err := tool.Func(context.Background(), args)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected not found error, got: %v", err)
	}
}
