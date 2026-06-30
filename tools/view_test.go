package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestViewTool(t *testing.T) {
	// Create temp directory structure.
	tmpDir := t.TempDir()
	os.MkdirAll(filepath.Join(tmpDir, "src", "utils"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "src", "main.go"), []byte("package main"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "src", "utils", "helper.go"), []byte("package utils"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "README.md"), []byte("# Test"), 0644)

	tool := NewViewTool(tmpDir, nil)

	args, _ := json.Marshal(map[string]any{"path": tmpDir})
	result, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("view failed: %v", err)
	}

	r := result.(ToolResult)
	if !strings.Contains(r.Content, "src/") {
		t.Errorf("expected src/ in output, got: %s", r.Content)
	}
	if !strings.Contains(r.Content, "README.md") {
		t.Errorf("expected README.md in output, got: %s", r.Content)
	}
	if !strings.Contains(r.Content, "├──") && !strings.Contains(r.Content, "└──") {
		t.Errorf("expected tree connectors in output, got: %s", r.Content)
	}
}

func TestViewToolFile(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte("hello"), 0644)

	tool := NewViewTool(tmpDir, nil)

	args, _ := json.Marshal(map[string]any{"path": "test.txt"})
	result, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("view failed: %v", err)
	}

	r := result.(ToolResult)
	if !strings.Contains(r.Content, "test.txt") {
		t.Errorf("expected filename in output, got: %s", r.Content)
	}
	if !strings.Contains(r.Content, "5 bytes") {
		t.Errorf("expected size in output, got: %s", r.Content)
	}
}

func TestViewToolMaxEntries(t *testing.T) {
	tmpDir := t.TempDir()
	for i := 0; i < 10; i++ {
		os.WriteFile(filepath.Join(tmpDir, fmt.Sprintf("file%d.txt", i)), []byte("x"), 0644)
	}

	tool := NewViewTool(tmpDir, &ViewToolConfig{MaxEntries: 5})

	args, _ := json.Marshal(map[string]any{})
	result, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("view failed: %v", err)
	}

	r := result.(ToolResult)
	if !strings.Contains(r.Content, "entries limit reached") {
		t.Errorf("expected limit notice, got: %s", r.Content)
	}
}
