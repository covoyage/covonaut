package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

// ProcessRegistry manages background processes.
type ProcessRegistry struct {
	mu        sync.RWMutex
	processes map[string]*ProcessEntry
}

// ProcessEntry tracks a background process.
type ProcessEntry struct {
	ID        string     `json:"id"`
	Command   string     `json:"command"`
	PID       int        `json:"pid"`
	Status    string     `json:"status"` // running, completed, failed, killed
	ExitCode  int        `json:"exit_code"`
	Output    []byte     `json:"-"`
	StartTime time.Time  `json:"start_time"`
	EndTime   *time.Time `json:"end_time,omitempty"`
	cmd       *exec.Cmd
	mu        sync.Mutex
}

// NewProcessRegistry creates a new process registry.
func NewProcessRegistry() *ProcessRegistry {
	return &ProcessRegistry{
		processes: make(map[string]*ProcessEntry),
	}
}

// Register adds a process to the registry.
func (r *ProcessRegistry) Register(entry *ProcessEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.processes[entry.ID] = entry
}

// Get retrieves a process by ID.
func (r *ProcessRegistry) Get(id string) (*ProcessEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.processes[id]
	return entry, ok
}

// List returns all process IDs.
func (r *ProcessRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.processes))
	for id := range r.processes {
		ids = append(ids, id)
	}
	return ids
}

// Cleanup removes completed processes older than maxAge.
func (r *ProcessRegistry) Cleanup(maxAge time.Duration) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	removed := 0
	for id, entry := range r.processes {
		if entry.Status != "running" && entry.EndTime != nil {
			if now.Sub(*entry.EndTime) > maxAge {
				delete(r.processes, id)
				removed++
			}
		}
	}
	return removed
}

// ProcessOperations defines pluggable operations for the process tool.
type ProcessOperations interface {
	Spawn(command string, cwd string) (*ProcessEntry, error)
	Kill(pid int) error
	Poll(entry *ProcessEntry) (string, int, []byte)
}

// DefaultProcessOperations uses the local system.
type DefaultProcessOperations struct {
	registry  *ProcessRegistry
	idCounter int
	mu        sync.Mutex
}

// NewDefaultProcessOperations creates default process operations.
func NewDefaultProcessOperations(registry *ProcessRegistry) *DefaultProcessOperations {
	return &DefaultProcessOperations{
		registry: registry,
	}
}

func (d *DefaultProcessOperations) nextID() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.idCounter++
	return fmt.Sprintf("proc-%d-%d", time.Now().Unix(), d.idCounter)
}

func (d *DefaultProcessOperations) Spawn(command string, cwd string) (*ProcessEntry, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}

	cmd := exec.Command(shell, "-c", command)
	cmd.Dir = cwd

	// Create output capture.
	output := &outputBuffer{maxBytes: 200 * 1024} // 200KB rolling buffer
	cmd.Stdout = output
	cmd.Stderr = output

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	entry := &ProcessEntry{
		ID:        d.nextID(),
		Command:   command,
		PID:       cmd.Process.Pid,
		Status:    "running",
		StartTime: time.Now(),
		cmd:       cmd,
	}

	d.registry.Register(entry)

	// Monitor in background.
	go func() {
		err := cmd.Wait()
		entry.mu.Lock()
		defer entry.mu.Unlock()
		now := time.Now()
		entry.EndTime = &now
		entry.Output = output.Bytes()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				entry.ExitCode = exitErr.ExitCode()
				entry.Status = "failed"
			} else {
				entry.Status = "failed"
				entry.ExitCode = -1
			}
		} else {
			entry.Status = "completed"
			entry.ExitCode = 0
		}
	}()

	return entry, nil
}

func (d *DefaultProcessOperations) Kill(pid int) error {
	// Try process group first, then direct kill.
	syscall.Kill(-pid, syscall.SIGKILL)
	return syscall.Kill(pid, syscall.SIGKILL)
}

