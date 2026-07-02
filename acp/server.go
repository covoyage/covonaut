package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Server struct {
	sessionMgr *SessionManager
	agentInfo  AgentInfo
	authProv   AuthProvider
	logger     *slog.Logger
	reader     *bufio.Reader
	rawReader  io.Reader // underlying reader for read deadline support
	writer     io.Writer
	writerMu   sync.Mutex

	// Outbound client requests (e.g. session/request_permission) keyed by id.
	nextOutID atomic.Int64
	pending   map[string]chan acpResponse
	pendingMu sync.Mutex

	// Capabilities advertised by the client in initialize (fs, terminal).
	clientCaps   *ClientCapabilities
	clientCapsMu sync.RWMutex
}

// acpResponse carries a routed client response to a waiting outbound request.
type acpResponse struct {
	result json.RawMessage
	err    error
}

type ServerConfig struct {
	SessionManager *SessionManager
	AgentInfo      AgentInfo
	AuthProvider   AuthProvider
	Logger         *slog.Logger
	Reader         io.Reader
	Writer         io.Writer
}

func NewServer(cfg ServerConfig) *Server {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	}
	if cfg.Reader == nil {
		cfg.Reader = os.Stdin
	}
	if cfg.Writer == nil {
		cfg.Writer = os.Stdout
	}
	if cfg.AuthProvider == nil {
		cfg.AuthProvider = &noopAuthProvider{}
	}

	return &Server{
		sessionMgr: cfg.SessionManager,
		agentInfo:  cfg.AgentInfo,
		authProv:   cfg.AuthProvider,
		logger:     cfg.Logger,
		reader:     bufio.NewReader(cfg.Reader),
		rawReader:  cfg.Reader,
		writer:     cfg.Writer,
		pending:    make(map[string]chan acpResponse),
	}
}

// isTimeoutError returns true when an I/O error is due to a read deadline expiry.
func isTimeoutError(err error) bool {
	if e, ok := err.(interface{ Timeout() bool }); ok {
		return e.Timeout()
	}
	return false
}

func (s *Server) Run(ctx context.Context) error {
	s.logger.Info("ACP server starting on stdio")

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		// Set a read deadline on the raw reader (e.g. stdin) so that
		// ReadBytes doesn't block forever on a partial line. If the
		// underlying reader doesn't support deadlines, this is a no-op.
		if f, ok := s.rawReader.(interface{ SetReadDeadline(t time.Time) error }); ok {
			f.SetReadDeadline(time.Now().Add(5 * time.Minute))
		}

		line, err := s.reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			// Timeout is not fatal — loop back to check ctx.Done().
			if isTimeoutError(err) {
				continue
			}
			return fmt.Errorf("read stdin: %w", err)
		}

		var req JSONRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			s.writeError(nil, -32700, "Parse error", err.Error())
			continue
		}

		if req.JSONRPC != "2.0" {
			s.writeError(req.ID, -32600, "Invalid Request", "jsonrpc must be 2.0")
			continue
		}

		// A message with an id but no method is a response to one of our
		// outbound client requests (e.g. session/request_permission). Route it
		// to the waiting caller instead of treating it as a request.
		if req.Method == "" && req.ID != nil {
			s.deliverClientResponse(req.ID, line)
			continue
		}

		s.handleRequest(ctx, &req)
	}
}

