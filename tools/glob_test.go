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

func TestDefaultQueriesStopAtGlobalLimit(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a/first.go", "b/second.go", "z.go"} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("match"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	glob, err := (DefaultGlobOperations{}).Glob("*.go", dir, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(glob) != 1 || glob[0] != filepath.Join("a", "first.go") {
		t.Fatalf("glob=%v", glob)
	}
	find, err := (DefaultFindOperations{}).Glob("*.go", dir, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(find) != 1 || find[0] != filepath.Join(dir, "a", "first.go") {
		t.Fatalf("find=%v", find)
	}
}

type unorderedGlobOperations struct{}

func (unorderedGlobOperations) Glob(string, string, int) ([]string, error) {
	return []string{"z.go", "a.go", "m.go"}, nil
}

func TestGlobSortsCustomResultsBeforeLimit(t *testing.T) {
	tool := NewGlobTool(t.TempDir(), &GlobToolConfig{Operations: unorderedGlobOperations{}, Limit: 1})
	output, err := tool.Func(context.Background(), json.RawMessage(`{"pattern":"*.go"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := output.(ToolResult).Content; !strings.HasPrefix(got, "a.go\n\n[") || strings.Contains(got, "z.go") {
		t.Fatalf("glob=%s", got)
	}
}
