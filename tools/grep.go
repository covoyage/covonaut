package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

// GrepOperations defines pluggable operations for the grep tool.
type GrepOperations interface {
	IsDirectory(path string) (bool, error)
	ReadFile(path string) (string, error)
}

// DefaultGrepOperations uses the local filesystem.
type DefaultGrepOperations struct{}

func (d DefaultGrepOperations) IsDirectory(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return info.IsDir(), nil
}
func (d DefaultGrepOperations) ReadFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// GrepToolConfig configures the grep tool.
type GrepToolConfig struct {
	Operations    GrepOperations
	MaxBytes      int64
	MaxLineLength int
	Limit         int
}

func (c *GrepToolConfig) defaults() {
	if c.Operations == nil {
		c.Operations = DefaultGrepOperations{}
	}
	if c.MaxBytes <= 0 {
		c.MaxBytes = 50 * 1024
	}
	if c.MaxLineLength <= 0 {
		c.MaxLineLength = 500
	}
	if c.Limit <= 0 {
		c.Limit = 100
	}
}

// GrepToolInput is the JSON arguments for the grep tool.
type GrepToolInput struct {
	Pattern    string `json:"pattern"`
	Path       string `json:"path,omitempty"`
	Glob       string `json:"glob,omitempty"`
	IgnoreCase bool   `json:"ignoreCase,omitempty"`
	Literal    bool   `json:"literal,omitempty"`
	Context    int    `json:"context,omitempty"`
	Limit      *int   `json:"limit,omitempty"`
}

// GrepToolDetails carries truncation metadata.
type GrepToolDetails struct {
	Truncation        *TruncationResult `json:"truncation,omitempty"`
	MatchLimitReached *int              `json:"match_limit_reached,omitempty"`
	LinesTruncated    bool              `json:"lines_truncated,omitempty"`
}

// NewGrepTool creates a file content search tool.
func NewGrepTool(cwd string, cfg *GrepToolConfig) *agentcore.Tool {
	if cfg == nil {
		cfg = &GrepToolConfig{}
	}
	cfg.defaults()

	return &agentcore.Tool{
		Name: "grep",
		Description: fmt.Sprintf("Search file contents for a pattern. Returns matching lines with file paths and line numbers. "+
			"Respects .gitignore. Output is truncated to %d matches or %s (whichever is hit first). "+
			"Long lines are truncated to %d chars.", cfg.Limit, FormatSize(cfg.MaxBytes), cfg.MaxLineLength),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern":    map[string]any{"type": "string", "description": "Search pattern (regex or literal string)"},
				"path":       map[string]any{"type": "string", "description": "Directory or file to search (default: current directory)"},
				"glob":       map[string]any{"type": "string", "description": "Filter files by glob pattern, e.g. '*.ts' or '**/*.spec.ts'"},
				"ignoreCase": map[string]any{"type": "boolean", "description": "Case-insensitive search (default: false)"},
				"literal":    map[string]any{"type": "boolean", "description": "Treat pattern as literal string instead of regex (default: false)"},
				"context":    map[string]any{"type": "integer", "description": "Number of lines to show before and after each match (default: 0)"},
				"limit":      map[string]any{"type": "integer", "description": fmt.Sprintf("Maximum number of matches to return (default: %d)", cfg.Limit)},
			},
			"required": []any{"pattern"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var input GrepToolInput
			if err := json.Unmarshal(args, &input); err != nil {
				return resultErrf("invalid arguments: %w", err)
			}

			searchPath := resolveReadPath(input.Path, cwd)
			if searchPath == "" {
				searchPath = cwd
			}

			isDir, err := cfg.Operations.IsDirectory(searchPath)
			if err != nil {
				return resultErrf("path not found: %s", input.Path)
			}

			limit := cfg.Limit
			if input.Limit != nil && *input.Limit > 0 {
				limit = *input.Limit
			}

			// Try ripgrep first.
			if rgPath, err := exec.LookPath("rg"); err == nil {
				return runRipgrep(ctx, rgPath, searchPath, input, limit, isDir, cfg)
			}

			// Fallback to Go regexp.
			return runGoGrep(ctx, searchPath, input, limit, isDir, cfg)
		},
	}
}

func runRipgrep(ctx context.Context, rgPath, searchPath string, input GrepToolInput, limit int, isDir bool, cfg *GrepToolConfig) (any, error) {
	args := []string{"--json", "--line-number", "--color=never", "--hidden"}
	if input.IgnoreCase {
		args = append(args, "--ignore-case")
	}
	if input.Literal {
		args = append(args, "--fixed-strings")
	}
	if input.Glob != "" {
		args = append(args, "--glob", input.Glob)
	}
	args = append(args, input.Pattern, searchPath)

	cmd := exec.CommandContext(ctx, rgPath, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return runGoGrep(ctx, searchPath, input, limit, isDir, cfg)
	}

	if err := cmd.Start(); err != nil {
		return runGoGrep(ctx, searchPath, input, limit, isDir, cfg)
	}

	var matches []struct {
		filePath string
		lineNum  int
	}
	scanner := bufio.NewScanner(stdout)
	matchCount := 0
	for scanner.Scan() {
		if matchCount >= limit {
			cmd.Process.Kill()
			break
		}
		var event map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		if event["type"] != "match" {
			continue
		}
		data, ok := event["data"].(map[string]any)
		if !ok {
			continue
		}
		pathText, _ := data["path"].(map[string]any)["text"].(string)
		lineNum, _ := data["line_number"].(float64)
		if pathText != "" {
			matches = append(matches, struct {
				filePath string
				lineNum  int
			}{pathText, int(lineNum)})
			matchCount++
		}
	}

	cmd.Wait()

	if len(matches) == 0 {
		return result("No matches found", nil)
	}

	return formatGrepMatches(matches, searchPath, input, limit, isDir, cfg)
}

