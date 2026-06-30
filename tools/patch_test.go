package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPatchTool(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.go")
	os.WriteFile(testFile, []byte("package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n"), 0644)

	tool := NewPatchTool(tmpDir, nil)

	// Test successful patch.
	args, _ := json.Marshal(map[string]string{
		"path":       "test.go",
		"old_string": "println(\"hello\")",
		"new_string": "println(\"world\")",
	})
	result, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tr := result.(ToolResult)
	if !strings.Contains(tr.Content, "Successfully patched") {
		t.Errorf("expected success message, got: %s", tr.Content)
	}

	// Verify file was modified.
	data, _ := os.ReadFile(testFile)
	if !strings.Contains(string(data), "world") {
		t.Errorf("expected file to contain 'world', got: %s", string(data))
	}
}

func TestPatchToolNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.go")
	os.WriteFile(testFile, []byte("package main\n"), 0644)

	tool := NewPatchTool(tmpDir, nil)

	args, _ := json.Marshal(map[string]string{
		"path":       "test.go",
		"old_string": "nonexistent text",
		"new_string": "replacement",
	})
	_, err := tool.Func(context.Background(), args)
	if err == nil {
		t.Fatal("expected error for missing old_string")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %v", err)
	}
}

func TestPatchToolMultipleMatches(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(testFile, []byte("hello world\nhello world\n"), 0644)

	tool := NewPatchTool(tmpDir, nil)

	args, _ := json.Marshal(map[string]string{
		"path":       "test.txt",
		"old_string": "hello world",
		"new_string": "goodbye world",
	})
	_, err := tool.Func(context.Background(), args)
	if err == nil {
		t.Fatal("expected error for multiple matches")
	}
	if !strings.Contains(err.Error(), "found 2 times") {
		t.Errorf("expected multiple matches error, got: %v", err)
	}
}

func TestPatchToolLineEndings(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	// File with CRLF line endings.
	os.WriteFile(testFile, []byte("line1\r\nline2\r\nline3\r\n"), 0644)

	tool := NewPatchTool(tmpDir, nil)

	// Patch should work regardless of line ending differences in old_string.
	args, _ := json.Marshal(map[string]string{
		"path":       "test.txt",
		"old_string": "line2",
		"new_string": "modified",
	})
	result, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tr := result.(ToolResult)
	if !strings.Contains(tr.Content, "Successfully patched") {
		t.Errorf("expected success message, got: %s", tr.Content)
	}

	// Verify line endings are preserved.
	data, _ := os.ReadFile(testFile)
	if !strings.Contains(string(data), "\r\n") {
		t.Errorf("expected CRLF line endings to be preserved")
	}
}
