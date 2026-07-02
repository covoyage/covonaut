package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

type sessionState struct {
	SessionID string
	Agent     AgentInstance
	CWD       string
	Model     string
	Mode      string

	cancel  context.CancelFunc
	running bool
	mu      sync.Mutex
}

func (s *sessionState) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

func (s *sessionState) Cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
}

type SessionManager struct {
	sessions map[string]*sessionState
	mu       sync.RWMutex

	agentFactory AgentFactory
	sessionStore SessionStore
	homeDir      string
	persistPath  string
	logger       *slog.Logger
}

type SessionManagerConfig struct {
	AgentFactory AgentFactory
	SessionStore SessionStore
	HomeDir      string
	Logger       *slog.Logger
}

func NewSessionManager(cfg SessionManagerConfig) *SessionManager {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	}
	if cfg.SessionStore == nil {
		cfg.SessionStore = &noopStore{}
	}

	sm := &SessionManager{
		sessions:     make(map[string]*sessionState),
		agentFactory: cfg.AgentFactory,
		sessionStore: cfg.SessionStore,
		homeDir:      cfg.HomeDir,
		persistPath:  filepath.Join(cfg.HomeDir, "acp_sessions.json"),
		logger:       cfg.Logger,
	}
	sm.loadPersistedSessions()
	return sm
}

func (sm *SessionManager) CreateSession(cwd, sessionID string) (*sessionState, error) {
	if cwd == "" {
		cwd = "."
	}
	absCWD, err := filepath.Abs(cwd)
	if err != nil {
		absCWD = cwd
	}

	if sessionID == "" {
		sessionID = fmt.Sprintf("%d", time.Now().UnixNano())
	}

	model := sm.agentFactory.DefaultModel()
	mode := sm.agentFactory.DefaultMode()

	agent, err := sm.agentFactory.CreateAgent(context.Background(), sessionID, absCWD, model, mode)
	if err != nil {
		return nil, fmt.Errorf("create agent: %w", err)
	}

	state := &sessionState{
		SessionID: sessionID,
		Agent:     agent,
		CWD:       absCWD,
		Model:     agent.Model(),
		Mode:      agent.Mode(),
	}

	sm.mu.Lock()
	if prev, ok := sm.sessions[sessionID]; ok {
		if prev.Agent != nil && prev.Agent.Core() != nil {
			prev.Agent.Core().Close()
		}
	}
	sm.sessions[sessionID] = state
	sm.mu.Unlock()

	sm.saveSessionMeta(state)

	sm.logger.Info("Created ACP session", "session_id", sessionID, "cwd", absCWD)
	return state, nil
}

func (sm *SessionManager) GetSession(sessionID string) *sessionState {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.sessions[sessionID]
}

func (sm *SessionManager) UpdateCWD(sessionID, cwd string) *sessionState {
	state := sm.GetSession(sessionID)
	if state == nil {
		return nil
	}
	absCWD, err := filepath.Abs(cwd)
	if err != nil {
		absCWD = cwd
	}
	state.CWD = absCWD
	sm.saveSessionMeta(state)
	return state
}

func (sm *SessionManager) UpdateModel(sessionID, model string) error {
	state := sm.GetSession(sessionID)
	if state == nil {
		return fmt.Errorf("session %s not found", sessionID)
	}
	state.Model = model
	sm.saveSessionMeta(state)
	return nil
}

func (sm *SessionManager) UpdateMode(sessionID, mode string) error {
	state := sm.GetSession(sessionID)
	if state == nil {
		return fmt.Errorf("session %s not found", sessionID)
	}
	state.Mode = mode
	sm.saveSessionMeta(state)
	return nil
}

func (sm *SessionManager) SetRunning(sessionID string, cancel context.CancelFunc) {
	sm.mu.RLock()
	state := sm.sessions[sessionID]
	sm.mu.RUnlock()
	if state == nil {
		return
	}
	state.mu.Lock()
	state.running = true
	state.cancel = cancel
	state.mu.Unlock()
}

func (sm *SessionManager) SetIdle(sessionID string) {
	sm.mu.RLock()
	state := sm.sessions[sessionID]
	sm.mu.RUnlock()
	if state == nil {
		return
	}
	state.mu.Lock()
	state.running = false
	state.cancel = nil
	state.mu.Unlock()
}

func (sm *SessionManager) ForkSession(sessionID, cwd string) (*sessionState, error) {
	original := sm.GetSession(sessionID)
	if original == nil {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}

	state, err := sm.CreateSession(cwd, "")
	if err != nil {
		return nil, err
	}

	sm.logger.Info("Forked ACP session", "from", sessionID, "to", state.SessionID)
	return state, nil
}

func (sm *SessionManager) ListSessions(cwd string) []SessionInfo {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	var result []SessionInfo
	for id, state := range sm.sessions {
		if cwd != "" && state.CWD != cwd {
			continue
		}
		result = append(result, SessionInfo{
			SessionID: id,
			CWD:       state.CWD,
			Title:     filepath.Base(state.CWD),
			UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		})
	}
	return result
}