// sendRequest issues an outbound JSON-RPC request to the client and waits for
// the response. Used for client-side methods like session/request_permission.
func (s *Server) sendRequest(method string, params any, timeout time.Duration) (json.RawMessage, error) {
	idStr := fmt.Sprintf("acp-out-%d", s.nextOutID.Add(1))
	paramsBytes, _ := json.Marshal(params)
	reqBytes, err := json.Marshal(JSONRPCRequest{JSONRPC: "2.0", ID: idStr, Method: method, Params: paramsBytes})
	if err != nil {
		return nil, err
	}

	ch := make(chan acpResponse, 1)
	s.pendingMu.Lock()
	s.pending[idStr] = ch
	s.pendingMu.Unlock()
	defer func() {
		s.pendingMu.Lock()
		delete(s.pending, idStr)
		s.pendingMu.Unlock()
	}()

	s.writerMu.Lock()
	_, werr := fmt.Fprintf(s.writer, "%s\n", reqBytes)
	s.writerMu.Unlock()
	if werr != nil {
		return nil, werr
	}

	select {
	case <-time.After(timeout):
		return nil, fmt.Errorf("client request %s timed out", method)
	case r := <-ch:
		return r.result, r.err
	}
}

// deliverClientResponse routes a client response to the waiting sendRequest.
func (s *Server) deliverClientResponse(id any, line []byte) {
	idStr, ok := id.(string)
	if !ok {
		return
	}
	s.pendingMu.Lock()
	ch := s.pending[idStr]
	s.pendingMu.Unlock()
	if ch == nil {
		return
	}
	var resp JSONRPCResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		ch <- acpResponse{err: err}
		return
	}
	if resp.Error != nil {
		ch <- acpResponse{err: resp.Error}
		return
	}
	ch <- acpResponse{result: resp.Result}
}

// RequestPermission asks the client (editor) to authorize a tool call and
// returns the user's outcome. Mirrors ACP's session/request_permission.
func (s *Server) RequestPermission(sessionID string, tc PermissionToolCall, options []PermissionOption) (*PermissionOutcome, error) {
	raw, err := s.sendRequest("session/request_permission", RequestPermissionParams{
		SessionID: sessionID,
		ToolCall:  tc,
		Options:   options,
	}, 5*time.Minute)
	if err != nil {
		return nil, err
	}
	var res RequestPermissionResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	return &res.Outcome, nil
}

// DefaultPermissionOptions are the standard allow/reject choices presented to
// the user for a tool-call permission request.
func DefaultPermissionOptions() []PermissionOption {
	return []PermissionOption{
		{OptionID: "allow_once", Name: "Allow", Kind: "allow_once"},
		{OptionID: "allow_always", Name: "Always allow", Kind: "allow_always"},
		{OptionID: "reject_once", Name: "Reject", Kind: "reject_once"},
	}
}

// clientSupportsFS reports whether the client advertised filesystem capability,
// meaning the agent should read/write through the editor (seeing unsaved
// buffers) instead of touching disk directly.
func (s *Server) clientSupportsFS() bool {
	s.clientCapsMu.RLock()
	defer s.clientCapsMu.RUnlock()
	return s.clientCaps != nil && s.clientCaps.FS != nil &&
		(s.clientCaps.FS.ReadTextFile || s.clientCaps.FS.WriteTextFile)
}

// ReadTextFile reads a file through the client (editor), seeing unsaved buffers.
func (s *Server) ReadTextFile(sessionID, path string) ([]byte, error) {
	raw, err := s.sendRequest("fs/read_text_file", map[string]any{
		"sessionId": sessionID,
		"path":      path,
	}, 30*time.Second)
	if err != nil {
		return nil, err
	}
	var res struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	return []byte(res.Content), nil
}

// WriteTextFile writes a file through the client (editor).
func (s *Server) WriteTextFile(sessionID, path string, content []byte) error {
	_, err := s.sendRequest("fs/write_text_file", map[string]any{
		"sessionId": sessionID,
		"path":      path,
		"content":   string(content),
	}, 30*time.Second)
	return err
}

// sessionFS adapts the server's fs methods to the per-session ACPFileSystem.
type sessionFS struct {
	server    *Server
	sessionID string
}

func (f *sessionFS) ReadTextFile(path string) ([]byte, error) {
	return f.server.ReadTextFile(f.sessionID, path)
}

func (f *sessionFS) WriteTextFile(path string, content []byte) error {
	return f.server.WriteTextFile(f.sessionID, path, content)
}

