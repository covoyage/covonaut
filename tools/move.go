package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/covoyage/covonaut/agentcore"
)

// MoveOperations defines pluggable operations for the move tool.
type MoveOperations interface {
	Stat(path string) (os.FileInfo, error)
	Rename(oldPath, newPath string) error
	MkdirAll(path string, perm os.FileMode) error
}

// DefaultMoveOperations uses the local filesystem.
type DefaultMoveOperations struct{}

func (d DefaultMoveOperations) Stat(path string) (os.FileInfo, error) { return os.Stat(path) }
func (d DefaultMoveOperations) Rename(oldPath, newPath string) error {
	return os.Rename(oldPath, newPath)
}
func (d DefaultMoveOperations) MkdirAll(path string, perm os.FileMode) error {
	return os.MkdirAll(path, perm)
}

// MoveToolConfig configures the move tool.
type MoveToolConfig struct {
	Operations MoveOperations
}

func (c *MoveToolConfig) defaults() {
	if c.Operations == nil {
		c.Operations = DefaultMoveOperations{}
	}
}

// MoveToolInput is the JSON arguments for the move tool.
type MoveToolInput struct {
	Source string `json:"source"`
	Dest   string `json:"dest"`
}

// NewMoveTool creates a file/directory move/rename tool.
func NewMoveTool(cwd string, cfg *MoveToolConfig) *agentcore.Tool {
	if cfg == nil {
		cfg = &MoveToolConfig{}
	}
	cfg.defaults()

	return &agentcore.Tool{
		Name: "move",
		Description: "Move or rename a file or directory. The destination parent directory is created automatically if it doesn't exist. " +
			"If destination exists, it will be overwritten.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"source": map[string]any{
					"type":        "string",
					"description": "Source file or directory path",
				},
				"dest": map[string]any{
					"type":        "string",
					"description": "Destination path",
				},
			},
			"required": []any{"source", "dest"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var input MoveToolInput
			if err := json.Unmarshal(args, &input); err != nil {
				return resultErrf("invalid arguments: %w", err)
			}

			if input.Source == "" {
				return resultErrf("source is required")
			}
			if input.Dest == "" {
				return resultErrf("dest is required")
			}

			sourcePath := resolvePath(input.Source, cwd)
			destPath := resolvePath(input.Dest, cwd)

			// Verify source exists.
			_, err := cfg.Operations.Stat(sourcePath)
			if err != nil {
				if os.IsNotExist(err) {
					return resultErrf("source not found: %s", input.Source)
				}
				return resultErrf("cannot stat source: %w", err)
			}

			// Ensure destination parent exists.
			parentDir := filepath.Dir(destPath)
			if err := cfg.Operations.MkdirAll(parentDir, 0755); err != nil {
				return resultErrf("failed to create destination parent directory: %w", err)
			}

			// Perform move.
			if err := cfg.Operations.Rename(sourcePath, destPath); err != nil {
				return resultErrf("failed to move: %w", err)
			}

			return result(fmt.Sprintf("Moved %s -> %s", input.Source, input.Dest), nil)
		},
	}
}
