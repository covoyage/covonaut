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

func TestGlobTool(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "a.go"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "b.go"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "a.ts"), []byte("x"), 0644)

	tool := NewGlobTool(tmpDir, nil)

	args, _ := json.Marshal(map[string]any{"pattern": "*.go"})
	result, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("glob failed: %v", err)
	}

	r := result.(ToolResult)
	lines := strings.Split(strings.TrimSpace(r.Content), "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 .go files, got %d: %s", len(lines), r.Content)
	}
}

func TestGlobToolNoMatch(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "a.txt"), []byte("x"), 0644)

	tool := NewGlobTool(tmpDir, nil)

	args, _ := json.Marshal(map[string]any{"pattern": "*.go"})
	result, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("glob failed: %v", err)
	}

	r := result.(ToolResult)
	if !strings.Contains(r.Content, "No files found") {
		t.Errorf("expected no match message, got: %s", r.Content)
	}
}

func TestGlobToolLimit(t *testing.T) {
	tmpDir := t.TempDir()
	for i := 0; i < 10; i++ {
		os.WriteFile(filepath.Join(tmpDir, fmt.Sprintf("file%d.go", i)), []byte("x"), 0644)
	}

	tool := NewGlobTool(tmpDir, &GlobToolConfig{Limit: 5})

	args, _ := json.Marshal(map[string]any{"pattern": "*.go"})
	result, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("glob failed: %v", err)
	}

	r := result.(ToolResult)
	if !strings.Contains(r.Content, "results limit reached") {
		t.Errorf("expected limit notice, got: %s", r.Content)
	}
}
