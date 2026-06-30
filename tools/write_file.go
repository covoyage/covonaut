package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

// WriteFileOperations defines pluggable filesystem operations for the write_file tool.
type WriteFileOperations interface {
	WriteFile(path string, content []byte) error
	ReadFile(path string) ([]byte, error)
	Stat(path string) (os.FileInfo, error)
	MkdirAll(path string, perm os.FileMode) error
}

// DefaultWriteFileOperations uses the local filesystem.
type DefaultWriteFileOperations struct{}

func (d DefaultWriteFileOperations) WriteFile(path string, content []byte) error {
	return os.WriteFile(path, content, 0644)
}

func (d DefaultWriteFileOperations) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func (d DefaultWriteFileOperations) Stat(path string) (os.FileInfo, error) {
	return os.Stat(path)
}

func (d DefaultWriteFileOperations) MkdirAll(path string, perm os.FileMode) error {
	return os.MkdirAll(path, perm)
}

// WriteFileToolConfig configures the write_file tool.
type WriteFileToolConfig struct {
	Operations WriteFileOperations
	MaxBytes   int64
}

func (c *WriteFileToolConfig) defaults() {
	if c.Operations == nil {
		c.Operations = DefaultWriteFileOperations{}
	}
	if c.MaxBytes <= 0 {
		c.MaxBytes = 100 * 1024 // 100KB default for write content
	}
}

// WriteFileToolInput is the JSON arguments for the write_file tool.
type WriteFileToolInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// WriteFileToolDetails carries write metadata.
type WriteFileToolDetails struct {
	BytesWritten int    `json:"bytes_written"`
	IsNewFile    bool   `json:"is_new_file"`
	SyntaxCheck  string `json:"syntax_check,omitempty"`
}

// NewWriteFileTool creates a write file tool.
func NewWriteFileTool(cwd string, cfg *WriteFileToolConfig) *agentcore.Tool {
	if cfg == nil {
		cfg = &WriteFileToolConfig{}
	}
	cfg.defaults()

	return &agentcore.Tool{
		Name: "write_file",
		Description: "Write content to a file. Creates the file if it doesn't exist, overwrites if it does. " +
			"Automatically creates parent directories. " +
			"For large files (>100KB), consider using edit or patch instead.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the file to write (relative or absolute)",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "Content to write to the file",
				},
			},
			"required": []any{"path", "content"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var input WriteFileToolInput
			if err := json.Unmarshal(args, &input); err != nil {
				return resultErrf("invalid arguments: %w", err)
			}

			if input.Path == "" {
				return resultErrf("path is required")
			}

			resolved := resolvePath(input.Path, cwd)

			// Check content size.
			contentBytes := []byte(input.Content)
			if int64(len(contentBytes)) > cfg.MaxBytes {
				return resultErrf("content exceeds maximum size of %s (got %s). Use edit or patch for large files.",
					FormatSize(cfg.MaxBytes), FormatSize(int64(len(contentBytes))))
			}

			// Ensure parent directory exists.
			parentDir := filepath.Dir(resolved)
			if err := cfg.Operations.MkdirAll(parentDir, 0755); err != nil {
				return resultErrf("failed to create parent directory: %w", err)
			}

			// Check if file already exists.
			_, statErr := cfg.Operations.Stat(resolved)
			isNewFile := os.IsNotExist(statErr)

			// Write the file.
			if err := cfg.Operations.WriteFile(resolved, contentBytes); err != nil {
				return resultErrf("failed to write file: %w", err)
			}

			// Syntax check for known file types.
			syntaxCheck := checkSyntax(resolved, input.Content)

			action := "Created"
			if !isNewFile {
				action = "Overwrote"
			}

			return result(
				fmt.Sprintf("%s %s (%s, %d bytes)", action, input.Path, syntaxCheck, len(contentBytes)),
				WriteFileToolDetails{
					BytesWritten: len(contentBytes),
					IsNewFile:    isNewFile,
					SyntaxCheck:  syntaxCheck,
				},
			)
		},
	}
}

// checkSyntax performs basic syntax validation for known file types.
// Returns a status string: "ok", "warning: <issue>", or "skipped".
func checkSyntax(path, content string) string {
	ext := strings.ToLower(filepath.Ext(path))

	switch ext {
	case ".json":
		var v any
		if err := json.Unmarshal([]byte(content), &v); err != nil {
			return fmt.Sprintf("JSON syntax error: %v", err)
		}
		return "JSON syntax ok"

	case ".go":
		// Basic Go syntax check: look for common issues.
		// Full parsing would require go/parser which adds dependency.
		// For now, do basic brace balance check.
		if !checkBraceBalance(content) {
			return "Go syntax warning: brace mismatch"
		}
		return "Go syntax ok"

	case ".yaml", ".yml":
		// Basic YAML check: indentation consistency.
		if err := checkYAMLBasic(content); err != nil {
			return fmt.Sprintf("YAML syntax warning: %v", err)
		}
		return "YAML syntax ok"

	case ".toml":
		// Basic TOML check: look for invalid section headers.
		if err := checkTOMLBasic(content); err != nil {
			return fmt.Sprintf("TOML syntax warning: %v", err)
		}
		return "TOML syntax ok"

	default:
		return "skipped"
	}
}

func checkBraceBalance(content string) bool {
	var stack int
	inString := false
	var stringChar rune

	for _, r := range content {
		if inString {
			if r == stringChar {
				inString = false
			}
			continue
		}
		if r == '"' || r == '\'' || r == '`' {
			inString = true
			stringChar = r
			continue
		}
		switch r {
		case '{', '(', '[':
			stack++
		case '}', ')', ']':
			stack--
			if stack < 0 {
				return false
			}
		}
	}
	return stack == 0
}

func checkYAMLBasic(content string) error {
	lines := strings.Split(content, "\n")
	var prevIndent int
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent%2 != 0 && indent > 0 {
			// YAML allows any indentation, but inconsistent indentation is suspicious.
			// Just warn if indent changes by 1 space.
			if abs(indent-prevIndent) == 1 {
				return fmt.Errorf("line %d: suspicious 1-space indentation", i+1)
			}
		}
		prevIndent = indent
	}
	return nil
}

func checkTOMLBasic(content string) error {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		// Check section headers.
		if strings.HasPrefix(trimmed, "[") {
			if !strings.HasSuffix(trimmed, "]") {
				return fmt.Errorf("line %d: unclosed section header", i+1)
			}
		}
	}
	return nil
}

func abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}
