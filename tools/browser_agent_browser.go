package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
)

type AgentBrowserManager struct {
	mu            sync.Mutex
	binaryPath    string
	socketBaseDir string
	sessions      map[string]*AgentBrowserSession
}

type AgentBrowserSession struct {
	TaskID    string
	CDPURL    string
	SocketDir string
	StartedAt time.Time
}

type AgentBrowserResult struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
}

func NewAgentBrowserManager() *AgentBrowserManager {
	binary, _ := findAgentBrowserBinary()
	dir := _abSocketSafeTmpdir()
	return &AgentBrowserManager{
		binaryPath:    binary,
		socketBaseDir: dir,
		sessions:      make(map[string]*AgentBrowserSession),
	}
}

func _abSocketSafeTmpdir() string {
	if runtime.GOOS == "darwin" {
		return "/tmp"
	}
	return os.TempDir()
}

var errAgentBrowserNotFound = fmt.Errorf(
	"agent-browser CLI not found. Install it with: npm install -g agent-browser && agent-browser install",
)

func findAgentBrowserBinary() (string, error) {
	if path := os.Getenv("AGENT_BROWSER_PATH"); path != "" {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
		if path, err := exec.LookPath(path); err == nil {
			return path, nil
		}
	}

	if path, err := exec.LookPath("agent-browser"); err == nil {
		return path, nil
	}

	homebrewPaths := []string{
		"/opt/homebrew/bin/agent-browser",
		"/opt/homebrew/lib/node_modules/agent-browser/bin/agent-browser.js",
		"/usr/local/bin/agent-browser",
	}
	for _, p := range homebrewPaths {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	if path, err := exec.LookPath("npx"); err == nil {
		return path + " agent-browser", nil
	}

	return "", errAgentBrowserNotFound
}

func (m *AgentBrowserManager) sessionSocketDir(taskID string) string {
	return filepath.Join(m.socketBaseDir, fmt.Sprintf("agent-browser-%s", taskID))
}

func (m *AgentBrowserManager) EnsureSession(ctx context.Context, taskID string) (*AgentBrowserSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if existing, ok := m.sessions[taskID]; ok {
		return existing, nil
	}

	socketDir := m.sessionSocketDir(taskID)
	os.MkdirAll(socketDir, 0700)

	ownerPIDPath := filepath.Join(socketDir, "owner_pid")
	os.WriteFile(ownerPIDPath, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0600)

	session := &AgentBrowserSession{
		TaskID:    taskID,
		SocketDir: socketDir,
		StartedAt: time.Now(),
	}

	// Try to discover CDP URL if a daemon is already running
	cdpURL := m.discoverCDPURL(socketDir)
	if cdpURL != "" {
		session.CDPURL = cdpURL
	}

	m.sessions[taskID] = session
	return session, nil
}

func (m *AgentBrowserManager) discoverCDPURL(socketDir string) string {
	cdpFile := filepath.Join(socketDir, "cdp_url")
	data, err := os.ReadFile(cdpFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func (m *AgentBrowserManager) StopSession(taskID string) {
	m.mu.Lock()
	session, ok := m.sessions[taskID]
	if ok {
		delete(m.sessions, taskID)
	}
	m.mu.Unlock()

	if !ok {
		return
	}

	// Send close command to agent-browser daemon
	m.runABCommand(taskID, "close", nil, 5*time.Second)
	os.RemoveAll(session.SocketDir)
}

func (m *AgentBrowserManager) RunCommand(taskID string, command string, args []string, timeout time.Duration) (*AgentBrowserResult, error) {
	if _, err := m.EnsureSession(context.Background(), taskID); err != nil {
		return nil, err
	}
	return m.runABCommand(taskID, command, args, timeout)
}

func (m *AgentBrowserManager) buildABCommand(ctx context.Context, taskID string, command string, args []string) *exec.Cmd {
	binary := m.binaryPath
	socketDir := m.sessionSocketDir(taskID)

	allArgs := []string{"--session", taskID, "--json", command}
	allArgs = append(allArgs, args...)

	var cmd *exec.Cmd
	if strings.HasPrefix(binary, "npx ") {
		npxBin := strings.Fields(binary)[0]
		npxArgs := []string{"agent-browser"}
		npxArgs = append(npxArgs, allArgs...)
		cmd = exec.CommandContext(ctx, npxBin, npxArgs...)
	} else {
		cmd = exec.CommandContext(ctx, binary, allArgs...)
	}

	cmd.Env = append(os.Environ(),
		"AGENT_BROWSER_SOCKET_DIR="+socketDir,
		"AGENT_BROWSER_IDLE_TIMEOUT_MS=300000",
	)

	return cmd
}

func (m *AgentBrowserManager) runABCommand(taskID string, command string, args []string, timeout time.Duration) (*AgentBrowserResult, error) {
	if m.binaryPath == "" {
		return nil, errAgentBrowserNotFound
	}

	if timeout <= 0 {
		timeout = 60 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := m.buildABCommand(ctx, taskID, command, args)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		outStr := strings.TrimSpace(stdout.String())
		errStr := strings.TrimSpace(stderr.String())

		if outStr != "" {
			var result AgentBrowserResult
			if json.Unmarshal([]byte(outStr), &result) == nil {
				return &result, nil
			}
		}

		if errStr != "" {
			return nil, fmt.Errorf("agent-browser %s failed: %s", command, errStr)
		}
		return nil, fmt.Errorf("agent-browser %s failed: %w", command, err)
	}

	outStr := strings.TrimSpace(stdout.String())
	if outStr == "" {
		if command == "close" || command == "record" {
			return &AgentBrowserResult{Success: true}, nil
		}
		return nil, fmt.Errorf("agent-browser %s returned empty output", command)
	}

	lines := strings.Split(outStr, "\n")
	lastLine := strings.TrimSpace(lines[len(lines)-1])

	var result AgentBrowserResult
	if err := json.Unmarshal([]byte(lastLine), &result); err != nil {
		return nil, fmt.Errorf("agent-browser JSON parse error: %w\noutput: %s", err, outStr)
	}

	// Cache CDP URL from snapshot response
	if result.Success && result.Data != nil {
		var metadata struct {
			CDPURL string `json:"cdpUrl,omitempty"`
		}
		if json.Unmarshal(result.Data, &metadata) == nil && metadata.CDPURL != "" {
			m.mu.Lock()
			if s, ok := m.sessions[taskID]; ok {
				s.CDPURL = metadata.CDPURL
			}
			m.mu.Unlock()
		}
	}

	return &result, nil
}

// Convenience wrappers

func (m *AgentBrowserManager) Navigate(taskID string, url string, timeout time.Duration) (*AgentBrowserResult, error) {
	return m.RunCommand(taskID, "open", []string{url}, timeout)
}

func (m *AgentBrowserManager) Snapshot(taskID string, timeout time.Duration) (*AgentBrowserResult, error) {
	return m.RunCommand(taskID, "snapshot", []string{"-c"}, timeout)
}

func (m *AgentBrowserManager) Click(taskID string, ref string, timeout time.Duration) (*AgentBrowserResult, error) {
	return m.RunCommand(taskID, "click", []string{ref}, timeout)
}

func (m *AgentBrowserManager) Fill(taskID string, ref string, text string, timeout time.Duration) (*AgentBrowserResult, error) {
	return m.RunCommand(taskID, "fill", []string{ref, text}, timeout)
}

func (m *AgentBrowserManager) Scroll(taskID string, direction string, timeout time.Duration) (*AgentBrowserResult, error) {
	return m.RunCommand(taskID, "scroll", []string{direction, "500"}, timeout)
}

func (m *AgentBrowserManager) Back(taskID string, timeout time.Duration) (*AgentBrowserResult, error) {
	return m.RunCommand(taskID, "back", nil, timeout)
}

func (m *AgentBrowserManager) PressKey(taskID string, key string, timeout time.Duration) (*AgentBrowserResult, error) {
	return m.RunCommand(taskID, "press", []string{key}, timeout)
}

func (m *AgentBrowserManager) Screenshot(taskID string, timeout time.Duration) (*AgentBrowserResult, error) {
	return m.RunCommand(taskID, "screenshot", nil, timeout)
}

func (m *AgentBrowserManager) Evaluate(taskID string, expression string, timeout time.Duration) (*AgentBrowserResult, error) {
	return m.RunCommand(taskID, "eval", []string{expression}, timeout)
}

func (m *AgentBrowserManager) Console(taskID string, timeout time.Duration) (*AgentBrowserResult, error) {
	consoleRes, _ := m.RunCommand(taskID, "console", []string{"-j"}, timeout)
	errorsRes, _ := m.RunCommand(taskID, "errors", []string{"-j"}, timeout)

	data := map[string]any{}
	if consoleRes != nil && consoleRes.Success && consoleRes.Data != nil {
		var cd struct {
			Console []map[string]any `json:"console"`
		}
		if json.Unmarshal(consoleRes.Data, &cd) == nil {
			data["console"] = cd.Console
		}
	}
	if errorsRes != nil && errorsRes.Success && errorsRes.Data != nil {
		var cd struct {
			Errors []map[string]any `json:"errors"`
		}
		if json.Unmarshal(errorsRes.Data, &cd) == nil {
			data["errors"] = cd.Errors
		}
	}
	dataBytes, _ := json.Marshal(data)
	return &AgentBrowserResult{Success: true, Data: dataBytes}, nil
}

func (m *AgentBrowserManager) CreateChromeDPContext(taskID string) (context.Context, context.CancelFunc, error) {
	m.mu.Lock()
	session, ok := m.sessions[taskID]
	m.mu.Unlock()
	if !ok {
		return nil, nil, fmt.Errorf("no session for task %s", taskID)
	}

	if session.CDPURL == "" {
		session.CDPURL = m.discoverCDPURL(session.SocketDir)
	}
	if session.CDPURL == "" {
		return nil, nil, fmt.Errorf("CDP URL not available for task %s", taskID)
	}

	allocCtx, allocCancel := chromedp.NewRemoteAllocator(context.Background(), session.CDPURL)
	browserCtx, cancel := chromedp.NewContext(allocCtx)
	cancelFn := func() {
		cancel()
		allocCancel()
	}
	return browserCtx, cancelFn, nil
}

func (m *AgentBrowserManager) StopAll() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.Unlock()

	for _, id := range ids {
		m.StopSession(id)
	}
}