func (sm *SessionManager) RestoreSession(sessionID string) (*sessionState, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if state, ok := sm.sessions[sessionID]; ok {
		return state, nil
	}

	meta, err := sm.sessionStore.LoadSessionMeta(sessionID)
	if err != nil {
		return nil, err
	}

	agent, err := sm.agentFactory.CreateAgent(context.Background(), meta.SessionID, meta.CWD, meta.Model, meta.Mode)
	if err != nil {
		return nil, fmt.Errorf("restore session %s: %w", sessionID, err)
	}

	state := &sessionState{
		SessionID: meta.SessionID,
		Agent:     agent,
		CWD:       meta.CWD,
		Model:     agent.Model(),
		Mode:      agent.Mode(),
	}

	sm.sessions[sessionID] = state
	sm.logger.Info("restored session from disk", "session_id", sessionID)
	return state, nil
}

func (sm *SessionManager) Cleanup() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	for id, state := range sm.sessions {
		if state.cancel != nil {
			state.cancel()
		}
		delete(sm.sessions, id)
	}
}

func (sm *SessionManager) saveSessionMeta(state *sessionState) {
	meta := SessionMeta{
		SessionID: state.SessionID,
		CWD:       state.CWD,
		Model:     state.Model,
		Mode:      state.Mode,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := sm.sessionStore.SaveSessionMeta(meta); err != nil {
		sm.logger.Error("failed to save session meta", "err", err)
	}
}

func (sm *SessionManager) loadPersistedSessions() {
	metas := sm.sessionStore.ListSessions("")
	loaded := 0
	for _, meta := range metas {
		if _, exists := sm.sessions[meta.SessionID]; exists {
			continue
		}
		agent, err := sm.agentFactory.CreateAgent(context.Background(), meta.SessionID, meta.CWD, meta.Model, meta.Mode)
		if err != nil {
			sm.logger.Warn("failed to restore session", "session_id", meta.SessionID, "err", err)
			continue
		}
		sm.sessions[meta.SessionID] = &sessionState{
			SessionID: meta.SessionID,
			Agent:     agent,
			CWD:       meta.CWD,
			Model:     agent.Model(),
			Mode:      agent.Mode(),
		}
		loaded++
	}
	if loaded > 0 {
		sm.logger.Info("restored persisted sessions", "count", loaded)
	}
}

type noopStore struct{}

func (n *noopStore) LoadSessionMeta(sessionID string) (SessionMeta, error) {
	return SessionMeta{}, fmt.Errorf("session %s not found", sessionID)
}

func (n *noopStore) SaveSessionMeta(meta SessionMeta) error {
	return nil
}

func (n *noopStore) ListSessions(cwd string) []SessionMeta {
	return nil
}

func EnsureHomeDir(homeDir string) (string, error) {
	if homeDir == "" {
		var err error
		homeDir, err = os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("get home dir: %w", err)
		}
		homeDir = filepath.Join(homeDir, ".acp-agent")
	}
	if err := os.MkdirAll(homeDir, 0755); err != nil {
		return "", fmt.Errorf("create home dir: %w", err)
	}
	return homeDir, nil
}

// RegisterEventListeners attaches per-prompt event handlers to the agent and
// returns an unregister function that MUST be called when the prompt finishes.
// The agent is reused across prompts within a session; without unregister,
// handlers accumulate and each new prompt re-notifies through stale closures
// bound to the previous prompt's notify channel.
func RegisterEventListeners(sessionID string, core *agentcore.Agent, notify func(method string, params any)) (unregister func()) {
	unsubs := make([]func(), 0, 3)
	unsubs = append(unsubs, core.On(agentcore.EventToolCallStart, func(e agentcore.Event) {
		ev, ok := e.(*agentcore.ToolCallStartEvent)
		if !ok {
			return
		}
		kind := ToolKind(ev.ToolCall.Name)
		args := parseToolArgs(ev.ToolCall.Arguments)
		title := BuildToolTitle(ev.ToolCall.Name, args)
		notify("session/update", SessionNotification{
			SessionID: sessionID,
			Update: SessionUpdate{
				SessionUpdate: "tool_call",
				ToolCallID:    ev.ToolCall.ID,
				Title:         title,
				Kind:          kind,
				Status:        "in_progress",
				RawInput:      args,
			},
		})
	}))

	unsubs = append(unsubs, core.On(agentcore.EventToolCallEnd, func(e agentcore.Event) {
		ev, ok := e.(*agentcore.ToolCallEndEvent)
		if !ok {
			return
		}
		kind := ToolKind(ev.ToolName)
		status := "completed"
		if ev.Err != nil {
			status = "error"
		}
		notify("session/update", SessionNotification{
			SessionID: sessionID,
			Update: SessionUpdate{
				SessionUpdate: "tool_call_update",
				ToolCallID:    ev.ToolCallID,
				Title:         ev.ToolName,
				Kind:          kind,
				Status:        status,
				RawOutput:     ev.Result,
			},
		})
	}))

	unsubs = append(unsubs, core.On(agentcore.EventMessageDelta, func(e agentcore.Event) {
		ev, ok := e.(*agentcore.MessageDeltaEvent)
		if !ok {
			return
		}
		if ev.Kind == "thinking" {
			notify("session/update", SessionNotification{
				SessionID: sessionID,
				Update: SessionUpdate{
					SessionUpdate: "agent_thought_chunk",
					Content:       TextContentBlock{Type: "text", Text: ev.Delta},
				},
			})
		} else {
			notify("session/update", SessionNotification{
				SessionID: sessionID,
				Update: SessionUpdate{
					SessionUpdate: "agent_message_chunk",
					Content:       TextContentBlock{Type: "text", Text: ev.Delta},
				},
			})
		}
	}))

	return func() {
		for _, u := range unsubs {
			u()
		}
	}
}

