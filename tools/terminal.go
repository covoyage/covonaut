package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/creack/pty"
)

// TerminalToolConfig configures the persistent terminal tool family. When
// nil, terminal tools are not registered.
type TerminalToolConfig struct {
	// Shell overrides the shell used for new sessions (default: $SHELL,
	// falling back to /bin/sh).
	Shell string

	// MaxSessions caps concurrent live sessions (default: 8).
	MaxSessions int

	// HistoryBytes caps how much output each session retains (default: 256KB).
	HistoryBytes int

	// DefaultReadTimeout is how long terminal_read waits for new output when
	// timeout_seconds is not given (default: 10s).
	DefaultReadTimeout time.Duration
}

func (c *TerminalToolConfig) setDefaults() {
	if c.MaxSessions <= 0 {
		c.MaxSessions = 8
	}
	if c.HistoryBytes <= 0 {
		c.HistoryBytes = 256 * 1024
	}
	if c.DefaultReadTimeout <= 0 {
		c.DefaultReadTimeout = 10 * time.Second
	}
}

// terminalRegistry owns all live PTY sessions. Sessions persist across tool
// calls: working directory, exported variables, and background jobs survive
// between terminal_send invocations, unlike per-call bash execution.
type terminalRegistry struct {
	mu   sync.Mutex
	cfg  TerminalToolConfig
	sess map[string]*terminalSession
	next int
}

func newTerminalRegistry(cfg *TerminalToolConfig) *terminalRegistry {
	c := *cfg
	c.setDefaults()
	return &terminalRegistry{cfg: c, sess: make(map[string]*terminalSession)}
}

// terminalSession is one interactive shell attached to a PTY.
type terminalSession struct {
	id      string
	command string
	cmd     *exec.Cmd
	tty     *os.File

	mu      sync.Mutex
	history []byte // capped retained output (oldest trimmed)
	pending []byte // output since the last successful read
	exited  bool
	exitErr error

	done   chan struct{} // closed when the child exits
	notify chan struct{} // receives a token whenever new output arrives
	closed bool          // close requested
}

// reader pumps PTY output into the session buffers until the PTY closes.
func (s *terminalSession) reader(historyCap int) {
	buf := make([]byte, 4096)
	for {
		n, err := s.tty.Read(buf)
		if n > 0 {
			s.mu.Lock()
			s.history = append(s.history, buf[:n]...)
			if len(s.history) > historyCap {
				s.history = s.history[len(s.history)-historyCap:]
			}
			s.pending = append(s.pending, buf[:n]...)
			if len(s.pending) > historyCap {
				s.pending = s.pending[len(s.pending)-historyCap:]
			}
			s.mu.Unlock()
			// Non-blocking notify: at most one pending token per reader.
			select {
			case s.notify <- struct{}{}:
			default:
			}
		}
		if err != nil {
			return
		}
	}
}

// reap waits for the child process and records its exit status.
func (s *terminalSession) reap() {
	err := s.cmd.Wait()
	s.mu.Lock()
	s.exited = true
	if _, ok := err.(*exec.ExitError); ok && err != nil {
		s.exitErr = fmt.Errorf("exit: %v", err)
	} else {
		s.exitErr = err
	}
	s.mu.Unlock()
	close(s.done)
}

// snapshot returns pending output (draining it) plus exit status.
func (s *terminalSession) snapshot() (out string, status string, exitErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out = string(s.pending)
	s.pending = nil
	if s.exited {
		status = "exited"
		exitErr = s.exitErr
	} else {
		status = "running"
	}
	return out, status, exitErr
}

// drainWait blocks until new output arrives, the session exits, or the
// timeout elapses; it returns false on timeout.
func (s *terminalSession) drainWait(timeout time.Duration) bool {
	select {
	case <-s.notify:
		return true
	case <-s.done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// alive reports whether the child process is still running.
func (s *terminalSession) alive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.exited
}

// hasPending reports whether unread output is buffered, without draining it.
func (s *terminalSession) hasPending() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending) > 0 || s.exited
}

// waitQuiet blocks until session output has appeared and then stopped
// growing for one poll interval, or the deadline passes. Used after starting
// the shell to give line-editor initialization time to finish before
// injecting input.
func (s *terminalSession) waitQuiet(max time.Duration) {
	deadline := time.Now().Add(max)
	// Phase 1: wait for the first output chunk (the prompt being drawn).
	// Returning early here would inject input before the shell is ready.
	for time.Now().Before(deadline) {
		if s.historyLen() > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Phase 2: wait until output stops growing.
	last := s.historyLen()
	for time.Now().Before(deadline) {
		time.Sleep(150 * time.Millisecond)
		n := s.historyLen()
		if n == last {
			return
		}
		last = n
	}
}

func (s *terminalSession) historyLen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.history)
}

