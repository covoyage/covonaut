package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

// mockFileInfo implements os.FileInfo for testing.
type mockFileInfo struct {
	name    string
	size    int64
	isDir   bool
	mode    os.FileMode
	modTime time.Time
}

func (m *mockFileInfo) Name() string      { return m.name }
func (m *mockFileInfo) Size() int64       { return m.size }
func (m *mockFileInfo) Mode() os.FileMode { return m.mode }
func (m *mockFileInfo) ModTime() time.Time { return m.modTime }
func (m *mockFileInfo) IsDir() bool        { return m.isDir }
func (m *mockFileInfo) Sys() any          { return nil }

// --- mock operations ---

type mockBashOps struct {
	execFunc func(command, cwd string, env map[string]string, timeoutSecs *int, onData func([]byte)) (int, error)
}

func (m *mockBashOps) Exec(command, cwd string, env map[string]string, timeoutSecs *int, onData func([]byte)) (int, error) {
	return m.execFunc(command, cwd, env, timeoutSecs, onData)
}

type mockWebFetchOps struct {
	fetchFunc func(url string) (string, error)
}

func (m *mockWebFetchOps) Fetch(url string) (string, error) {
	return m.fetchFunc(url)
}

type mockReadOps struct {
	readFileFunc func(path string) ([]byte, error)
	statFunc     func(path string) (os.FileInfo, error)
}

func (m *mockReadOps) ReadFile(path string) ([]byte, error) { return m.readFileFunc(path) }
func (m *mockReadOps) Stat(path string) (os.FileInfo, error) { return m.statFunc(path) }

type mockEditOps struct {
	readFileFunc  func(path string) ([]byte, error)
	writeFileFunc func(path string, content []byte) error
	accessFunc    func(path string) error
}

func (m *mockEditOps) ReadFile(path string) ([]byte, error)    { return m.readFileFunc(path) }
func (m *mockEditOps) WriteFile(path string, content []byte) error { return m.writeFileFunc(path, content) }
func (m *mockEditOps) Access(path string) error                 { return m.accessFunc(path) }

type mockLsOps struct {
	existsFunc  func(path string) bool
	statFunc    func(path string) (os.FileInfo, error)
	readDirFunc func(path string) ([]os.DirEntry, error)
}

func (m *mockLsOps) Exists(path string) bool                      { return m.existsFunc(path) }
func (m *mockLsOps) Stat(path string) (os.FileInfo, error)        { return m.statFunc(path) }
func (m *mockLsOps) ReadDir(path string) ([]os.DirEntry, error)   { return m.readDirFunc(path) }

type mockGrepOps struct {
	isDirectoryFunc func(path string) (bool, error)
	readFileFunc    func(path string) (string, error)
}

func (m *mockGrepOps) IsDirectory(path string) (bool, error) { return m.isDirectoryFunc(path) }
func (m *mockGrepOps) ReadFile(path string) (string, error)  { return m.readFileFunc(path) }

type mockFindOps struct {
	existsFunc func(path string) bool
	globFunc   func(pattern string, cwd string, ignore []string, limit int) ([]string, error)
}

func (m *mockFindOps) Exists(path string) bool                                          { return m.existsFunc(path) }
func (m *mockFindOps) Glob(pattern string, cwd string, ignore []string, limit int) ([]string, error) {
	return m.globFunc(pattern, cwd, ignore, limit)
}

func callTool(t *testing.T, tool *agentcore.Tool, input any) (ToolResult, error) {
	t.Helper()
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	result, err := tool.Func(context.Background(), data)
	if err != nil {
		return ToolResult{}, err
	}
	tr, ok := result.(ToolResult)
	if !ok {
		t.Fatalf("unexpected result type: %T", result)
	}
	return tr, nil
}

// --- Bash tests ---