func runGoGrep(ctx context.Context, searchPath string, input GrepToolInput, limit int, isDir bool, cfg *GrepToolConfig) (any, error) {
	var pattern string
	if input.Literal {
		pattern = regexp.QuoteMeta(input.Pattern)
	} else {
		pattern = input.Pattern
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return resultErrf("invalid pattern: %w", err)
	}
	if input.IgnoreCase {
		re, err = regexp.Compile("(?i)" + pattern)
		if err != nil {
			return resultErrf("invalid pattern: %w", err)
		}
	}

	var matches []struct {
		filePath string
		lineNum  int
	}
	matchCount := 0
	if isDir {
		filepath.WalkDir(searchPath, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if input.Glob != "" {
				matched, _ := filepath.Match(input.Glob, filepath.Base(path))
				if !matched {
					return nil
				}
			}
			content, err := cfg.Operations.ReadFile(path)
			if err != nil {
				return nil
			}
			lines := strings.Split(content, "\n")
			for i, line := range lines {
				if matchCount >= limit {
					return filepath.SkipDir
				}
				if re.MatchString(line) {
					matches = append(matches, struct {
						filePath string
						lineNum  int
					}{path, i + 1})
					matchCount++
				}
			}
			return nil
		})
	} else {
		content, err := cfg.Operations.ReadFile(searchPath)
		if err != nil {
			return resultErrf("failed to read file: %w", err)
		}
		lines := strings.Split(content, "\n")
		for i, line := range lines {
			if matchCount >= limit {
				break
			}
			if re.MatchString(line) {
				matches = append(matches, struct {
					filePath string
					lineNum  int
				}{searchPath, i + 1})
				matchCount++
			}
		}
	}

	if len(matches) == 0 {
		return result("No matches found", nil)
	}

	return formatGrepMatches(matches, searchPath, input, limit, isDir, cfg)
}

func formatGrepMatches(matches []struct {
	filePath string
	lineNum  int
}, searchPath string, input GrepToolInput, limit int, isDir bool, cfg *GrepToolConfig) (any, error) {
	fileCache := make(map[string][]string)
	getLines := func(filePath string) []string {
		lines, ok := fileCache[filePath]
		if !ok {
			content, err := cfg.Operations.ReadFile(filePath)
			if err != nil {
				return nil
			}
			lines = strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
			fileCache[filePath] = lines
		}
		return lines
	}

	formatPath := func(filePath string) string {
		if isDir {
			rel, err := filepath.Rel(searchPath, filePath)
			if err == nil && !strings.HasPrefix(rel, "..") {
				return filepath.ToSlash(rel)
			}
		}
		return filepath.Base(filePath)
	}

	var outputLines []string
	linesTruncated := false
	for _, match := range matches {
		lines := getLines(match.filePath)
		if lines == nil {
			outputLines = append(outputLines, fmt.Sprintf("%s:%d: (unable to read file)", formatPath(match.filePath), match.lineNum))
			continue
		}

		start := match.lineNum
		end := match.lineNum
		if input.Context > 0 {
			start = match.lineNum - input.Context
			if start < 1 {
				start = 1
			}
			end = match.lineNum + input.Context
			if end > len(lines) {
				end = len(lines)
			}
		}

		for i := start; i <= end; i++ {
			lineText := ""
			if i-1 < len(lines) {
				lineText = lines[i-1]
			}
			truncated, wasTruncated := TruncateLine(lineText, cfg.MaxLineLength)
			if wasTruncated {
				linesTruncated = true
			}
			if i == match.lineNum {
				outputLines = append(outputLines, fmt.Sprintf("%s:%d: %s", formatPath(match.filePath), i, truncated))
			} else {
				outputLines = append(outputLines, fmt.Sprintf("%s-%d- %s", formatPath(match.filePath), i, truncated))
			}
		}
	}

	rawOutput := strings.Join(outputLines, "\n")
	truncation := TruncateHead(rawOutput, TruncationOptions{MaxBytes: int(cfg.MaxBytes), MaxLines: 1<<31 - 1})
	output := truncation.Content

	var details GrepToolDetails
	var notices []string
	if len(matches) >= limit {
		notices = append(notices, fmt.Sprintf("%d matches limit reached", limit))
		details.MatchLimitReached = &limit
	}
	if truncation.Truncated {
		notices = append(notices, fmt.Sprintf("%s limit reached", FormatSize(cfg.MaxBytes)))
		details.Truncation = &truncation
	}
	if linesTruncated {
		notices = append(notices, fmt.Sprintf("some lines truncated to %d chars", cfg.MaxLineLength))
		details.LinesTruncated = true
	}
	if len(notices) > 0 {
		output += fmt.Sprintf("\n\n[%s]", strings.Join(notices, ". "))
	}

	return result(output, details)
}
