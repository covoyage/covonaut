package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}
	cmd = exec.Command("git", "config", "user.email", "test@test.com")
	cmd.Dir = dir
	cmd.Run()
	cmd = exec.Command("git", "config", "user.name", "Test")
	cmd.Dir = dir
	cmd.Run()
}

func TestGitStatusTool(t *testing.T) {
	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir)
	os.WriteFile(filepath.Join(tmpDir, "file.txt"), []byte("hello"), 0644)

	tool := NewGitStatusTool(tmpDir, nil)
	result, err := tool.Func(context.Background(), json.RawMessage("{}"))
	if err != nil {
		t.Fatalf("git_status failed: %v", err)
	}

	r := result.(ToolResult)
	if !strings.Contains(r.Content, "file.txt") && !strings.Contains(r.Content, "??") {
		t.Errorf("expected untracked file in status, got: %s", r.Content)
	}
}

func TestGitDiffTool(t *testing.T) {
	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir)
	os.WriteFile(filepath.Join(tmpDir, "file.txt"), []byte("hello"), 0644)

	cmd := exec.Command("git", "add", ".")
	cmd.Dir = tmpDir
	cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "initial")
	cmd.Dir = tmpDir
	cmd.Run()

	os.WriteFile(filepath.Join(tmpDir, "file.txt"), []byte("world"), 0644)

	tool := NewGitDiffTool(tmpDir, nil)
	result, err := tool.Func(context.Background(), json.RawMessage("{}"))
	if err != nil {
		t.Fatalf("git_diff failed: %v", err)
	}

	r := result.(ToolResult)
	if !strings.Contains(r.Content, "hello") || !strings.Contains(r.Content, "world") {
		t.Errorf("expected diff content, got: %s", r.Content)
	}
}

func TestGitLogTool(t *testing.T) {
	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir)
	os.WriteFile(filepath.Join(tmpDir, "file.txt"), []byte("hello"), 0644)

	cmd := exec.Command("git", "add", ".")
	cmd.Dir = tmpDir
	cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "initial commit")
	cmd.Dir = tmpDir
	cmd.Run()

	tool := NewGitLogTool(tmpDir, nil)
	result, err := tool.Func(context.Background(), json.RawMessage("{}"))
	if err != nil {
		t.Fatalf("git_log failed: %v", err)
	}

	r := result.(ToolResult)
	if !strings.Contains(r.Content, "initial commit") {
		t.Errorf("expected commit message in log, got: %s", r.Content)
	}
}

func TestGitLogToolMaxCount(t *testing.T) {
	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir)

	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("file%d.txt", i)
		os.WriteFile(filepath.Join(tmpDir, name), []byte("x"), 0644)
		cmd := exec.Command("git", "add", ".")
		cmd.Dir = tmpDir
		cmd.Run()
		cmd = exec.Command("git", "commit", "-m", fmt.Sprintf("commit %d", i))
		cmd.Dir = tmpDir
		cmd.Run()
	}

	tool := NewGitLogTool(tmpDir, nil)
	args, _ := json.Marshal(map[string]any{"max_count": 3})
	result, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("git_log failed: %v", err)
	}

	r := result.(ToolResult)
	lines := strings.Split(strings.TrimSpace(r.Content), "\n")
	if len(lines) != 3 {
		t.Errorf("expected 3 commits, got %d: %s", len(lines), r.Content)
	}
}
