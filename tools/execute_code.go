package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

var ansiEscapeRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

var blockedEnvPrefixes = []string{
	"KEY", "TOKEN", "SECRET", "PASSWORD", "CREDENTIAL", "AUTH",
}

var safeEnvPrefixes = []string{
	"PATH", "HOME", "USER", "LANG", "LC_", "TERM", "TMPDIR", "TMP", "TEMP",
	"SHELL", "LOGNAME", "XDG_", "PYTHON", "VIRTUAL_ENV", "CONDA",
}

func scrubEnv() []string {
	var cleaned []string
outer:
	for _, e := range os.Environ() {
		name, _, _ := strings.Cut(e, "=")
		upper := strings.ToUpper(name)

		for _, block := range blockedEnvPrefixes {
			if strings.Contains(upper, block) {
				continue outer
			}
		}

		safe := false
		for _, prefix := range safeEnvPrefixes {
			if strings.HasPrefix(upper, prefix) {
				safe = true
				break
			}
		}
		if safe {
			cleaned = append(cleaned, e)
		}
	}
	return cleaned
}

type ExecuteCodeToolConfig struct {
	// PythonCommand is the python interpreter path. Default: "python3".
	PythonCommand string
	// CommandTimeout is the max execution time per call. Default: 120s.
	CommandTimeout time.Duration
	// MaxOutputBytes is the max stdout bytes. Default: 50KB.
	MaxOutputBytes int64
}

func resolvePython(pythonCommand string) (string, error) {
	if pythonCommand != "" {
		if _, err := exec.LookPath(pythonCommand); err == nil {
			return pythonCommand, nil
		}
	}
	for _, name := range []string{"python3", "python"} {
		if _, err := exec.LookPath(name); err == nil {
			return name, nil
		}
	}
	return "", fmt.Errorf("no Python interpreter found (tried python3, python)")
}

func NewExecuteCodeTool(cfg *ExecuteCodeToolConfig) *agentcore.Tool {
	if cfg == nil {
		cfg = &ExecuteCodeToolConfig{}
	}
	if cfg.CommandTimeout <= 0 {
		cfg.CommandTimeout = 120 * time.Second
	}
	if cfg.MaxOutputBytes <= 0 {
		cfg.MaxOutputBytes = 50 * 1024
	}

	return &agentcore.Tool{
		Name: "execute_code",
		Description: "Execute Python code in a subprocess and return the output. " +
			"The code runs in a clean environment (API keys and secrets are scrubbed). " +
			"Useful for data processing, analysis, computation, generating content, " +
			"or any task that benefits from programmatic logic. " +
			fmt.Sprintf("Timeout: %s. Output limit: %s.", cfg.CommandTimeout, FormatSize(cfg.MaxOutputBytes)),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"code": map[string]any{
					"type":        "string",
					"description": "Python code to execute. Use print() to output results to stdout.",
				},
				"timeout": map[string]any{
					"type":        "integer",
					"description": fmt.Sprintf("Timeout in seconds (default: %d, max: 300)", int(cfg.CommandTimeout.Seconds())),
				},
			},
			"required": []any{"code"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var input struct {
				Code    string `json:"code"`
				Timeout int    `json:"timeout"`
			}
			if err := json.Unmarshal(args, &input); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			if input.Code == "" {
				return nil, fmt.Errorf("code is required")
			}

			python, err := resolvePython(cfg.PythonCommand)
			if err != nil {
				return nil, err
			}

			timeout := cfg.CommandTimeout
			if input.Timeout > 0 {
				timeout = time.Duration(input.Timeout) * time.Second
				if timeout > 300*time.Second {
					timeout = 300 * time.Second
				}
			}

			execCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			tmpDir, err := os.MkdirTemp("", "covo-exec-*")
			if err != nil {
				return nil, fmt.Errorf("failed to create temp dir: %w", err)
			}
			defer os.RemoveAll(tmpDir)

			scriptPath := filepath.Join(tmpDir, "script.py")
			if err := os.WriteFile(scriptPath, []byte(input.Code), 0644); err != nil {
				return nil, fmt.Errorf("failed to write script: %w", err)
			}

			cmd := exec.CommandContext(execCtx, python, scriptPath)
			cmd.Dir = tmpDir
			applySubprocessIsolation(cmd) // prevent /dev/tty access

			cleanEnv := scrubEnv()
			cleanEnv = append(cleanEnv,
				"PYTHONDONTWRITEBYTECODE=1",
				"PYTHONIOENCODING=utf-8",
				"PYTHONUNBUFFERED=1",
			)
			cmd.Env = cleanEnv

			var stdout, stderr bytes.Buffer
			cmd.Stdin = nil // prevent subprocess from consuming TUI stdin
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr

			start := time.Now()
			runErr := cmd.Run()
			duration := time.Since(start)

			result := map[string]any{
				"duration_seconds": duration.Seconds(),
				"language":         "python",
				"python":           python,
			}

			if runErr != nil {
				if execCtx.Err() != nil {
					result["status"] = "timeout"
					result["error"] = fmt.Sprintf("execution timed out after %s", timeout)
				} else {
					result["status"] = "error"
					result["error"] = runErr.Error()
				}
			} else {
				result["status"] = "success"
			}

			stdoutStr := stdout.String()
			stderrStr := stderr.String()

			// Strip ANSI escape sequences to prevent terminal corruption.
			stdoutStr = ansiEscapeRe.ReplaceAllString(stdoutStr, "")
			stderrStr = ansiEscapeRe.ReplaceAllString(stderrStr, "")

			if int64(len(stdoutStr)) > cfg.MaxOutputBytes {
				keep := int(cfg.MaxOutputBytes) / 2
				stdoutStr = stdoutStr[:keep] +
					fmt.Sprintf("\n\n... [output truncated at %d bytes] ...\n\n", cfg.MaxOutputBytes) +
					stdoutStr[len(stdoutStr)-keep:]
			}

			result["output"] = stdoutStr
			if stderrStr != "" {
				if int64(len(stderrStr)) > 10*1024 {
					stderrStr = stderrStr[:10*1024] + fmt.Sprintf("\n... [stderr truncated at 10KB] ...")
				}
				result["stderr"] = stderrStr
			}

			return result, nil
		},
	}
}