func (s *Server) writeResponse(id any, result any) {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
	}

	resultBytes, err := json.Marshal(result)
	if err != nil {
		s.writeError(id, -32603, "Internal error", err.Error())
		return
	}
	resp.Result = resultBytes

	data, _ := json.Marshal(resp)
	s.writerMu.Lock()
	fmt.Fprintf(s.writer, "%s\n", data)
	s.writerMu.Unlock()
}

func (s *Server) writeNotification(method string, params any) {
	paramsBytes, _ := json.Marshal(params)
	notif := JSONRPCNotification{
		JSONRPC: "2.0",
		Method:  method,
		Params:  paramsBytes,
	}
	data, _ := json.Marshal(notif)
	s.writerMu.Lock()
	fmt.Fprintf(s.writer, "%s\n", data)
	s.writerMu.Unlock()
}

func (s *Server) writeError(id any, code int, message string, data any) {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &JSONRPCError{
			Code:    code,
			Message: message,
			Data:    data,
		},
	}
	respData, _ := json.Marshal(resp)
	s.writerMu.Lock()
	fmt.Fprintf(s.writer, "%s\n", respData)
	s.writerMu.Unlock()
}

func (s *Server) handleRequest(ctx context.Context, req *JSONRPCRequest) {
	defer func() {
		if r := recover(); r != nil {
			err := fmt.Errorf("panic in ACP handler: %v", r)
			s.logger.Error("ACP handler panic", "err", err)
			s.writeError(req.ID, -32603, "Internal error", err.Error())
		}
	}()
	switch req.Method {
	case "initialize":
		s.handleInitialize(req)
	case "authenticate":
		s.handleAuthenticate(req)
	case "session/new":
		s.handleNewSession(req)
	case "session/load":
		s.handleLoadSession(req)
	case "session/resume":
		s.handleResumeSession(req)
	case "session/fork":
		s.handleForkSession(req)
	case "session/list":
		s.handleListSessions(req)
	case "session/prompt":
		s.handlePrompt(ctx, req)
	case "session/cancel":
		s.handleCancel(req)
	case "session/set_mode":
		s.handleSetMode(req)
	case "session/set_model":
		s.handleSetModel(req)
	default:
		s.logger.Warn("unknown ACP method", "method", req.Method)
		s.writeError(req.ID, -32601, "Method not found", req.Method)
	}
}

func (s *Server) handleInitialize(req *JSONRPCRequest) {
	var params InitializeParams
	if req.Params != nil {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			s.writeError(req.ID, -32602, "Invalid params", err.Error())
			return
		}
	}

	clientName := "unknown"
	if params.ClientInfo != nil {
		clientName = params.ClientInfo.Name
	}
	s.clientCapsMu.Lock()
	s.clientCaps = params.ClientCapabilities
	s.clientCapsMu.Unlock()
	s.logger.Info("ACP initialize", "client", clientName)

	result := InitializeResult{
		ProtocolVersion: ProtocolVersion,
		AgentCapabilities: AgentCapabilities{
			LoadSession: true,
			PromptCapabilities: &PromptCapabilities{
				Image: false,
			},
			SessionCapabilities: &SessionCapabilities{
				Fork:   &SessionForkCapabilities{},
				List:   &SessionListCapabilities{},
				Resume: &SessionResumeCapabilities{},
			},
		},
		AuthMethods: s.authProv.AuthMethods(),
	}

	s.writeResponse(req.ID, result)
}

func (s *Server) handleAuthenticate(req *JSONRPCRequest) {
	var params AuthenticateParams
	if req.Params != nil {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			s.writeError(req.ID, -32602, "Invalid params", err.Error())
			return
		}
	}

	_ = params
	s.writeResponse(req.ID, AuthenticateResult{})
}

