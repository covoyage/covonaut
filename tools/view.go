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

// ViewOperations defines pluggable operations for the view tool.
type ViewOperations interface {
	Stat(path string) (os.FileInfo, error)
	ReadDir(path string) ([]os.DirEntry, error)
}

// DefaultViewOperations uses the local filesystem.
type DefaultViewOperations struct{}

func (d DefaultViewOperations) Stat(path string) (os.FileInfo, error) { return os.Stat(path) }
func (d DefaultViewOperations) ReadDir(path string) ([]os.DirEntry, error) {
	return os.ReadDir(path)
}

// ViewToolConfig configures the view tool.
type ViewToolConfig struct {
	Operations ViewOperations
	MaxDepth   int
	MaxEntries int
}

func (c *ViewToolConfig) defaults() {
	if c.Operations == nil {
		c.Operations = DefaultViewOperations{}
	}
	if c.MaxDepth <= 0 {
		c.MaxDepth = 3
	}
	if c.MaxEntries <= 0 {
		c.MaxEntries = 200
	}
}

// ViewToolInput is the JSON arguments for the view tool.
type ViewToolInput struct {
	Path  string `json:"path,omitempty"`
	Depth *int   `json:"depth,omitempty"`
}

// NewViewTool creates a directory tree viewing tool.
func NewViewTool(cwd string, cfg *ViewToolConfig) *agentcore.Tool {
	if cfg == nil {
		cfg = &ViewToolConfig{}
	}
	cfg.defaults()

	return &agentcore.Tool{
		Name: "view",
		Description: fmt.Sprintf("View directory structure as a tree. Returns a hierarchical listing with files and directories. "+
			"Max depth: %d, max entries: %d. Use this for exploring project structure.", cfg.MaxDepth, cfg.MaxEntries),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":  map[string]any{"type": "string", "description": "Directory to view (default: current directory)"},
				"depth": map[string]any{"type": "integer", "description": fmt.Sprintf("Maximum depth to traverse (default: %d)", cfg.MaxDepth)},
			},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var input ViewToolInput
			if err := json.Unmarshal(args, &input); err != nil {
				return resultErrf("invalid arguments: %w", err)
			}

			dirPath := resolveReadPath(input.Path, cwd)
			if dirPath == "" {
				dirPath = cwd
			}

			info, err := cfg.Operations.Stat(dirPath)
			if err != nil {
				return resultErrf("path not found: %s", input.Path)
			}

			maxDepth := cfg.MaxDepth
			if input.Depth != nil && *input.Depth > 0 {
				maxDepth = *input.Depth
			}

			// If path is a file, show file info.
			if !info.IsDir() {
				return result(fmt.Sprintf("%s (%s, %d bytes)", filepath.Base(dirPath), info.Mode(), info.Size()), nil)
			}

			var lines []string
			entries := 0
			var walk func(path string, prefix string, depth int) error
			walk = func(path string, prefix string, depth int) error {
				if depth > maxDepth {
					return nil
				}
				if entries >= cfg.MaxEntries {
					return fmt.Errorf("max_entries_reached")
				}

				entriesList, err := cfg.Operations.ReadDir(path)
				if err != nil {
					return nil
				}

				// Sort: dirs first, then files, alphabetically.
				sort.Slice(entriesList, func(i, j int) bool {
					if entriesList[i].IsDir() != entriesList[j].IsDir() {
						return entriesList[i].IsDir()
					}
					return strings.ToLower(entriesList[i].Name()) < strings.ToLower(entriesList[j].Name())
				})

				for i, entry := range entriesList {
					if entries >= cfg.MaxEntries {
						return fmt.Errorf("max_entries_reached")
					}

					isLast := i == len(entriesList)-1
					connector := "├── "
					if isLast {
						connector = "└── "
					}

					name := entry.Name()
					if entry.IsDir() {
						name += "/"
					}
					lines = append(lines, prefix+connector+name)
					entries++

					if entry.IsDir() {
						nextPrefix := prefix
						if isLast {
							nextPrefix += "    "
						} else {
							nextPrefix += "│   "
						}
						if err := walk(filepath.Join(path, entry.Name()), nextPrefix, depth+1); err != nil {
							if err.Error() == "max_entries_reached" {
								return err
							}
						}
					}
				}
				return nil
			}

			if err := walk(dirPath, "", 1); err != nil && err.Error() != "max_entries_reached" {
				return resultErrf("walk failed: %w", err)
			}

			output := filepath.Base(dirPath) + "/"
			if len(lines) > 0 {
				output += "\n" + strings.Join(lines, "\n")
			}
			if entries >= cfg.MaxEntries {
				output += fmt.Sprintf("\n\n[%d entries limit reached]", cfg.MaxEntries)
			}

			return result(output, nil)
		},
	}
}
