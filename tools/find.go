package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

// FindOperations defines pluggable operations for the find tool.
type FindOperations interface {
	Exists(path string) bool
	Glob(pattern string, cwd string, ignore []string, limit int) ([]string, error)
}

// DefaultFindOperations uses the local filesystem.
type DefaultFindOperations struct{}

func (d DefaultFindOperations) Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
func (d DefaultFindOperations) Glob(pattern string, cwd string, ignore []string, limit int) ([]string, error) {
	var results []string
	err := filepath.WalkDir(cwd, func(path string, info os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			for _, i := range ignore {
				if matched, _ := filepath.Match(i, filepath.Base(path)); matched {
					return filepath.SkipDir
				}
			}
			return nil
		}
		matched, _ := filepath.Match(pattern, filepath.Base(path))
		if !matched {
			// Try doublestar-style matching for **/ patterns.
			if strings.Contains(pattern, "**") {
				matched, _ = filepath.Match(pattern, path)
			}
		}
		if matched {
			results = append(results, path)
			if len(results) >= limit {
				return filepath.SkipDir
			}
		}
		return nil
	})
	return results, err
}

// FindToolConfig configures the find tool.
type FindToolConfig struct {
	Operations FindOperations
	MaxBytes   int64
	Limit      int
}

func (c *FindToolConfig) defaults() {
	if c.Operations == nil {
		c.Operations = DefaultFindOperations{}
	}
	if c.MaxBytes <= 0 {
		c.MaxBytes = 50 * 1024
	}
	if c.Limit <= 0 {
		c.Limit = 1000
	}
}

// FindToolInput is the JSON arguments for the find tool.
type FindToolInput struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path,omitempty"`
	Limit   *int   `json:"limit,omitempty"`
}

// FindToolDetails carries truncation metadata.
type FindToolDetails struct {
	Truncation         *TruncationResult `json:"truncation,omitempty"`
	ResultLimitReached *int              `json:"result_limit_reached,omitempty"`
}

// NewFindTool creates a file search tool.
func NewFindTool(cwd string, cfg *FindToolConfig) *agentcore.Tool {
	if cfg == nil {
		cfg = &FindToolConfig{}
	}
	cfg.defaults()

	return &agentcore.Tool{
		Name: "find",
		Description: fmt.Sprintf("Search for files by glob pattern. Returns matching file paths relative to the search directory. "+
			"Respects .gitignore. Output is truncated to %d results or %s (whichever is hit first).", cfg.Limit, FormatSize(cfg.MaxBytes)),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string", "description": "Glob pattern to match files, e.g. '*.ts', '**/*.json', or 'src/**/*.spec.ts'"},
				"path":    map[string]any{"type": "string", "description": "Directory to search in (default: current directory)"},
				"limit":   map[string]any{"type": "integer", "description": fmt.Sprintf("Maximum number of results (default: %d)", cfg.Limit)},
			},
			"required": []any{"pattern"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var input FindToolInput
			if err := json.Unmarshal(args, &input); err != nil {
				return resultErrf("invalid arguments: %w", err)
			}

			searchPath := resolveReadPath(input.Path, cwd)
			if searchPath == "" {
				searchPath = cwd
			}

			if !cfg.Operations.Exists(searchPath) {
				return resultErrf("path not found: %s", input.Path)
			}

			limit := cfg.Limit
			if input.Limit != nil && *input.Limit > 0 {
				limit = *input.Limit
			}

			// Try fd first.
			if fdPath, err := exec.LookPath("fd"); err == nil {
				return runFd(ctx, fdPath, searchPath, input.Pattern, limit, cfg)
			}

			// Fallback to filepath.Glob.
			return runGlob(searchPath, input.Pattern, limit, cfg)
		},
	}
}

func runFd(ctx context.Context, fdPath, searchPath, pattern string, limit int, cfg *FindToolConfig) (any, error) {
	args := []string{"--glob", "--color=never", "--hidden", "--max-results", fmt.Sprintf("%d", limit)}

	// Collect .gitignore files.
	gitignoreFiles := []string{}
	filepath.WalkDir(searchPath, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if filepath.Base(path) == ".gitignore" {
			gitignoreFiles = append(gitignoreFiles, path)
		}
		return nil
	})
	for _, gi := range gitignoreFiles {
		args = append(args, "--ignore-file", gi)
	}
	args = append(args, pattern, searchPath)

	cmd := exec.CommandContext(ctx, fdPath, args...)
	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && len(exitErr.Stderr) > 0 {
			return resultErrf("fd error: %s", string(exitErr.Stderr))
		}
		return runGlob(searchPath, pattern, limit, cfg)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	return formatFindResults(lines, searchPath, limit, cfg)
}

func runGlob(searchPath, pattern string, limit int, cfg *FindToolConfig) (any, error) {
	results, err := cfg.Operations.Glob(pattern, searchPath, []string{"node_modules", ".git"}, limit)
	if err != nil {
		return resultErrf("search failed: %w", err)
	}
	return formatFindResults(results, searchPath, limit, cfg)
}

func formatFindResults(results []string, searchPath string, limit int, cfg *FindToolConfig) (any, error) {
	if len(results) == 0 {
		return result("No files found matching pattern", nil)
	}

	var relativized []string
	for _, line := range results {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		hadTrailingSlash := strings.HasSuffix(line, "/") || strings.HasSuffix(line, string(filepath.Separator))
		relPath := line
		if strings.HasPrefix(line, searchPath) {
			relPath = line[len(searchPath)+1:]
		} else {
			relPath, _ = filepath.Rel(searchPath, line)
		}
		if hadTrailingSlash && !strings.HasSuffix(relPath, "/") {
			relPath += "/"
		}
		relativized = append(relativized, filepath.ToSlash(relPath))
	}

	rawOutput := strings.Join(relativized, "\n")
	truncation := TruncateHead(rawOutput, TruncationOptions{MaxBytes: int(cfg.MaxBytes), MaxLines: 1<<31 - 1})
	output := truncation.Content

	var details FindToolDetails
	var notices []string
	if len(results) >= limit {
		notices = append(notices, fmt.Sprintf("%d results limit reached", limit))
		details.ResultLimitReached = &limit
	}
	if truncation.Truncated {
		notices = append(notices, fmt.Sprintf("%s limit reached", FormatSize(cfg.MaxBytes)))
		details.Truncation = &truncation
	}
	if len(notices) > 0 {
		output += fmt.Sprintf("\n\n[%s]", strings.Join(notices, ". "))
	}

	return result(output, details)
}
