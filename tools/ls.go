package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

// LsOperations defines pluggable filesystem operations for the ls tool.
type LsOperations interface {
	Exists(path string) bool
	Stat(path string) (os.FileInfo, error)
	ReadDir(path string) ([]os.DirEntry, error)
}

// DefaultLsOperations uses the local filesystem.
type DefaultLsOperations struct{}

func (d DefaultLsOperations) Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
func (d DefaultLsOperations) Stat(path string) (os.FileInfo, error) { return os.Stat(path) }
func (d DefaultLsOperations) ReadDir(path string) ([]os.DirEntry, error) {
	return os.ReadDir(path)
}

// LsToolConfig configures the ls tool.
type LsToolConfig struct {
	Operations LsOperations
	MaxBytes   int64
	Limit      int
}

func (c *LsToolConfig) defaults() {
	if c.Operations == nil {
		c.Operations = DefaultLsOperations{}
	}
	if c.MaxBytes <= 0 {
		c.MaxBytes = 50 * 1024
	}
	if c.Limit <= 0 {
		c.Limit = 500
	}
}

// LsToolInput is the JSON arguments for the ls tool.
type LsToolInput struct {
	Path  string `json:"path,omitempty"`
	Limit *int   `json:"limit,omitempty"`
}

// LsToolDetails carries truncation metadata.
type LsToolDetails struct {
	Truncation        *TruncationResult `json:"truncation,omitempty"`
	EntryLimitReached *int              `json:"entry_limit_reached,omitempty"`
}

// NewLsTool creates a directory listing tool.
func NewLsTool(cwd string, cfg *LsToolConfig) *agentcore.Tool {
	if cfg == nil {
		cfg = &LsToolConfig{}
	}
	cfg.defaults()

	return &agentcore.Tool{
		Name:        "ls",
		Description: fmt.Sprintf("List directory contents. Returns entries sorted alphabetically, with '/' suffix for directories. Includes dotfiles. Output is truncated to %d entries or %s (whichever is hit first).", cfg.Limit, FormatSize(cfg.MaxBytes)),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":  map[string]any{"type": "string", "description": "Directory to list (default: current directory)"},
				"limit": map[string]any{"type": "integer", "description": fmt.Sprintf("Maximum number of entries to return (default: %d)", cfg.Limit)},
			},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var input LsToolInput
			if err := json.Unmarshal(args, &input); err != nil {
				return resultErrf("invalid arguments: %w", err)
			}

			dirPath := resolveReadPath(input.Path, cwd)
			if dirPath == "" {
				dirPath = cwd
			}

			if !cfg.Operations.Exists(dirPath) {
				return resultErrf("path not found: %s", input.Path)
			}

			info, err := cfg.Operations.Stat(dirPath)
			if err != nil {
				return resultErrf("cannot stat path: %w", err)
			}
			if !info.IsDir() {
				return resultErrf("not a directory: %s", input.Path)
			}

			entries, err := cfg.Operations.ReadDir(dirPath)
			if err != nil {
				return resultErrf("cannot read directory: %w", err)
			}

			// Sort alphabetically, case-insensitive.
			sort.Slice(entries, func(i, j int) bool {
				return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
			})

			limit := cfg.Limit
			if input.Limit != nil && *input.Limit > 0 {
				limit = *input.Limit
			}

			var results []string
			entryLimitReached := false
			for _, entry := range entries {
				if len(results) >= limit {
					entryLimitReached = true
					break
				}
				name := entry.Name()
				if entry.IsDir() {
					name += "/"
				}
				results = append(results, name)
			}

			if len(results) == 0 {
				return result("(empty directory)", nil)
			}

			rawOutput := strings.Join(results, "\n")
			truncation := TruncateHead(rawOutput, TruncationOptions{MaxBytes: int(cfg.MaxBytes), MaxLines: 1<<31 - 1})
			output := truncation.Content

			var details LsToolDetails
			var notices []string
			if entryLimitReached {
				notices = append(notices, fmt.Sprintf("%d entries limit reached", limit))
				details.EntryLimitReached = &limit
			}
			if truncation.Truncated {
				notices = append(notices, fmt.Sprintf("%s limit reached", FormatSize(cfg.MaxBytes)))
				details.Truncation = &truncation
			}
			if len(notices) > 0 {
				output += fmt.Sprintf("\n\n[%s]", strings.Join(notices, ". "))
			}

			if details.Truncation == nil && details.EntryLimitReached == nil {
				return result(output, nil)
			}
			return result(output, details)
		},
	}
}
