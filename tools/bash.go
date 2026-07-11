package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unicode"

	"github.com/covoyage/covonaut/agentcore"
)

// BashOperations defines pluggable operations for the bash tool.
type BashOperations interface {
	Exec(command string, cwd string, env map[string]string, timeoutSecs *int, onData func(data []byte)) (int, error)
}

// DefaultBashOperations uses the local shell.
type DefaultBashOperations struct{}

func (d DefaultBashOperations) Exec(command string, cwd string, env map[string]string, timeoutSecs *int, onData func(data []byte)) (int, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}

	cmd := exec.Command(shell, "-c", command)
	cmd.Dir = cwd
	if env != nil {
		cmd.Env = os.Environ()
		for k, v := range env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return -1, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return -1, err
	}

	if err := cmd.Start(); err != nil {
		return -1, err
	}

	// Set up timeout.
	var timer *time.Timer
	if timeoutSecs != nil && *timeoutSecs > 0 {
		timer = time.AfterFunc(time.Duration(*timeoutSecs)*time.Second, func() {
			if cmd.Process != nil {
				killProcessTree(cmd.Process.Pid)
			}
		})
	}

	// Stream output.
	var wg sync.WaitGroup
	readPipe := func(pipe *bufio.Reader) {
		defer wg.Done()
		for {
			line, err := pipe.ReadBytes('\n')
			if len(line) > 0 {
				onData(line)
			}
			if err != nil {
				break
			}
		}
	}

	wg.Add(2)
	go readPipe(bufio.NewReader(stdout))
	go readPipe(bufio.NewReader(stderr))
	wg.Wait()

	if timer != nil {
		timer.Stop()
	}

	err = cmd.Wait()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return -1, err
	}
	return 0, nil
}

func killProcessTree(pid int) {
	// Try to kill the process group.
	syscall.Kill(-pid, syscall.SIGKILL)
	// Fallback: kill the process itself.
	syscall.Kill(pid, syscall.SIGKILL)
}

func stripAnsi(text string) string {
	var b []byte
	runes := []rune(text)
	i := 0
	for i < len(runes) {
		if runes[i] == '\x1b' && i+1 < len(runes) && runes[i+1] == '[' {
			i += 2
			for i < len(runes) && ((runes[i] >= '0' && runes[i] <= '9') || runes[i] == ';' || runes[i] == '?') {
				i++
			}
			if i < len(runes) && ((runes[i] >= 'A' && runes[i] <= 'Z') || (runes[i] >= 'a' && runes[i] <= 'z')) {
				i++
			}
			continue
		}
		b = append(b, string(runes[i])...)
		i++
	}
	return string(b)
}

func sanitizeBinaryOutput(text string) string {
	var builder []rune
	for _, r := range text {
		if unicode.Is(unicode.C, r) && r != '\n' && r != '\r' && r != '\t' {
			builder = append(builder, '\uFFFD')
		} else {
			builder = append(builder, r)
		}
	}
	return string(builder)
}

// BashToolConfig configures the bash tool.
type BashToolConfig struct {
	Operations BashOperations
	MaxBytes   int64
	MaxLines   int64

	// AllowList restricts commands to those matching one of the given
	// prefixes (word-boundary match, e.g. "git" allows "git status" but
	// not "github"). Empty means all commands are allowed (subject to
	// BlockList). The check is applied to each sub-command split on
	// &&, ||, ;, |. See commandAllowed for limitations.
	AllowList []string

	// BlockList denies commands matching one of the given prefixes.
	// BlockList is evaluated before AllowList and wins on conflict.
	BlockList []string
}

func (c *BashToolConfig) defaults() {
	if c.Operations == nil {
		c.Operations = DefaultBashOperations{}
	}
	if c.MaxBytes <= 0 {
		c.MaxBytes = 50 * 1024
	}
	if c.MaxLines <= 0 {
		c.MaxLines = 2000
	}
}

// BashToolInput is the JSON arguments for the bash tool.
type BashToolInput struct {
	Command string `json:"command"`
	Timeout *int   `json:"timeout,omitempty"`
}

// BashToolDetails carries truncation metadata.
type BashToolDetails struct {
	Truncation     *TruncationResult `json:"truncation,omitempty"`
	FullOutputPath string            `json:"full_output_path,omitempty"`
}

