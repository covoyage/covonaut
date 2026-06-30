package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

// GitOperations defines pluggable operations for git tools.
type GitOperations interface {
	Exec(args []string, cwd string) (string, int, error)
}

// DefaultGitOperations executes git commands locally.
type DefaultGitOperations struct{}

func (d DefaultGitOperations) Exec(args []string, cwd string) (string, int, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	output, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return "", -1, err
		}
	}
	return string(output), exitCode, nil
}

// GitToolConfig configures git tools.
type GitToolConfig struct {
	Operations GitOperations
}

func (c *GitToolConfig) defaults() {
	if c.Operations == nil {
		c.Operations = DefaultGitOperations{}
	}
}

// --- git_status ---

type GitStatusInput struct{}

func NewGitStatusTool(cwd string, cfg *GitToolConfig) *agentcore.Tool {
	if cfg == nil {
		cfg = &GitToolConfig{}
	}
	cfg.defaults()

	return &agentcore.Tool{
		Name:        "git_status",
		Description: "Show the working tree status. Returns modified, staged, untracked, and conflicted files.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			output, code, err := cfg.Operations.Exec([]string{"status", "--short", "--branch"}, cwd)
			if err != nil {
				return resultErrf("git status failed: %w", err)
			}
			if code != 0 {
				return resultErrf("git status exited with code %d: %s", code, output)
			}

			output = strings.TrimSpace(output)
			if output == "" {
				return result("Working tree clean", nil)
			}
			return result(output, nil)
		},
	}
}

// --- git_diff ---

type GitDiffInput struct {
	Target   string `json:"target,omitempty"`
	Staged   bool   `json:"staged,omitempty"`
	FilePath string `json:"file_path,omitempty"`
}

func NewGitDiffTool(cwd string, cfg *GitToolConfig) *agentcore.Tool {
	if cfg == nil {
		cfg = &GitToolConfig{}
	}
	cfg.defaults()

	return &agentcore.Tool{
		Name:        "git_diff",
		Description: "Show changes between commits, commit and working tree, etc. Default shows unstaged changes.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target":    map[string]any{"type": "string", "description": "Commit, branch, or file to diff against (default: working tree vs HEAD)"},
				"staged":    map[string]any{"type": "boolean", "description": "Show staged changes instead of unstaged"},
				"file_path": map[string]any{"type": "string", "description": "Specific file to show diff for"},
			},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var input GitDiffInput
			if err := json.Unmarshal(args, &input); err != nil {
				return resultErrf("invalid arguments: %w", err)
			}

			gitArgs := []string{"diff"}
			if input.Staged {
				gitArgs = append(gitArgs, "--staged")
			}
			if input.Target != "" {
				gitArgs = append(gitArgs, input.Target)
			}
			if input.FilePath != "" {
				gitArgs = append(gitArgs, "--", input.FilePath)
			}

			output, code, err := cfg.Operations.Exec(gitArgs, cwd)
			if err != nil {
				return resultErrf("git diff failed: %w", err)
			}
			if code != 0 {
				return resultErrf("git diff exited with code %d: %s", code, output)
			}

			output = strings.TrimSpace(output)
			if output == "" {
				return result("No changes", nil)
			}
			return result(output, nil)
		},
	}
}

// --- git_log ---

type GitLogInput struct {
	MaxCount *int   `json:"max_count,omitempty"`
	FilePath string `json:"file_path,omitempty"`
	Oneline  bool   `json:"oneline,omitempty"`
}

func NewGitLogTool(cwd string, cfg *GitToolConfig) *agentcore.Tool {
	if cfg == nil {
		cfg = &GitToolConfig{}
	}
	cfg.defaults()

	return &agentcore.Tool{
		Name:        "git_log",
		Description: "Show commit logs. Default shows last 10 commits in oneline format.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"max_count": map[string]any{"type": "integer", "description": "Maximum number of commits to show (default: 10)"},
				"file_path": map[string]any{"type": "string", "description": "Only show commits affecting this file"},
				"oneline":   map[string]any{"type": "boolean", "description": "Show one commit per line (default: true)"},
			},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var input GitLogInput
			if err := json.Unmarshal(args, &input); err != nil {
				return resultErrf("invalid arguments: %w", err)
			}

			maxCount := 10
			if input.MaxCount != nil && *input.MaxCount > 0 {
				maxCount = *input.MaxCount
			}

			gitArgs := []string{"log"}
			if input.Oneline || input.Oneline == false && maxCount <= 20 {
				gitArgs = append(gitArgs, "--oneline")
			}
			gitArgs = append(gitArgs, fmt.Sprintf("-%d", maxCount))
			if input.FilePath != "" {
				gitArgs = append(gitArgs, "--", input.FilePath)
			}

			output, code, err := cfg.Operations.Exec(gitArgs, cwd)
			if err != nil {
				return resultErrf("git log failed: %w", err)
			}
			if code != 0 {
				return resultErrf("git log exited with code %d: %s", code, output)
			}

			output = strings.TrimSpace(output)
			if output == "" {
				return result("No commits found", nil)
			}
			return result(output, nil)
		},
	}
}