func (d *DefaultProcessOperations) Poll(entry *ProcessEntry) (string, int, []byte) {
	entry.mu.Lock()
	defer entry.mu.Unlock()
	return entry.Status, entry.ExitCode, entry.Output
}

// outputBuffer is a thread-safe rolling buffer for process output.
type outputBuffer struct {
	mu       sync.Mutex
	data     []byte
	maxBytes int
}

func (b *outputBuffer) Write(p []byte) (n int, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, p...)
	if len(b.data) > b.maxBytes {
		// Keep last maxBytes.
		b.data = b.data[len(b.data)-b.maxBytes:]
	}
	return len(p), nil
}

func (b *outputBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.data...)
}

// ProcessToolConfig configures the process tool.
type ProcessToolConfig struct {
	Operations ProcessOperations
	MaxBytes   int64
	MaxLines   int64
}

func (c *ProcessToolConfig) defaults() {
	if c.Operations == nil {
		// Will be set later when registry is available.
	}
	if c.MaxBytes <= 0 {
		c.MaxBytes = 50 * 1024
	}
	if c.MaxLines <= 0 {
		c.MaxLines = 2000
	}
}

// ProcessToolInput is the JSON arguments for the process tool.
type ProcessToolInput struct {
	Action    string `json:"action"` // spawn, status, wait, kill, list
	Command   string `json:"command,omitempty"`
	ProcessID string `json:"process_id,omitempty"`
	Timeout   *int   `json:"timeout,omitempty"`
}

// ProcessToolDetails carries process metadata.
type ProcessToolDetails struct {
	ProcessID  string            `json:"process_id,omitempty"`
	Status     string            `json:"status,omitempty"`
	PID        int               `json:"pid,omitempty"`
	ExitCode   int               `json:"exit_code,omitempty"`
	StartTime  string            `json:"start_time,omitempty"`
	EndTime    string            `json:"end_time,omitempty"`
	Duration   string            `json:"duration,omitempty"`
	OutputSize int               `json:"output_size,omitempty"`
	Truncation *TruncationResult `json:"truncation,omitempty"`
}

// NewProcessTool creates a process management tool.
func NewProcessTool(cwd string, cfg *ProcessToolConfig) *agentcore.Tool {
	if cfg == nil {
		cfg = &ProcessToolConfig{}
	}
	cfg.defaults()

	return &agentcore.Tool{
		Name: "process",
		Description: "Manage background processes. Actions: spawn (start a background command), " +
			"status (check process status), wait (block until completion), kill (terminate), " +
			"list (show all tracked processes). " +
			"Output is truncated to last " + fmt.Sprintf("%d lines or %s", cfg.MaxLines, FormatSize(cfg.MaxBytes)) + ".",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "Action to perform: spawn, status, wait, kill, list",
					"enum":        []any{"spawn", "status", "wait", "kill", "list"},
				},
				"command": map[string]any{
					"type":        "string",
					"description": "Command to execute (required for spawn)",
				},
				"process_id": map[string]any{
					"type":        "string",
					"description": "Process ID (required for status, wait, kill)",
				},
				"timeout": map[string]any{
					"type":        "integer",
					"description": "Timeout in seconds for wait action (optional)",
				},
			},
			"required": []any{"action"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var input ProcessToolInput
			if err := json.Unmarshal(args, &input); err != nil {
				return resultErrf("invalid arguments: %w", err)
			}

			if cfg.Operations == nil {
				return resultErrf("process operations not configured")
			}

			switch input.Action {
			case "spawn":
				return handleSpawn(cfg, cwd, input)
			case "status":
				return handleStatus(cfg, input)
			case "wait":
				return handleWait(cfg, input)
			case "kill":
				return handleKill(cfg, input)
			case "list":
				return handleList(cfg)
			default:
				return resultErrf("unknown action: %s", input.Action)
			}
		},
	}
}