// NewBashTool creates a shell execution tool.
func NewBashTool(cwd string, cfg *BashToolConfig) *agentcore.Tool {
	if cfg == nil {
		cfg = &BashToolConfig{}
	}
	cfg.defaults()

	return &agentcore.Tool{
		Name: "bash",
		Description: fmt.Sprintf("Execute a bash command in the current working directory. Returns stdout and stderr. "+
			"Output is truncated to last %d lines or %s (whichever is hit first). "+
			"If truncated, full output is saved to a temp file. Optionally provide a timeout in seconds.", cfg.MaxLines, FormatSize(cfg.MaxBytes)),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{"type": "string", "description": "Bash command to execute"},
				"timeout": map[string]any{"type": "integer", "description": "Timeout in seconds (optional, no default timeout)"},
			},
			"required": []any{"command"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var input BashToolInput
			if err := json.Unmarshal(args, &input); err != nil {
				return resultErrf("invalid arguments: %w", err)
			}

			if input.Command == "" {
				return resultErrf("command is required")
			}

			if err := commandAllowed(input.Command, cfg.AllowList, cfg.BlockList); err != nil {
				return resultErrf("command not allowed: %s", err)
			}

			var chunks [][]byte
			var totalBytes int
			var tempFile *os.File
			var tempFilePath string

			onData := func(data []byte) {
				totalBytes += len(data)
				chunks = append(chunks, data)

				// Start writing to temp file once output exceeds threshold.
				if totalBytes > int(cfg.MaxBytes) && tempFile == nil {
					tempFile, _ = os.CreateTemp("", "covonaut-bash-*.log")
					if tempFile != nil {
						tempFilePath = tempFile.Name()
						for _, c := range chunks {
							tempFile.Write(c)
						}
					}
				}
				if tempFile != nil {
					tempFile.Write(data)
				}

				// Keep rolling buffer of recent output.
				maxChunksBytes := int(cfg.MaxBytes) * 2
				chunksBytes := 0
				for _, c := range chunks {
					chunksBytes += len(c)
				}
				for chunksBytes > maxChunksBytes && len(chunks) > 1 {
					chunksBytes -= len(chunks[0])
					chunks = chunks[1:]
				}
			}

			exitCode, err := cfg.Operations.Exec(input.Command, cwd, nil, input.Timeout, onData)

			if tempFile != nil {
				tempFile.Close()
			}

			// Combine rolling buffer.
			var fullOutput []byte
			for _, c := range chunks {
				fullOutput = append(fullOutput, c...)
			}
			outputText := string(fullOutput)

			outputText = stripAnsi(outputText)
			outputText = sanitizeBinaryOutput(outputText)

			// Apply tail truncation.
			truncation := TruncateTail(outputText, TruncationOptions{
				MaxLines: int(cfg.MaxLines),
				MaxBytes: int(cfg.MaxBytes),
			})

			var details BashToolDetails
			resultText := truncation.Content
			if resultText == "" {
				resultText = "(no output)"
			}

			if truncation.Truncated {
				details.Truncation = &truncation
				details.FullOutputPath = tempFilePath
				startLine := truncation.TotalLines - truncation.OutputLines + 1
				endLine := truncation.TotalLines
				if truncation.LastLinePartial {
					resultText += fmt.Sprintf("\n\n[Showing last %s of line %d. Full output: %s]",
						FormatSize(int64(truncation.OutputBytes)), endLine, tempFilePath)
				} else if truncation.TruncatedBy == "lines" {
					resultText += fmt.Sprintf("\n\n[Showing lines %d-%d of %d. Full output: %s]",
						startLine, endLine, truncation.TotalLines, tempFilePath)
				} else {
					resultText += fmt.Sprintf("\n\n[Showing lines %d-%d of %d (%s limit). Full output: %s]",
						startLine, endLine, truncation.TotalLines, FormatSize(cfg.MaxBytes), tempFilePath)
				}
			}

			if exitCode != 0 {
				resultText += fmt.Sprintf("\n\nCommand exited with code %d", exitCode)
				return result(resultText, details)
			}

			if err != nil {
				return resultErrf("command failed: %w", err)
			}

			return result(resultText, details)
		},
	}
}
