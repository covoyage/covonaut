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

func TestReadToolTruncationIncludesOffsetHint(t *testing.T) {
	tmpDir := t.TempDir()
	var b strings.Builder
	for i := 1; i <= 12; i++ {
		fmt.Fprintf(&b, "line-%d\n", i)
	}
	testFile := filepath.Join(tmpDir, "big.txt")
	if err := os.WriteFile(testFile, []byte(b.String()), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadTool(tmpDir, &ReadToolConfig{MaxLines: 5, MaxBytes: 50 * 1024})
	args, _ := json.Marshal(map[string]string{"path": "big.txt"})
	result, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tr := result.(ToolResult)
	if !strings.Contains(tr.Content, "continue with offset=6 and limit") {
		t.Fatalf("expected offset continuation hint, got: %s", tr.Content)
	}
	if !strings.Contains(tr.Content, "line-1") || strings.Contains(tr.Content, "line-12") {
		t.Fatalf("expected truncated head, got: %s", tr.Content)
	}
}

func TestReadToolDispatchesImageToHandler(t *testing.T) {
	tmpDir := t.TempDir()
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	if err := os.WriteFile(filepath.Join(tmpDir, "dot.png"), png, 0644); err != nil {
		t.Fatal(err)
	}

	called := false
	tool := NewReadTool(tmpDir, &ReadToolConfig{
		MediaHandler: func(ctx context.Context, kind ReadMediaKind, paths []string, input ReadToolInput) (any, error) {
			called = true
			if kind != ReadMediaImage {
				t.Fatalf("kind = %q", kind)
			}
			if len(paths) != 1 || !strings.HasSuffix(paths[0], "dot.png") {
				t.Fatalf("paths = %v", paths)
			}
			if input.Prompt != "what's in this" {
				t.Fatalf("prompt = %q", input.Prompt)
			}
			return ToolResult{Content: "image-ok"}, nil
		},
	})
	args, _ := json.Marshal(map[string]string{"path": "dot.png", "prompt": "what's in this"})
	res, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("read image: %v", err)
	}
	if !called {
		t.Fatal("expected media handler")
	}
	if res.(ToolResult).Content != "image-ok" {
		t.Fatalf("got %+v", res)
	}
}

func TestReadToolImageWithoutHandlerErrors(t *testing.T) {
	tmpDir := t.TempDir()
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	if err := os.WriteFile(filepath.Join(tmpDir, "dot.png"), png, 0644); err != nil {
		t.Fatal(err)
	}
	tool := NewReadTool(tmpDir, nil)
	args, _ := json.Marshal(map[string]string{"path": "dot.png"})
	if _, err := tool.Func(context.Background(), args); err == nil {
		t.Fatal("expected error without MediaHandler")
	}
}

func TestReadToolRendersNotebook(t *testing.T) {
	tmpDir := t.TempDir()
	nb := `{
		"cells": [
			{"cell_type": "markdown", "source": ["# Title\n"], "outputs": []},
			{"cell_type": "code", "source": ["print(1)\n"], "outputs": [{"output_type": "stream", "text": ["1\n"]}]}
		]
	}`
	if err := os.WriteFile(filepath.Join(tmpDir, "demo.ipynb"), []byte(nb), 0644); err != nil {
		t.Fatal(err)
	}
	tool := NewReadTool(tmpDir, nil)
	args, _ := json.Marshal(map[string]string{"path": "demo.ipynb"})
	res, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("read notebook: %v", err)
	}
	content := res.(ToolResult).Content
	if !strings.Contains(content, "cell 1 (markdown)") || !strings.Contains(content, "# Title") {
		t.Fatalf("markdown cell missing: %s", content)
	}
	if !strings.Contains(content, "print(1)") || !strings.Contains(content, "[stream]") {
		t.Fatalf("code cell missing: %s", content)
	}
}

func TestReadToolDispatchesPDFToHandler(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "doc.pdf"), []byte("%PDF-1.4\n"), 0644); err != nil {
		t.Fatal(err)
	}
	called := false
	tool := NewReadTool(tmpDir, &ReadToolConfig{
		MediaHandler: func(ctx context.Context, kind ReadMediaKind, paths []string, input ReadToolInput) (any, error) {
			called = true
			if kind != ReadMediaPDF {
				t.Fatalf("kind = %q", kind)
			}
			if len(paths) != 1 || !strings.HasSuffix(paths[0], "doc.pdf") {
				t.Fatalf("paths = %v", paths)
			}
			if input.Pages != "1-2" {
				t.Fatalf("pages = %q", input.Pages)
			}
			return ToolResult{Content: "pdf-ok"}, nil
		},
	})
	args, _ := json.Marshal(map[string]string{"path": "doc.pdf", "pages": "1-2"})
	res, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("read pdf: %v", err)
	}
	if !called {
		t.Fatal("expected media handler")
	}
	if res.(ToolResult).Content != "pdf-ok" {
		t.Fatalf("got %+v", res)
	}
}

func TestReadToolDispatchesRemoteURL(t *testing.T) {
	called := false
	tool := NewReadTool(t.TempDir(), &ReadToolConfig{
		MediaHandler: func(ctx context.Context, kind ReadMediaKind, paths []string, input ReadToolInput) (any, error) {
			called = true
			if kind != ReadMediaImage {
				t.Fatalf("kind = %q", kind)
			}
			if len(paths) != 1 || paths[0] != "https://example.com/a.png" {
				t.Fatalf("paths = %v", paths)
			}
			return ToolResult{Content: "remote-ok"}, nil
		},
	})
	args, _ := json.Marshal(map[string]string{"path": "https://example.com/a.png"})
	res, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("read url: %v", err)
	}
	if !called {
		t.Fatal("expected media handler")
	}
	if res.(ToolResult).Content != "remote-ok" {
		t.Fatalf("got %+v", res)
	}
}

func TestReadToolDispatchesMultiplePaths(t *testing.T) {
	called := false
	tool := NewReadTool(t.TempDir(), &ReadToolConfig{
		MediaHandler: func(ctx context.Context, kind ReadMediaKind, paths []string, input ReadToolInput) (any, error) {
			called = true
			if kind != ReadMediaImage {
				t.Fatalf("kind = %q", kind)
			}
			if len(paths) != 2 {
				t.Fatalf("paths = %v", paths)
			}
			return ToolResult{Content: "multi-ok"}, nil
		},
	})
	args, _ := json.Marshal(map[string]any{"paths": []string{"a.png", "b.png"}})
	res, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("read paths: %v", err)
	}
	if !called {
		t.Fatal("expected media handler")
	}
	if res.(ToolResult).Content != "multi-ok" {
		t.Fatalf("got %+v", res)
	}
}

func TestReadToolRejectsBinary(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "blob.bin"), []byte{0, 1, 2, 3, 4}, 0644); err != nil {
		t.Fatal(err)
	}
	tool := NewReadTool(tmpDir, nil)
	args, _ := json.Marshal(map[string]string{"path": "blob.bin"})
	if _, err := tool.Func(context.Background(), args); err == nil {
		t.Fatal("expected binary rejection")
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