func (s *Server) handleNewSession(req *JSONRPCRequest) {
	var params NewSessionParams
	if req.Params != nil {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			s.writeError(req.ID, -32602, "Invalid params", err.Error())
			return
		}
	}

	cwd := params.CWD
	if cwd == "" {
		cwd = "."
	}

	state, err := s.sessionMgr.CreateSession(cwd, "")
	if err != nil {
		s.logger.Error("create session failed", "err", err)
		s.writeError(req.ID, -32603, "Internal error", err.Error())
		return
	}

	result := NewSessionResult{
		SessionID: state.SessionID,
		Models:    s.buildModelState(state),
		Modes:     s.buildModeState(),
	}

	s.writeResponse(req.ID, result)
}

func (s *Server) handleLoadSession(req *JSONRPCRequest) {
	var params LoadSessionParams
	if req.Params != nil {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			s.writeError(req.ID, -32602, "Invalid params", err.Error())
			return
		}
	}

	state := s.sessionMgr.UpdateCWD(params.SessionID, params.CWD)
	if state == nil {
		restored, err := s.sessionMgr.RestoreSession(params.SessionID)
		if err != nil {
			s.writeError(req.ID, -32002, "Session not found", params.SessionID)
			return
		}
		state = restored
		state = s.sessionMgr.UpdateCWD(params.SessionID, params.CWD)
	}

	result := LoadSessionResult{
		Models: s.buildModelState(state),
		Modes:  s.buildModeState(),
	}

	s.writeResponse(req.ID, result)
}

func (s *Server) handleResumeSession(req *JSONRPCRequest) {
	var params ResumeSessionParams
	if req.Params != nil {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			s.writeError(req.ID, -32602, "Invalid params", err.Error())
			return
		}
	}

	state := s.sessionMgr.UpdateCWD(params.SessionID, params.CWD)
	if state == nil {
		restored, err := s.sessionMgr.RestoreSession(params.SessionID)
		if err != nil {
			s.logger.Warn("resume session not found, creating new", "session_id", params.SessionID)
			state, err = s.sessionMgr.CreateSession(params.CWD, "")
			if err != nil {
				s.writeError(req.ID, -32603, "Internal error", err.Error())
				return
			}
		} else {
			state = restored
			state = s.sessionMgr.UpdateCWD(params.SessionID, params.CWD)
		}
	}

	result := ResumeSessionResult{
		Models: s.buildModelState(state),
		Modes:  s.buildModeState(),
	}

	s.writeResponse(req.ID, result)
}

func (s *Server) handleForkSession(req *JSONRPCRequest) {
	var params ForkSessionParams
	if req.Params != nil {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			s.writeError(req.ID, -32602, "Invalid params", err.Error())
			return
		}
	}

	cwd := params.CWD
	if cwd == "" {
		cwd = "."
	}

	state, err := s.sessionMgr.ForkSession(params.SessionID, cwd)
	if err != nil {
		s.logger.Error("fork session failed", "err", err)
		s.writeError(req.ID, -32603, "Internal error", err.Error())
		return
	}

	result := ForkSessionResult{
		SessionID: state.SessionID,
		Models:    s.buildModelState(state),
		Modes:     s.buildModeState(),
	}

	s.writeResponse(req.ID, result)
}

func (s *Server) handleListSessions(req *JSONRPCRequest) {
	var params ListSessionsParams
	if req.Params != nil {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			s.writeError(req.ID, -32602, "Invalid params", err.Error())
			return
		}
	}

	sessions := s.sessionMgr.ListSessions(params.CWD)

	result := ListSessionsResult{
		Sessions: sessions,
	}

	s.writeResponse(req.ID, result)
}