func TestBashToolBasic(t *testing.T) {
	tool := NewBashTool("/tmp", &BashToolConfig{
		Operations: &mockBashOps{
			execFunc: func(cmd, cwd string, env map[string]string, to *int, onData func([]byte)) (int, error) {
				onData([]byte("hello\n"))
				return 0, nil
			},
		},
	})
	tr, err := callTool(t, tool, BashToolInput{Command: "echo hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tr.Content != "hello\n" {
		t.Fatalf("content = %q", tr.Content)
	}
}

func TestBashToolEmptyCommand(t *testing.T) {
	tool := NewBashTool("/tmp", &BashToolConfig{
		Operations: &mockBashOps{},
	})
	_, err := callTool(t, tool, BashToolInput{Command: ""})
	if err == nil {
		t.Fatal("expected error for empty command")
	}
}

func TestBashToolNonZeroExit(t *testing.T) {
	tool := NewBashTool("/tmp", &BashToolConfig{
		Operations: &mockBashOps{
			execFunc: func(cmd, cwd string, env map[string]string, to *int, onData func([]byte)) (int, error) {
				onData([]byte("error output\n"))
				return 1, nil
			},
		},
	})
	tr, err := callTool(t, tool, BashToolInput{Command: "false"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tr.Content != "error output\n\n\nCommand exited with code 1" {
		t.Fatalf("content = %q", tr.Content)
	}
}

func TestBashToolConfigDefaults(t *testing.T) {
	cfg := &BashToolConfig{}
	cfg.defaults()
	if cfg.MaxBytes != 50*1024 {
		t.Fatalf("MaxBytes = %d", cfg.MaxBytes)
	}
	if cfg.MaxLines != 2000 {
		t.Fatalf("MaxLines = %d", cfg.MaxLines)
	}
}

// --- WebFetch tests ---

func TestWebFetchToolBasic(t *testing.T) {
	tool := NewWebFetchTool(&WebFetchToolConfig{
		Operations: &mockWebFetchOps{
			fetchFunc: func(url string) (string, error) {
				return "<html><body><p>hello world</p></body></html>", nil
			},
		},
		MaxBytes: 100000,
		MaxLines: 1000,
	})
	tr, err := callTool(t, tool, WebFetchToolInput{URL: "http://example.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tr.Content != "hello world" {
		t.Fatalf("content = %q", tr.Content)
	}
}

func TestWebFetchToolEmptyURL(t *testing.T) {
	tool := NewWebFetchTool(&WebFetchToolConfig{
		Operations: &mockWebFetchOps{},
	})
	_, err := callTool(t, tool, WebFetchToolInput{URL: ""})
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
}

func TestWebFetchToolFetchError(t *testing.T) {
	tool := NewWebFetchTool(&WebFetchToolConfig{
		Operations: &mockWebFetchOps{
			fetchFunc: func(url string) (string, error) {
				return "", errors.New("connection refused")
			},
		},
	})
	_, err := callTool(t, tool, WebFetchToolInput{URL: "http://example.com"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- Read tests ---

func TestReadToolBasic(t *testing.T) {
	tool := NewReadTool("/tmp", &ReadToolConfig{
		Operations: &mockReadOps{
			readFileFunc: func(path string) ([]byte, error) {
				return []byte("line1\nline2\nline3\n"), nil
			},
			statFunc: func(path string) (os.FileInfo, error) {
				return &mockFileInfo{size: 18}, nil
			},
		},
	})
	tr, err := callTool(t, tool, ReadToolInput{Path: "test.txt"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tr.Content != "line1\nline2\nline3\n" {
		t.Fatalf("content = %q", tr.Content)
	}
}

func TestReadToolWithOffsetLimit(t *testing.T) {
	tool := NewReadTool("/tmp", &ReadToolConfig{
		Operations: &mockReadOps{
			readFileFunc: func(path string) ([]byte, error) {
				var content string
				for i := 1; i <= 20; i++ {
					content += fmt.Sprintf("line %d\n", i)
				}
				return []byte(content), nil
			},
			statFunc: func(path string) (os.FileInfo, error) {
				return &mockFileInfo{size: 100}, nil
			},
		},
	})
	limit := 3
	offset := 5
	tr, err := callTool(t, tool, ReadToolInput{Path: "test.txt", Offset: &offset, Limit: &limit})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tr.Content != "line 5\nline 6\nline 7" {
		t.Fatalf("content = %q", tr.Content)
	}
}

func TestReadToolFileNotFound(t *testing.T) {
	tool := NewReadTool("/tmp", &ReadToolConfig{
		Operations: &mockReadOps{
			readFileFunc: func(path string) ([]byte, error) {
				return nil, errors.New("file not found")
			},
			statFunc: func(path string) (os.FileInfo, error) {
				return nil, errors.New("file not found")
			},
		},
	})
	_, err := callTool(t, tool, ReadToolInput{Path: "nonexistent.txt"})
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

// --- Edit tests ---

func TestEditToolBasic(t *testing.T) {
	tool := NewEditTool("/tmp", &EditToolConfig{
		Operations: &mockEditOps{
			accessFunc: func(path string) error { return nil },
			readFileFunc: func(path string) ([]byte, error) {
				return []byte("hello world"), nil
			},
			writeFileFunc: func(path string, content []byte) error {
				return nil
			},
		},
	})
	tr, err := callTool(t, tool, EditToolInput{
		Path: "test.txt",
		Edits: []Edit{
			{OldText: "hello", NewText: "hi"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tr.Content != "Successfully replaced 1 block(s) in test.txt." {
		t.Fatalf("content = %q", tr.Content)
	}
}

func TestEditToolNotFound(t *testing.T) {
	tool := NewEditTool("/tmp", &EditToolConfig{
		Operations: &mockEditOps{
			accessFunc: func(path string) error {
				return errors.New("not found")
			},
		},
	})
	_, err := callTool(t, tool, EditToolInput{
		Path: "nonexistent.txt",
		Edits: []Edit{
			{OldText: "hello", NewText: "hi"},
		},
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestEditToolNoEdits(t *testing.T) {
	tool := NewEditTool("/tmp", &EditToolConfig{
		Operations: &mockEditOps{
			accessFunc: func(path string) error { return nil },
			readFileFunc: func(path string) ([]byte, error) {
				return []byte("content"), nil
			},
		},
	})
	_, err := callTool(t, tool, EditToolInput{
		Path:  "test.txt",
		Edits: []Edit{},
	})
	if err == nil {
		t.Fatal("expected error for empty edits")
	}
}

// --- Ls tests ---

type mockDirEntry struct {
	name  string
	isDir bool
}

func (m *mockDirEntry) Name() string               { return m.name }
func (m *mockDirEntry) IsDir() bool                 { return m.isDir }
func (m *mockDirEntry) Type() os.FileMode           { return 0 }
func (m *mockDirEntry) Info() (os.FileInfo, error)  { return nil, nil }

func TestLsToolBasic(t *testing.T) {
	tool := NewLsTool("/tmp", &LsToolConfig{
		Operations: &mockLsOps{
			existsFunc: func(path string) bool { return true },
			statFunc: func(path string) (os.FileInfo, error) {
				return &mockFileInfo{isDir: true, size: 0, name: "testdir"}, nil
			},
			readDirFunc: func(path string) ([]os.DirEntry, error) {
				return []os.DirEntry{
					&mockDirEntry{name: "file1.txt", isDir: false},
					&mockDirEntry{name: "subdir", isDir: true},
				}, nil
			},
		},
	})
	tr, err := callTool(t, tool, LsToolInput{Path: "."})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tr.Content == "" {
		t.Fatal("expected non-empty content")
	}
}

func TestLsToolNotExists(t *testing.T) {
	tool := NewLsTool("/tmp", &LsToolConfig{
		Operations: &mockLsOps{
			existsFunc: func(path string) bool { return false },
		},
	})
	_, err := callTool(t, tool, LsToolInput{Path: "nonexistent"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestLsToolFile(t *testing.T) {
	tool := NewLsTool("/tmp", &LsToolConfig{
		Operations: &mockLsOps{
			existsFunc: func(path string) bool { return true },
			statFunc: func(path string) (os.FileInfo, error) {
				return &mockFileInfo{isDir: true, size: 100, name: "somedir"}, nil
			},
			readDirFunc: func(path string) ([]os.DirEntry, error) {
				return []os.DirEntry{
					&mockDirEntry{name: "file.txt", isDir: false},
				}, nil
			},
		},
	})
	_, err := callTool(t, tool, LsToolInput{Path: "somedir"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- Grep tests ---

func TestGrepToolBasic(t *testing.T) {
	tool := NewGrepTool("/tmp", &GrepToolConfig{
		Operations: &mockGrepOps{
			isDirectoryFunc: func(path string) (bool, error) {
				return true, nil
			},
			readFileFunc: func(path string) (string, error) {
				return "line1 has pattern\nline2 no\nline3 has PATTERN\n", nil
			},
		},
	})
	tr, err := callTool(t, tool, GrepToolInput{
		Pattern: "pattern",
		Path:    "/test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tr.Content == "" {
		t.Fatal("expected match results")
	}
}

func TestGrepToolIgnoreCase(t *testing.T) {
	tool := NewGrepTool("/tmp", &GrepToolConfig{
		Operations: &mockGrepOps{
			isDirectoryFunc: func(path string) (bool, error) {
				return true, nil
			},
			readFileFunc: func(path string) (string, error) {
				return "PATTERN\npattern\nPattern\n", nil
			},
		},
	})
	tr, err := callTool(t, tool, GrepToolInput{
		Pattern:    "pattern",
		Path:       "/test",
		IgnoreCase: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tr.Content == "" {
		t.Fatal("expected match results with ignore case")
	}
}

func TestGrepToolLiteral(t *testing.T) {
	tool := NewGrepTool("/tmp", &GrepToolConfig{
		Operations: &mockGrepOps{
			isDirectoryFunc: func(path string) (bool, error) {
				return true, nil
			},
			readFileFunc: func(path string) (string, error) {
				return "hello.world\nhello\nworld\n", nil
			},
		},
	})
	tr, err := callTool(t, tool, GrepToolInput{
		Pattern: "hello.world",
		Path:    "/test",
		Literal: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tr.Content == "" {
		t.Fatal("expected literal match")
	}
}

func TestGrepToolNoMatch(t *testing.T) {
	tool := NewGrepTool("/tmp", &GrepToolConfig{
		Operations: &mockGrepOps{
			isDirectoryFunc: func(path string) (bool, error) {
				return true, nil
			},
			readFileFunc: func(path string) (string, error) {
				return "nothing here\n", nil
			},
		},
	})
	tr, err := callTool(t, tool, GrepToolInput{
		Pattern: "missing",
		Path:    "/test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tr.Content != "No matches found" {
		t.Fatalf("expected 'No matches found', got %q", tr.Content)
	}
}

// --- Find tests ---

func TestFindToolBasic(t *testing.T) {
	tool := NewFindTool("/tmp", &FindToolConfig{
		Operations: &mockFindOps{
			existsFunc: func(path string) bool { return true },
			globFunc: func(pattern, cwd string, ignore []string, limit int) ([]string, error) {
				return []string{"file1.go", "file2.go"}, nil
			},
		},
	})
	tr, err := callTool(t, tool, FindToolInput{
		Pattern: "**/*.go",
		Path:    ".",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tr.Content == "" {
		t.Fatal("expected results")
	}
}

func TestFindToolNotExists(t *testing.T) {
	tool := NewFindTool("/tmp", &FindToolConfig{
		Operations: &mockFindOps{
			existsFunc: func(path string) bool { return false },
		},
	})
	_, err := callTool(t, tool, FindToolInput{
		Pattern: "**/*.go",
		Path:    "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error for missing path")
	}
}

func TestFindToolLimit(t *testing.T) {
	limit := 1
	tool := NewFindTool("/tmp", &FindToolConfig{
		Operations: &mockFindOps{
			existsFunc: func(path string) bool { return true },
			globFunc: func(pattern, cwd string, ignore []string, limitArg int) ([]string, error) {
				if limitArg != 1 {
					t.Fatalf("expected limit 1, got %d", limitArg)
				}
				return []string{"file1.go"}, nil
			},
		},
	})
	_, err := callTool(t, tool, FindToolInput{
		Pattern: "**/*.go",
		Path:    ".",
		Limit:   &limit,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFindToolConfigDefaults(t *testing.T) {
	cfg := &FindToolConfig{}
	cfg.defaults()
	if cfg.MaxBytes <= 0 {
		t.Fatal("MaxBytes should be positive")
	}
}

func TestLsToolConfigDefaults(t *testing.T) {
	cfg := &LsToolConfig{}
	cfg.defaults()
	if cfg.MaxBytes <= 0 {
		t.Fatal("MaxBytes should be positive")
	}
	if cfg.Limit <= 0 {
		t.Fatal("Limit should be positive")
	}
}

func TestGrepToolConfigDefaults(t *testing.T) {
	cfg := &GrepToolConfig{}
	cfg.defaults()
	if cfg.MaxBytes <= 0 {
		t.Fatal("MaxBytes should be positive")
	}
	if cfg.MaxLineLength <= 0 {
		t.Fatal("MaxLineLength should be positive")
	}
	if cfg.Limit <= 0 {
		t.Fatal("Limit should be positive")
	}
}

func TestReadToolConfigDefaults(t *testing.T) {
	cfg := &ReadToolConfig{}
	cfg.defaults()
	if cfg.MaxBytes <= 0 {
		t.Fatal("MaxBytes should be positive")
	}
	if cfg.MaxLines <= 0 {
		t.Fatal("MaxLines should be positive")
	}
}

func TestWebFetchToolConfigDefaults(t *testing.T) {
	cfg := &WebFetchToolConfig{}
	cfg.defaults()
	if cfg.MaxBytes <= 0 {
		t.Fatal("MaxBytes should be positive")
	}
}

func TestFormatSize(t *testing.T) {
	if s := FormatSize(500); s != "500B" {
		t.Fatalf("got %q", s)
	}
	if s := FormatSize(2048); s != "2.0KB" {
		t.Fatalf("got %q", s)
	}
	if s := FormatSize(1048576); s != "1.0MB" {
		t.Fatalf("got %q", s)
	}
}
