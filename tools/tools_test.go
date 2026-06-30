package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadTool(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(testFile, []byte("line1\nline2\nline3\n"), 0644)

	tool := NewReadTool(tmpDir, nil)

	// Test basic read.
	args, _ := json.Marshal(map[string]string{"path": "test.txt"})
	result, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tr := result.(ToolResult)
	if !strings.Contains(tr.Content, "line1") {
		t.Errorf("expected content to contain 'line1', got: %s", tr.Content)
	}

	// Test offset/limit.
	args, _ = json.Marshal(map[string]any{"path": "test.txt", "offset": 2, "limit": 1})
	result, err = tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tr = result.(ToolResult)
	if tr.Content != "line2" {
		t.Errorf("expected 'line2', got: %s", tr.Content)
	}
}

func TestLsTool(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "a.txt"), []byte(""), 0644)
	os.Mkdir(filepath.Join(tmpDir, "subdir"), 0755)
	os.WriteFile(filepath.Join(tmpDir, ".hidden"), []byte(""), 0644)

	tool := NewLsTool(tmpDir, nil)

	args, _ := json.Marshal(map[string]string{"path": "."})
	result, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tr := result.(ToolResult)
	if !strings.Contains(tr.Content, "a.txt") {
		t.Errorf("expected 'a.txt' in output, got: %s", tr.Content)
	}
	if !strings.Contains(tr.Content, "subdir/") {
		t.Errorf("expected 'subdir/' in output, got: %s", tr.Content)
	}
	if !strings.Contains(tr.Content, ".hidden") {
		t.Errorf("expected '.hidden' in output, got: %s", tr.Content)
	}
}

func TestEditTool(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.go")
	os.WriteFile(testFile, []byte("package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n"), 0644)

	tool := NewEditTool(tmpDir, nil)

	args, _ := json.Marshal(map[string]any{
		"path": "test.go",
		"edits": []map[string]string{
			{"oldText": "println(\"hello\")", "newText": "println(\"world\")"},
		},
	})
	result, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tr := result.(ToolResult)
	if !strings.Contains(tr.Content, "Successfully replaced") {
		t.Errorf("expected success message, got: %s", tr.Content)
	}

	// Verify file was modified.
	data, _ := os.ReadFile(testFile)
	if !strings.Contains(string(data), "world") {
		t.Errorf("expected file to contain 'world', got: %s", string(data))
	}
}

func TestBashTool(t *testing.T) {
	tmpDir := t.TempDir()
	tool := NewBashTool(tmpDir, nil)

	args, _ := json.Marshal(map[string]string{"command": "echo hello"})
	result, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tr := result.(ToolResult)
	if !strings.Contains(tr.Content, "hello") {
		t.Errorf("expected 'hello' in output, got: %s", tr.Content)
	}
}

func TestGrepTool(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "a.go"), []byte("package main\nfunc main() {}\n"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "b.go"), []byte("package test\nfunc test() {}\n"), 0644)

	tool := NewGrepTool(tmpDir, nil)

	args, _ := json.Marshal(map[string]string{"pattern": "package main"})
	result, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tr := result.(ToolResult)
	if !strings.Contains(tr.Content, "a.go") {
		t.Errorf("expected 'a.go' in output, got: %s", tr.Content)
	}
}

func TestFindTool(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "a.go"), []byte(""), 0644)
	os.WriteFile(filepath.Join(tmpDir, "b.ts"), []byte(""), 0644)

	tool := NewFindTool(tmpDir, nil)

	args, _ := json.Marshal(map[string]string{"pattern": "*.go"})
	result, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tr := result.(ToolResult)
	if !strings.Contains(tr.Content, "a.go") {
		t.Errorf("expected 'a.go' in output, got: %s", tr.Content)
	}
}

func TestTruncateHead(t *testing.T) {
	content := "line1\nline2\nline3\nline4\nline5"
	result := TruncateHead(content, TruncationOptions{MaxLines: 3, MaxBytes: 1000})
	if !result.Truncated {
		t.Error("expected truncation")
	}
	if result.TruncatedBy != "lines" {
		t.Errorf("expected lines truncation, got: %s", result.TruncatedBy)
	}
	if result.OutputLines != 3 {
		t.Errorf("expected 3 output lines, got: %d", result.OutputLines)
	}
}

func TestTruncateTail(t *testing.T) {
	content := "line1\nline2\nline3\nline4\nline5"
	result := TruncateTail(content, TruncationOptions{MaxLines: 3, MaxBytes: 1000})
	if !result.Truncated {
		t.Error("expected truncation")
	}
	if result.OutputLines != 3 {
		t.Errorf("expected 3 output lines, got: %d", result.OutputLines)
	}
	if !strings.HasPrefix(result.Content, "line3") {
		t.Errorf("expected to start with 'line3', got: %s", result.Content)
	}
}