func (s *Server) handlePrompt(ctx context.Context, req *JSONRPCRequest) {
	var params struct {
		SessionID string            `json:"sessionId"`
		Prompt    []json.RawMessage `json:"prompt"`
	}
	if req.Params != nil {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			s.writeError(req.ID, -32602, "Invalid params", err.Error())
			return
		}
	}

	state := s.sessionMgr.GetSession(params.SessionID)
	if state == nil {
		s.logger.Error("prompt session not found", "session_id", params.SessionID)
		s.writeResponse(req.ID, PromptResponse{StopReason: "refusal"})
		return
	}

	// Route tool-call authorization to the client (editor) when supported.
	if pa, ok := state.Agent.(PermissionAware); ok {
		sid := params.SessionID
		pa.SetPermissionRequester(func(toolCallID, name string, rawInput any) bool {
			outcome, err := s.RequestPermission(sid, PermissionToolCall{
				ToolCallID: toolCallID,
				Title:      name,
				Kind:       ToolKind(name),
				Status:     "pending",
				RawInput:   rawInput,
			}, DefaultPermissionOptions())
			if err != nil || outcome == nil || outcome.Outcome != "selected" {
				return false // error/cancelled → deny (these are dangerous-tool gates)
			}
			return strings.HasPrefix(outcome.OptionID, "allow")
		})
	}

	// Route file reads/writes through the editor when the client supports it.
	if fsa, ok := state.Agent.(FileSystemAware); ok && s.clientSupportsFS() {
		fsa.SetFileSystem(&sessionFS{server: s, sessionID: params.SessionID})
	}

	if state.IsRunning() {
		s.logger.Warn("session already running, cancelling previous", "session_id", params.SessionID)
		state.Cancel()
		s.sessionMgr.SetIdle(params.SessionID)
	}

	userText := extractPromptContent(params.Prompt)
	if strings.TrimSpace(userText) == "" {
		s.writeResponse(req.ID, PromptResponse{StopReason: "end_turn"})
		return
	}

	s.logger.Info("ACP prompt", "session_id", params.SessionID, "text", truncateStr(userText, 100))

	s.sendNotification(params.SessionID, "session/update", SessionNotification{
		SessionID: params.SessionID,
		Update: SessionUpdate{
			SessionUpdate: "user_message_chunk",
			Content:       TextContentBlock{Type: "text", Text: userText},
		},
	})

	agentCtx, cancel := context.WithCancel(ctx)
	s.sessionMgr.SetRunning(params.SessionID, cancel)

	core := state.Agent.Core()
	unregisterEvents := RegisterEventListeners(params.SessionID, core, func(method string, p any) {
		s.sendNotification(params.SessionID, method, p)
	})

	go func() {
		defer unregisterEvents()
		defer func() {
			s.sessionMgr.SetIdle(params.SessionID)
		}()

		result, err := state.Agent.Run(agentCtx, userText)
		if err != nil {
			s.logger.Error("agent run failed", "err", err)
		}

		if result != "" {
			s.sendNotification(params.SessionID, "session/update", SessionNotification{
					SessionID: params.SessionID,
					Update: SessionUpdate{
						SessionUpdate: "agent_message_chunk",
						Content:       TextContentBlock{Type: "text", Text: result},
					},
			})
		}

		s.writeResponse(req.ID, PromptResponse{
			StopReason: "end_turn",
		})
	}()
}

func (s *Server) handleCancel(req *JSONRPCRequest) {
	var params CancelParams
	if req.Params != nil {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			s.writeError(req.ID, -32602, "Invalid params", err.Error())
			return
		}
	}

	state := s.sessionMgr.GetSession(params.SessionID)
	if state != nil {
		state.Cancel()
		s.logger.Info("ACP session cancelled", "session_id", params.SessionID)
	}

	s.writeResponse(req.ID, nil)
}

func (s *Server) handleSetMode(req *JSONRPCRequest) {
	var params SetSessionModeParams
	if req.Params != nil {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			s.writeError(req.ID, -32602, "Invalid params", err.Error())
			return
		}
	}

	if s.sessionMgr.GetSession(params.SessionID) == nil {
		s.writeError(req.ID, -32002, "Session not found", params.SessionID)
		return
	}

	if err := s.sessionMgr.UpdateMode(params.SessionID, params.ModeID); err != nil {
		s.writeError(req.ID, -32003, "Update mode failed", err.Error())
		return
	}

	// Rebuild agent so the new mode's system prompt takes effect
	state := s.sessionMgr.GetSession(params.SessionID)
	if r, ok := state.Agent.(Rebuildable); ok {
		if err := r.Rebuild(params.ModeID, state.Model); err != nil {
			slog.Error("rebuild agent for mode change", "err", err)
		}
	}

	s.writeResponse(req.ID, nil)
}