func parseToolArgs(raw string) map[string]any {
	args := make(map[string]any)
	if raw == "" {
		return args
	}
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		args["raw"] = raw
	}
	return args
}

func ToolKind(name string) string {
	kinds := map[string]string{
		"read_file":         "read",
		"write_file":        "edit",
		"patch":             "edit",
		"search_files":      "search",
		"terminal":          "execute",
		"process":           "execute",
		"execute_code":      "execute",
		"todo":              "other",
		"skill_view":        "read",
		"skills_list":       "read",
		"skill_manage":      "edit",
		"web_search":        "fetch",
		"web_extract":       "fetch",
		"browser_navigate":  "fetch",
		"browser_click":     "execute",
		"browser_type":      "execute",
		"browser_snapshot":  "read",
		"browser_vision":    "read",
		"browser_scroll":    "execute",
		"browser_press":     "execute",
		"browser_back":      "execute",
		"browser_get_images": "read",
		"delegate_task":     "execute",
		"vision_analyze":    "read",
		"image_generate":    "execute",
		"_thinking":         "think",
	}
	if k, ok := kinds[name]; ok {
		return k
	}
	return "other"
}

func BuildToolTitle(name string, args map[string]any) string {
	switch name {
	case "terminal":
		if cmd, ok := args["command"].(string); ok {
			if len(cmd) > 80 {
				cmd = cmd[:77] + "..."
			}
			return "terminal: " + cmd
		}
	case "read_file":
		return "read: " + fmt.Sprint(args["path"])
	case "write_file":
		return "write: " + fmt.Sprint(args["path"])
	case "patch":
		mode := fmt.Sprint(args["mode"])
		path := fmt.Sprint(args["path"])
		return "patch (" + mode + "): " + path
	case "search_files":
		return "search: " + fmt.Sprint(args["pattern"])
	case "web_search":
		return "web search: " + fmt.Sprint(args["query"])
	case "web_extract":
		if urls, ok := args["urls"].([]any); ok && len(urls) > 0 {
			label := fmt.Sprint(urls[0])
			if len(urls) > 1 {
				label += fmt.Sprintf(" (+%d)", len(urls)-1)
			}
			return "extract: " + label
		}
		return "web extract"
	case "delegate_task":
		if goal, ok := args["goal"].(string); ok && goal != "" {
			if len(goal) > 60 {
				goal = goal[:57] + "..."
			}
			return "delegate: " + goal
		}
		return "delegate task"
	case "execute_code":
		if code, ok := args["code"].(string); ok {
			lines := splitLines(code)
			for _, line := range lines {
				line = trimSpace(line)
				if line != "" {
					if len(line) > 70 {
						line = line[:67] + "..."
					}
					return "python: " + line
				}
			}
		}
		return "python code"
	case "browser_navigate":
		return "navigate: " + fmt.Sprint(args["url"])
	case "browser_snapshot":
		return "browser snapshot"
	case "browser_vision":
		return "browser vision: " + truncateStr(fmt.Sprint(args["question"]), 50)
	case "browser_get_images":
		return "browser images"
	case "vision_analyze":
		return "analyze image: " + truncateStr(fmt.Sprint(args["question"]), 50)
	case "image_generate":
		prompt := ""
		if p, ok := args["prompt"].(string); ok {
			prompt = p
		} else if d, ok := args["description"].(string); ok {
			prompt = d
		}
		if prompt != "" {
			return "generate image: " + truncateStr(prompt, 50)
		}
		return "generate image"
	}
	return name
}

func splitLines(s string) []string {
	result := []string{}
	current := ""
	for _, c := range s {
		if c == '\n' {
			result = append(result, current)
			current = ""
		} else {
			current += string(c)
		}
	}
	if current != "" {
		result = append(result, current)
	}
	return result
}

func trimSpace(s string) string {
	start := 0
	end := len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}