func handleSpawn(cfg *ProcessToolConfig, cwd string, input ProcessToolInput) (any, error) {
	if input.Command == "" {
		return resultErrf("command is required for spawn")
	}

	entry, err := cfg.Operations.Spawn(input.Command, cwd)
	if err != nil {
		return resultErrf("failed to spawn process: %w", err)
	}

	return result(
		fmt.Sprintf("Spawned process %s (PID %d): %s", entry.ID, entry.PID, input.Command),
		ProcessToolDetails{
			ProcessID: entry.ID,
			Status:    entry.Status,
			PID:       entry.PID,
			StartTime: entry.StartTime.Format(time.RFC3339),
		},
	)
}

func handleStatus(cfg *ProcessToolConfig, input ProcessToolInput) (any, error) {
	if input.ProcessID == "" {
		return resultErrf("process_id is required for status")
	}

	status, exitCode, output := cfg.Operations.Poll(&ProcessEntry{ID: input.ProcessID})

	// Get full entry for metadata.
	// Note: In real implementation, we'd need access to the registry.
	// For now, return what we have.
	outputText := string(output)
	truncation := TruncateTail(outputText, TruncationOptions{
		MaxLines: int(cfg.MaxLines),
		MaxBytes: int(cfg.MaxBytes),
	})

	resultText := fmt.Sprintf("Process %s: status=%s, exit_code=%d", input.ProcessID, status, exitCode)
	if truncation.Content != "" {
		resultText += "\n\nOutput:\n" + truncation.Content
	}

	var details ProcessToolDetails
	if truncation.Truncated {
		details.Truncation = &truncation
	}
	details.Status = status
	details.ExitCode = exitCode
	details.OutputSize = len(output)

	return result(resultText, details)
}

func handleWait(cfg *ProcessToolConfig, input ProcessToolInput) (any, error) {
	if input.ProcessID == "" {
		return resultErrf("process_id is required for wait")
	}

	// Poll until completion or timeout.
	timeout := 300 // default 5 minutes
	if input.Timeout != nil && *input.Timeout > 0 {
		timeout = *input.Timeout
	}

	deadline := time.Now().Add(time.Duration(timeout) * time.Second)
	for time.Now().Before(deadline) {
		status, exitCode, output := cfg.Operations.Poll(&ProcessEntry{ID: input.ProcessID})
		if status != "running" {
			outputText := string(output)
			truncation := TruncateTail(outputText, TruncationOptions{
				MaxLines: int(cfg.MaxLines),
				MaxBytes: int(cfg.MaxBytes),
			})

			resultText := fmt.Sprintf("Process %s completed with status=%s, exit_code=%d", input.ProcessID, status, exitCode)
			if truncation.Content != "" {
				resultText += "\n\nOutput:\n" + truncation.Content
			}

			var details ProcessToolDetails
			if truncation.Truncated {
				details.Truncation = &truncation
			}
			details.Status = status
			details.ExitCode = exitCode
			details.OutputSize = len(output)

			return result(resultText, details)
		}
		time.Sleep(1 * time.Second)
	}

	return resultErrf("timeout waiting for process %s after %d seconds", input.ProcessID, timeout)
}

func handleKill(cfg *ProcessToolConfig, input ProcessToolInput) (any, error) {
	if input.ProcessID == "" {
		return resultErrf("process_id is required for kill")
	}

	// We need to find the PID. In a real implementation, we'd look up in registry.
	// For now, parse process_id to extract PID if it was stored there.
	return resultErrf("kill not fully implemented: need registry lookup for PID")
}

func handleList(cfg *ProcessToolConfig) (any, error) {
	return resultErrf("list not fully implemented: need registry access")
}

// Helper to parse process ID for PID extraction.
func parsePIDFromID(id string) int {
	parts := strings.Split(id, "-")
	if len(parts) >= 3 {
		if pid, err := strconv.Atoi(parts[2]); err == nil {
			return pid
		}
	}
	return 0
}
