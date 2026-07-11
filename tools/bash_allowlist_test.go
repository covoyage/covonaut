package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
)

func TestCommandAllowed_EmptyCommand(t *testing.T) {
	if err := commandAllowed("", nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestCommandAllowed_NoListsAllowsAll(t *testing.T) {
	if err := commandAllowed("rm -rf /", nil, nil); err != nil {
		t.Fatalf("expected allowed, got %v", err)
	}
}

func TestCommandAllowed_AllowListMatch(t *testing.T) {
	if err := commandAllowed("git status", []string{"git"}, nil); err != nil {
		t.Fatalf("expected allowed, got %v", err)
	}
}

func TestCommandAllowed_AllowListReject(t *testing.T) {
	if err := commandAllowed("rm -rf /", []string{"git"}, nil); err == nil {
		t.Fatal("expected rejection")
	}
}

func TestCommandAllowed_AllowListWordBoundary(t *testing.T) {
	// "git" must not match "github"
	if err := commandAllowed("github push", []string{"git"}, nil); err == nil {
		t.Fatal("github should not match git prefix")
	}
	// "git" matches "git" exactly
	if err := commandAllowed("git", []string{"git"}, nil); err != nil {
		t.Fatalf("exact match should pass: %v", err)
	}
}

func TestCommandAllowed_AllowListMultiWordEntry(t *testing.T) {
	if err := commandAllowed("go test ./...", []string{"go test", "git"}, nil); err != nil {
		t.Fatalf("expected allowed: %v", err)
	}
	if err := commandAllowed("go build", []string{"go test", "git"}, nil); err == nil {
		t.Fatal("go build should not match go test")
	}
}

func TestCommandAllowed_BlockListMatch(t *testing.T) {
	if err := commandAllowed("rm -rf /tmp", nil, []string{"rm -rf"}); err == nil {
		t.Fatal("expected block")
	}
}

func TestCommandAllowed_BlockListNoMatch(t *testing.T) {
	if err := commandAllowed("ls -la", nil, []string{"rm -rf"}); err != nil {
		t.Fatalf("expected allowed: %v", err)
	}
}

func TestCommandAllowed_BlockOverridesAllow(t *testing.T) {
	// rm -rf is in both allow and block; block wins.
	if err := commandAllowed("rm -rf x", []string{"rm -rf"}, []string{"rm -rf"}); err == nil {
		t.Fatal("block should override allow")
	}
}

func TestCommandAllowed_MultiSubcommandAllPass(t *testing.T) {
	cmd := "git status && go test ./... && ls -la"
	if err := commandAllowed(cmd, []string{"git", "go test", "ls"}, nil); err != nil {
		t.Fatalf("expected allowed: %v", err)
	}
}

func TestCommandAllowed_MultiSubcommandOneRejected(t *testing.T) {
	cmd := "git status && rm -rf / && ls"
	if err := commandAllowed(cmd, []string{"git", "ls"}, nil); err == nil {
		t.Fatal("expected rejection on rm subcommand")
	}
}

func TestCommandAllowed_PipeSeparator(t *testing.T) {
	cmd := "git log | grep fix"
	if err := commandAllowed(cmd, []string{"git", "grep"}, nil); err != nil {
		t.Fatalf("expected allowed: %v", err)
	}
	cmd2 := "git log | rm -rf /"
	if err := commandAllowed(cmd2, []string{"git"}, nil); err == nil {
		t.Fatal("expected rejection on rm in pipe")
	}
}

func TestCommandAllowed_SemicolonSeparator(t *testing.T) {
	cmd := "ls; rm -rf /"
	if err := commandAllowed(cmd, []string{"ls"}, nil); err == nil {
		t.Fatal("expected rejection on rm after semicolon")
	}
}

// --- bash tool integration ---
// (reuses mockBashOps from tools_ops_test.go, which takes an execFunc)

func callBashFunc(t *testing.T, tool *agentcore.Tool, cmd string) (any, error) {
	t.Helper()
	args, _ := json.Marshal(BashToolInput{Command: cmd})
	return tool.Func(context.Background(), args)
}

func recordingOps(called *bool, gotCmd *string) *mockBashOps {
	return &mockBashOps{
		execFunc: func(cmd, cwd string, env map[string]string, timeout *int, onData func([]byte)) (int, error) {
			*called = true
			if gotCmd != nil {
				*gotCmd = cmd
			}
			return 0, nil
		},
	}
}

func TestBashTool_AllowListRejects(t *testing.T) {
	var called bool
	ops := recordingOps(&called, nil)
	tool := NewBashTool(".", &BashToolConfig{Operations: ops, AllowList: []string{"git"}})
	_, err := callBashFunc(t, tool, "rm -rf /")
	if err == nil {
		t.Fatal("expected rejection error")
	}
	if called {
		t.Fatal("operations should not be called for rejected command")
	}
}

func TestBashTool_AllowListAllows(t *testing.T) {
	var called bool
	var gotCmd string
	ops := recordingOps(&called, &gotCmd)
	tool := NewBashTool(".", &BashToolConfig{Operations: ops, AllowList: []string{"git"}})
	_, err := callBashFunc(t, tool, "git status")
	if err != nil {
		t.Fatalf("expected allowed: %v", err)
	}
	if !called {
		t.Fatal("operations should have been called")
	}
	if gotCmd != "git status" {
		t.Fatalf("cmd=%q want %q", gotCmd, "git status")
	}
}

func TestBashTool_BlockListRejects(t *testing.T) {
	var called bool
	ops := recordingOps(&called, nil)
	tool := NewBashTool(".", &BashToolConfig{Operations: ops, BlockList: []string{"rm -rf"}})
	_, err := callBashFunc(t, tool, "rm -rf /tmp")
	if err == nil {
		t.Fatal("expected block error")
	}
	if called {
		t.Fatal("operations should not be called for blocked command")
	}
}

func TestBashTool_BlockListAllowsNonBlocked(t *testing.T) {
	var called bool
	ops := recordingOps(&called, nil)
	tool := NewBashTool(".", &BashToolConfig{Operations: ops, BlockList: []string{"rm -rf"}})
	_, err := callBashFunc(t, tool, "ls -la")
	if err != nil {
		t.Fatalf("expected allowed: %v", err)
	}
	if !called {
		t.Fatal("operations should have been called")
	}
}

func TestBashTool_NoListsAllowsAll(t *testing.T) {
	var called bool
	ops := recordingOps(&called, nil)
	tool := NewBashTool(".", &BashToolConfig{Operations: ops})
	_, err := callBashFunc(t, tool, "rm -rf /")
	if err != nil {
		t.Fatalf("expected allowed with no lists: %v", err)
	}
	if !called {
		t.Fatal("operations should have been called")
	}
}