// --- tool handlers ---

func (r *terminalRegistry) shellCommand() string {
	if r.cfg.Shell != "" {
		return r.cfg.Shell
	}
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh
	}
	return "/bin/sh"
}

func (r *terminalRegistry) openTool(ctx context.Context, args json.RawMessage) (any, error) {
	if runtime.GOOS == "windows" {
		return nil, fmt.Errorf("terminal tools require a POSIX platform; use the bash tool on Windows")
	}

	var params struct {
		Command string `json:"command"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &params); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
	}

	r.mu.Lock()
	if len(r.sess) >= r.cfg.MaxSessions {
		r.mu.Unlock()
		return nil, fmt.Errorf("session limit reached (%d); close a session first", r.cfg.MaxSessions)
	}
	r.next++
	id := fmt.Sprintf("term-%d", r.next)
	r.mu.Unlock()

	// Interactive shell so foreground jobs and Ctrl-C semantics work. When
	// an initial command is given, write it as the first input after the
	// shell has settled: writing earlier races with line-editor
	// initialization (zsh's bracketed-paste mode mangles input received
	// before the prompt is up). The typed command keeps the interactive
	// session running and persists exported variables and the cwd.
	shell := r.shellCommand()
	cmd := exec.Command(shell)
	tty, err := pty.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("start pty: %w", err)
	}

	s := &terminalSession{
		id:      id,
		command: params.Command,
		cmd:     cmd,
		tty:     tty,
		done:    make(chan struct{}),
		notify:  make(chan struct{}, 1),
	}
	go s.reader(r.cfg.HistoryBytes)
	go s.reap()

	if params.Command != "" {
		s.waitQuiet(5 * time.Second)
		if _, err := tty.WriteString(params.Command + "\n"); err != nil {
			return nil, fmt.Errorf("write initial command: %w", err)
		}
	}

	r.mu.Lock()
	r.sess[id] = s
	r.mu.Unlock()

	return map[string]any{
		"session_id": id,
		"shell":      r.shellCommand(),
		"note":       "environment persists across terminal_send calls; use terminal_close when done",
	}, nil
}

func (r *terminalRegistry) getSession(id string) (*terminalSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sess[id]
	if !ok {
		return nil, fmt.Errorf("unknown session %q", id)
	}
	return s, nil
}

func (r *terminalRegistry) sendTool(ctx context.Context, args json.RawMessage) (any, error) {
	var params struct {
		SessionID string `json:"session_id"`
		Input     string `json:"input"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if params.SessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	s, err := r.getSession(params.SessionID)
	if err != nil {
		return nil, err
	}
	if !s.alive() {
		return nil, fmt.Errorf("session %s has exited", s.id)
	}
	input := params.Input
	if input != "" && !bytes.HasSuffix([]byte(input), []byte{'\n'}) {
		input += "\n"
	}
	if _, err := s.tty.WriteString(input); err != nil {
		return nil, fmt.Errorf("write input: %w", err)
	}
	return map[string]any{"session_id": s.id, "sent": true}, nil
}

func (r *terminalRegistry) readTool(ctx context.Context, args json.RawMessage) (any, error) {
	var params struct {
		SessionID      string  `json:"session_id"`
		TimeoutSeconds float64 `json:"timeout_seconds"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if params.SessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	s, err := r.getSession(params.SessionID)
	if err != nil {
		return nil, err
	}

	// Wait for output only if nothing is buffered yet; the drain must not
	// happen before this check or the probe would discard buffered data.
	if !s.hasPending() {
		timeout := r.cfg.DefaultReadTimeout
		if params.TimeoutSeconds > 0 {
			timeout = time.Duration(params.TimeoutSeconds * float64(time.Second))
		}
		s.drainWait(timeout)
	}

	out, status, exitErr := s.snapshot()
	result := map[string]any{
		"session_id": s.id,
		"output":     out,
		"status":     status,
	}
	if exitErr != nil {
		result["exit_error"] = exitErr.Error()
	}
	return result, nil
}

func (r *terminalRegistry) signalTool(ctx context.Context, args json.RawMessage) (any, error) {
	var params struct {
		SessionID string `json:"session_id"`
		Signal    string `json:"signal"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if params.SessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	if params.Signal == "" {
		params.Signal = "SIGINT"
	}
	sig, ok := signalByName(params.Signal)
	if !ok {
		return nil, fmt.Errorf("unsupported signal %q (use SIGINT, SIGTERM, SIGHUP, SIGQUIT or SIGKILL)", params.Signal)
	}
	s, err := r.getSession(params.SessionID)
	if err != nil {
		return nil, err
	}
	if !s.alive() {
		return nil, fmt.Errorf("session %s has exited", s.id)
	}
	if err := s.sendSignal(sig); err != nil {
		return nil, fmt.Errorf("send %s: %w", params.Signal, err)
	}
	return map[string]any{"signalled": params.Signal, "session_id": s.id}, nil
}

func (r *terminalRegistry) closeTool(ctx context.Context, args json.RawMessage) (any, error) {
	var params struct {
		SessionID string `json:"session_id"`
		Force     bool   `json:"force"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if params.SessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	s, err := r.getSession(params.SessionID)
	if err != nil {
		return nil, err
	}

	if s.alive() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		// Closing the PTY master sends SIGHUP to the foreground group. If
		// the shell ignores it (or force is requested), kill outright.
		_ = s.tty.Close()
		select {
		case <-s.done:
		case <-time.After(2 * time.Second):
			if params.Force {
				_ = s.cmd.Process.Kill()
				<-s.done
			}
		}
	}

	_, status, exitErr := s.snapshot()
	r.mu.Lock()
	delete(r.sess, s.id)
	r.mu.Unlock()

	result := map[string]any{"closed": s.id, "status": status}
	if exitErr != nil {
		result["exit_error"] = exitErr.Error()
	}
	return result, nil
}

func (r *terminalRegistry) listTool(ctx context.Context, args json.RawMessage) (any, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entries := make([]map[string]any, 0, len(r.sess))
	for id, s := range r.sess {
		status := "running"
		if !s.alive() {
			status = "exited"
		}
		entries = append(entries, map[string]any{
			"id":      id,
			"command": s.command,
			"status":  status,
		})
	}
	return map[string]any{"sessions": entries}, nil
}

// --- registration ---

func terminalStringParam(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func terminalNumberParam(desc string) map[string]any {
	return map[string]any{"type": "number", "description": desc}
}

// buildTerminalTools returns the persistent terminal tool family backed by
// reg. All six tools share the registry and mutate shared session state, so
// none of them is safe for concurrent mixed execution.
func buildTerminalTools(reg *terminalRegistry) []*agentcore.Tool {
	sessionIDParam := terminalStringParam("Session ID returned by terminal_open")
	return []*agentcore.Tool{
		{
			Name: "terminal_open",
			Description: "Start a persistent interactive shell session in a PTY. " +
				"The working directory, environment variables, and background jobs persist across terminal_send calls. " +
				"Provide command to run it as the first input; the session stays open afterwards. " +
				"Close sessions with terminal_close when finished.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": terminalStringParam("Optional first command to run in the new session"),
				},
			},
			Func: reg.openTool,
		},
		{
			Name: "terminal_send",
			Description: "Write input to a persistent terminal session. A trailing newline is appended automatically, " +
				"so this executes a command; send Ctrl-D style input via terminal_send with explicit control characters.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_id": sessionIDParam,
					"input":      terminalStringParam("Text to write to the shell"),
				},
				"required": []string{"session_id", "input"},
			},
			Func: reg.sendTool,
		},
		{
			Name: "terminal_read",
			Description: "Read new output from a persistent terminal session since the last read. " +
				"Waits up to timeout_seconds for output to arrive before returning.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_id":      sessionIDParam,
					"timeout_seconds": terminalNumberParam("How long to wait for new output in seconds (default: 10)"),
				},
				"required": []string{"session_id"},
			},
			Func: reg.readTool,
		},
		{
			Name:        "terminal_signal",
			Description: "Send a signal (SIGINT, SIGTERM, SIGHUP, SIGQUIT, SIGKILL) to a persistent terminal session's foreground process group. Use SIGINT for Ctrl-C semantics.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_id": sessionIDParam,
					"signal":     terminalStringParam("Signal name, e.g. SIGINT (default)"),
				},
				"required": []string{"session_id"},
			},
			Func: reg.signalTool,
		},
		{
			Name:        "terminal_close",
			Description: "Close a persistent terminal session. The shell receives SIGHUP; pass force to kill it after the grace period.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_id": sessionIDParam,
					"force":      map[string]any{"type": "boolean", "description": "Kill the shell after the grace period instead of waiting"},
				},
				"required": []string{"session_id"},
			},
			Func: reg.closeTool,
		},
		{
			Name:        "terminal_list",
			Description: "List persistent terminal sessions and their status (running or exited).",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
			Func: reg.listTool,
		},
	}
}

// NewTerminalTools constructs the persistent terminal tool family. Useful
// when registering terminal support without the full built-in extension.
func NewTerminalTools(cfg *TerminalToolConfig) []*agentcore.Tool {
	if cfg == nil {
		return nil
	}
	return buildTerminalTools(newTerminalRegistry(cfg))
}