func (s *Server) handleSetModel(req *JSONRPCRequest) {
	var params SetSessionModelParams
	if req.Params != nil {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			s.writeError(req.ID, -32602, "Invalid params", err.Error())
			return
		}
	}

	if s.sessionMgr.GetSession(params.SessionID) == nil {
		s.writeError(req.ID, -32002, "Session not found", params.SessionID)
		return
	}

	if err := s.sessionMgr.UpdateModel(params.SessionID, params.ModelID); err != nil {
		s.writeError(req.ID, -32003, "Update model failed", err.Error())
		return
	}

	// Rebuild agent so the new model takes effect
	state := s.sessionMgr.GetSession(params.SessionID)
	if r, ok := state.Agent.(Rebuildable); ok {
		if err := r.Rebuild(state.Mode, params.ModelID); err != nil {
			slog.Error("rebuild agent for model change", "err", err)
		}
	}

	s.writeResponse(req.ID, nil)
}

func (s *Server) buildModelState(state *sessionState) *SessionModelState {
	if state == nil {
		return nil
	}
	return &SessionModelState{
		AvailableModels: []ModelInfo{
			{
				ModelID:     state.Model,
				Name:        state.Model,
				Description: "current",
			},
		},
		CurrentModelID: state.Model,
	}
}

func (s *Server) buildModeState() *SessionModeState {
	modes := s.sessionMgr.agentFactory.AvailableModes()
	return &SessionModeState{
		CurrentModeID:  s.sessionMgr.agentFactory.DefaultMode(),
		AvailableModes: modes,
	}
}

func (s *Server) sendNotification(sessionID string, method string, params any) {
	s.writeNotification(method, params)
}

// extractPromptContent flattens an ACP prompt (an array of content blocks) into
// a single text prompt. It handles every block type Zed may send: plain text,
// embedded resources (file contents inlined), resource links (file references),
// and images/audio (acknowledged so the model knows an attachment was sent).
func extractPromptContent(blocks []json.RawMessage) string {
	var parts []string
	for _, raw := range blocks {
		var b struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			URI      string `json:"uri"`
			Name     string `json:"name"`
			Resource *struct {
				URI  string `json:"uri"`
				Text string `json:"text"`
			} `json:"resource"`
		}
		if err := json.Unmarshal(raw, &b); err != nil {
			continue
		}
		switch b.Type {
		case "text":
			if b.Text != "" {
				parts = append(parts, b.Text)
			}
		case "resource":
			if b.Resource == nil {
				continue
			}
			if b.Resource.Text != "" {
				parts = append(parts, fmt.Sprintf("<file uri=%q>\n%s\n</file>", b.Resource.URI, b.Resource.Text))
			} else if b.Resource.URI != "" {
				parts = append(parts, fmt.Sprintf("[referenced resource: %s]", b.Resource.URI))
			}
		case "resource_link":
			if b.URI == "" {
				continue
			}
			if b.Name != "" {
				parts = append(parts, fmt.Sprintf("[referenced file: %s (%s)]", b.Name, b.URI))
			} else {
				parts = append(parts, fmt.Sprintf("[referenced file: %s]", b.URI))
			}
		case "image":
			parts = append(parts, "[image attached]")
		case "audio":
			parts = append(parts, "[audio attached]")
		}
	}
	return strings.Join(parts, "\n")
}

type noopAuthProvider struct{}

func (n *noopAuthProvider) AuthMethods() []any {
	return []any{}
}