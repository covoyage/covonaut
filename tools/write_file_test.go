package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFileTool(t *testing.T) {
	tmpDir := t.TempDir()
	tool := NewWriteFileTool(tmpDir, nil)

	// Test creating a new file.
	args, _ := json.Marshal(map[string]string{
		"path":    "test.txt",
		"content": "hello world",
	})
	result, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tr := result.(ToolResult)
	if !strings.Contains(tr.Content, "Created") {
		t.Errorf("expected 'Created' in result, got: %s", tr.Content)
	}
	if !strings.Contains(tr.Content, "11 bytes") {
		t.Errorf("expected '11 bytes' in result, got: %s", tr.Content)
	}

	// Verify file content.
	data, _ := os.ReadFile(filepath.Join(tmpDir, "test.txt"))
	if string(data) != "hello world" {
		t.Errorf("expected 'hello world', got: %s", string(data))
	}
}

func TestWriteFileToolOverwrite(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "existing.txt"), []byte("old"), 0644)

	tool := NewWriteFileTool(tmpDir, nil)
	args, _ := json.Marshal(map[string]string{
		"path":    "existing.txt",
		"content": "new content",
	})
	result, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tr := result.(ToolResult)
	if !strings.Contains(tr.Content, "Overwrote") {
		t.Errorf("expected 'Overwrote' in result, got: %s", tr.Content)
	}

	data, _ := os.ReadFile(filepath.Join(tmpDir, "existing.txt"))
	if string(data) != "new content" {
		t.Errorf("expected 'new content', got: %s", string(data))
	}
}

func TestWriteFileToolCreateDirs(t *testing.T) {
	tmpDir := t.TempDir()
	tool := NewWriteFileTool(tmpDir, nil)
	args, _ := json.Marshal(map[string]string{
		"path":    "subdir/nested/file.txt",
		"content": "nested content",
	})
	_, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(tmpDir, "subdir/nested/file.txt"))
	if string(data) != "nested content" {
		t.Errorf("expected 'nested content', got: %s", string(data))
	}
}

func TestWriteFileToolJSONSyntax(t *testing.T) {
	tmpDir := t.TempDir()
	tool := NewWriteFileTool(tmpDir, nil)

	// Valid JSON.
	args, _ := json.Marshal(map[string]string{
		"path":    "valid.json",
		"content": `{"key": "value", "num": 123}`,
	})
	result, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tr := result.(ToolResult)
	if !strings.Contains(tr.Content, "JSON syntax ok") {
		t.Errorf("expected 'JSON syntax ok', got: %s", tr.Content)
	}

	// Invalid JSON.
	args, _ = json.Marshal(map[string]string{
		"path":    "invalid.json",
		"content": `{"key": "value", "num": }`,
	})
	result, err = tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tr = result.(ToolResult)
	if !strings.Contains(tr.Content, "JSON syntax error") {
		t.Errorf("expected 'JSON syntax error', got: %s", tr.Content)
	}
}

func TestWriteFileToolSizeLimit(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &WriteFileToolConfig{MaxBytes: 10}
	tool := NewWriteFileTool(tmpDir, cfg)
	args, _ := json.Marshal(map[string]string{
		"path":    "large.txt",
		"content": "this content is definitely more than 10 bytes",
	})
	_, err := tool.Func(context.Background(), args)
	if err == nil {
		t.Fatal("expected error for oversized content")
	}
	if !strings.Contains(err.Error(), "exceeds maximum size") {
		t.Errorf("expected size limit error, got: %v", err)
	}
}
