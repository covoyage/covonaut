package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

// GlobOperations defines pluggable operations for the glob tool.
type GlobOperations interface {
	Glob(pattern string, cwd string, limit int) ([]string, error)
}

// DefaultGlobOperations uses the local filesystem.
type DefaultGlobOperations struct{}

func (d DefaultGlobOperations) Glob(pattern string, cwd string, limit int) ([]string, error) {
	var results []string
	seen := make(map[string]bool)

	// Support both absolute and relative patterns.
	searchDir := cwd
	basePattern := pattern

	if filepath.IsAbs(pattern) {
		searchDir = filepath.Dir(pattern)
		basePattern = filepath.Base(pattern)
	}

	err := filepath.WalkDir(searchDir, func(path string, info os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			// Skip common directories.
			base := filepath.Base(path)
			if base == ".git" || base == "node_modules" || base == "vendor" || base == ".next" || base == "dist" || base == "build" {
				return filepath.SkipDir
			}
			return nil
		}

		matched, _ := filepath.Match(basePattern, filepath.Base(path))
		if !matched {
			// Try matching against relative path from searchDir.
			rel, _ := filepath.Rel(searchDir, path)
			matched, _ = filepath.Match(pattern, rel)
		}

		if matched {
			rel, _ := filepath.Rel(cwd, path)
			if !seen[rel] {
				results = append(results, rel)
				seen[rel] = true
				if len(results) >= limit {
					return filepath.SkipDir
				}
			}
		}
		return nil
	})

	return results, err
}

// GlobToolConfig configures the glob tool.
type GlobToolConfig struct {
	Operations GlobOperations
	Limit      int
}

func (c *GlobToolConfig) defaults() {
	if c.Operations == nil {
		c.Operations = DefaultGlobOperations{}
	}
	if c.Limit <= 0 {
		c.Limit = 1000
	}
}

// GlobToolInput is the JSON arguments for the glob tool.
type GlobToolInput struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path,omitempty"`
	Limit   *int   `json:"limit,omitempty"`
}

// NewGlobTool creates a glob pattern matching tool.
func NewGlobTool(cwd string, cfg *GlobToolConfig) *agentcore.Tool {
	if cfg == nil {
		cfg = &GlobToolConfig{}
	}
	cfg.defaults()

	return &agentcore.Tool{
		Name: "glob",
		Description: fmt.Sprintf("Find files matching a glob pattern. Returns matching file paths relative to the search directory. "+
			"Supports standard glob syntax: * matches any characters, ? matches single character. "+
			"Output limited to %d results.", cfg.Limit),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string", "description": "Glob pattern, e.g. '*.go', '*.test.ts', 'Dockerfile*'"},
				"path":    map[string]any{"type": "string", "description": "Directory to search in (default: current directory)"},
				"limit":   map[string]any{"type": "integer", "description": fmt.Sprintf("Maximum results (default: %d)", cfg.Limit)},
			},
			"required": []any{"pattern"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var input GlobToolInput
			if err := json.Unmarshal(args, &input); err != nil {
				return resultErrf("invalid arguments: %w", err)
			}

			if input.Pattern == "" {
				return resultErrf("pattern is required")
			}

			searchPath := resolveReadPath(input.Path, cwd)
			if searchPath == "" {
				searchPath = cwd
			}

			limit := cfg.Limit
			if input.Limit != nil && *input.Limit > 0 {
				limit = *input.Limit
			}

			results, err := cfg.Operations.Glob(input.Pattern, searchPath, limit)
			if err != nil {
				return resultErrf("glob failed: %w", err)
			}

			if len(results) == 0 {
				return result("No files found matching pattern", nil)
			}

			sort.Strings(results)
			output := strings.Join(results, "\n")
			if len(results) >= limit {
				output += fmt.Sprintf("\n\n[%d results limit reached]", limit)
			}

			return result(output, nil)
		},
	}
}
