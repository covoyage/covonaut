package a2a

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/covoyage/covonaut/agentcore"
	"slices"
)

type contextKey string

const lastEventIDKey contextKey = "lastEventID"

func withLastEventID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, lastEventIDKey, id)
}

func lastEventIDFromCtx(ctx context.Context) int {
	v := ctx.Value(lastEventIDKey)
	if v == nil {
		return 0
	}
	s, ok := v.(string)
	if !ok {
		return 0
	}
	n, _ := strconv.Atoi(s)
	return n
}

const (
	defaultMaxRequestBody = 10 << 20 // 10 MB
	defaultTaskTimeout    = 5 * time.Minute
)

// ---------------------------------------------------------------------------
// AgentHandler is the interface that agents must implement to be exposed
// via the A2A protocol.
// ---------------------------------------------------------------------------

type AgentHandler interface {
	Card() AgentCard
	SendTask(ctx context.Context, req SendTaskRequest) (*Task, error)
	GetTask(ctx context.Context, req GetTaskRequest) (*Task, error)
	CancelTask(ctx context.Context, req CancelTaskRequest) (*Task, error)
	QueryTasks(ctx context.Context, req QueryTasksRequest) (*QueryTasksResult, error)
	SetPushNotification(ctx context.Context, req SetPushNotificationRequest) error
	GetPushNotification(ctx context.Context, taskID string) (*PushNotificationConfig, error)
}

type TaskUpdatePublisher interface {
	PublishTaskUpdate(taskID string, ev *TaskUpdateEvent)
}

type StreamingHandler interface {
	SetUpdatePublisher(TaskUpdatePublisher)
}

// ---------------------------------------------------------------------------
type taskSubState struct {
	mu        sync.RWMutex
	subs      []chan *TaskUpdateEvent
	history   []historyEntry
	nextSeq   int
}

type historyEntry struct {
	event *TaskUpdateEvent
	seq   int
}

// Server exposes an AgentHandler over HTTP/SSE with A2A protocol endpoints.
type Server struct {
	handler AgentHandler
	cors    CORSConfig
	auth    AuthConfig
	srv     *http.Server
	logger  *slog.Logger

	taskTTL       time.Duration
	cleanupTicker *time.Ticker
	cleanupStop   chan struct{}

	rateLimiter *RateLimiter

	sessionMgr *SessionManager
	sessionTTL time.Duration

	taskStates    map[string]*taskSubState
	taskStatesMu  sync.RWMutex
	totalHistSize atomic.Int64
	maxHistoryLen int
	maxTotalHist  int

	maxRequestBody  int64
	taskTimeout     time.Duration
	requestTimeout  time.Duration
}

// CORSConfig configures cross-origin resource sharing.
type CORSConfig struct {
	AllowOrigins     []string
	AllowMethods     []string
	AllowHeaders     []string
	AllowCredentials bool
}

// AuthConfig configures authentication for the A2A server.
type AuthConfig struct {
	APIKey      string
	BearerToken string
}

// ServerOption configures a Server.
type ServerOption func(*Server)

func WithLogger(logger *slog.Logger) ServerOption {
	return func(s *Server) { s.logger = logger }
}

func WithTaskTTL(ttl time.Duration) ServerOption {
	return func(s *Server) { s.taskTTL = ttl }
}

func WithRateLimiter(limiter *RateLimiter) ServerOption {
	return func(s *Server) { s.rateLimiter = limiter }
}

func WithSessionManager(ttl time.Duration) ServerOption {
	return func(s *Server) {
		s.sessionMgr = NewSessionManager()
		s.sessionTTL = ttl
	}
}

func WithCORS(cfg CORSConfig) ServerOption {
	return func(s *Server) { s.cors = cfg }
}

func WithAuth(cfg AuthConfig) ServerOption {
	return func(s *Server) { s.auth = cfg }
}

func WithMaxRequestBody(n int64) ServerOption {
	return func(s *Server) { s.maxRequestBody = n }
}

func WithTaskTimeout(d time.Duration) ServerOption {
	return func(s *Server) { s.taskTimeout = d }
}

func WithRequestTimeout(d time.Duration) ServerOption {
	return func(s *Server) { s.requestTimeout = d }
}

func WithMaxEventHistory(perTask, total int) ServerOption {
	return func(s *Server) {
		s.maxHistoryLen = perTask
		s.maxTotalHist = total
	}
}

// NewServer creates an A2A server wrapping the given handler.
func NewServer(handler AgentHandler, opts ...ServerOption) *Server {
	s := &Server{
		handler:        handler,
		taskStates:     make(map[string]*taskSubState),
		maxHistoryLen:  64,
		maxTotalHist:   10000,
		logger:         slog.Default(),
		maxRequestBody:  defaultMaxRequestBody,
		taskTimeout:     defaultTaskTimeout,
		requestTimeout:  30 * time.Second,
	}
	for _, opt := range opts {
		opt(s)
	}
	if sh, ok := s.handler.(StreamingHandler); ok {
		sh.SetUpdatePublisher(s)
	}

	if s.taskTTL > 0 {
		s.startCleanup()
	}
	return s
}

func (s *Server) getTaskState(taskID string) *taskSubState {
	s.taskStatesMu.RLock()
	ts, ok := s.taskStates[taskID]
	s.taskStatesMu.RUnlock()
	if ok {
		return ts
	}

	s.taskStatesMu.Lock()
	defer s.taskStatesMu.Unlock()

	if ts, ok = s.taskStates[taskID]; ok {
		return ts
	}

	ts = &taskSubState{}
	s.taskStates[taskID] = ts
	return ts
}

func (s *Server) startCleanup() {
	s.cleanupTicker = time.NewTicker(s.taskTTL / 2)
	s.cleanupStop = make(chan struct{})
	go func() {
		for {
			select {
			case <-s.cleanupTicker.C:
				s.purgeOldTasks()
			case <-s.cleanupStop:
				return
			}
		}
	}()
}

func (s *Server) purgeOldTasks() {
	cutoff := time.Now().Add(-s.taskTTL)

	s.taskStatesMu.Lock()
	var toDelete []string
	for id, ts := range s.taskStates {
		ts.mu.RLock()
		hist := ts.history
		if len(hist) == 0 {
			ts.mu.RUnlock()
			continue
		}
		lastEv := hist[len(hist)-1]
		ts.mu.RUnlock()

		var lastUpdate time.Time
		if lastEv.event.Result != nil && len(lastEv.event.Result.History) > 0 {
			lastUpdate = lastEv.event.Result.History[len(lastEv.event.Result.History)-1].Timestamp
		}
		if lastUpdate.IsZero() {
			continue
		}
		if lastUpdate.Before(cutoff) {
			toDelete = append(toDelete, id)
		}
	}

	for _, id := range toDelete {
		ts := s.taskStates[id]
		ts.mu.Lock()
		for _, ch := range ts.subs {
			close(ch)
		}
		ts.subs = nil
		removed := len(ts.history)
		ts.history = nil
		ts.mu.Unlock()
		s.totalHistSize.Add(-int64(removed))
		delete(s.taskStates, id)
		s.logger.Debug("purged old task history", "task_id", id)
	}
	s.taskStatesMu.Unlock()

	if s.sessionMgr != nil && s.sessionTTL > 0 {
		sessionCutoff := time.Now().Add(-s.sessionTTL)
		count := s.sessionMgr.PurgeStale(sessionCutoff)
		if count > 0 {
			s.logger.Debug("purged stale sessions", "count", count)
		}
	}
}

func isTerminalState(state TaskState) bool {
	switch state {
	case TaskStateCompleted, TaskStateFailed, TaskStateCanceled:
		return true
	default:
		return false
	}
}

// ValidateCard checks that an AgentCard has required fields.
func ValidateCard(card AgentCard) error {
	if card.Name == "" {
		return fmt.Errorf("agent card: name is required")
	}
	if card.URL == "" {
		return fmt.Errorf("agent card: url is required")
	}
	for i, skill := range card.Skills {
		if skill.ID == "" {
			return fmt.Errorf("agent card: skill[%d]: id is required", i)
		}
		if skill.Name == "" {
			return fmt.Errorf("agent card: skill[%d]: name is required", i)
		}
		if err := validateSkillParameters(skill.Parameters, i); err != nil {
			return err
		}
	}
	return nil
}

func validateSkillParameters(params map[string]any, skillIndex int) error {
	if params == nil {
		return nil
	}
	schemaType, ok := params["type"]
	if !ok {
		return fmt.Errorf("agent card: skill[%d]: parameters must have a 'type' field (JSON Schema)", skillIndex)
	}
	typeStr, ok := schemaType.(string)
	if !ok {
		return fmt.Errorf("agent card: skill[%d]: parameters 'type' must be a string", skillIndex)
	}
	validTypes := map[string]bool{
		"object":  true,
		"array":   true,
		"string":  true,
		"number":  true,
		"integer": true,
		"boolean": true,
		"null":    true,
	}
	if !validTypes[typeStr] {
		return fmt.Errorf("agent card: skill[%d]: parameters has invalid JSON Schema type %q", skillIndex, typeStr)
	}
	if typeStr == "object" {
		if props, ok := params["properties"]; ok {
			if _, ok := props.(map[string]any); !ok {
				return fmt.Errorf("agent card: skill[%d]: parameters 'properties' must be an object", skillIndex)
			}
		}
	}
	return nil
}

// Handler returns an http.Handler with all A2A routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/agent.json", s.handleAgentCard)
	mux.HandleFunc("/", s.handleJSONRPC)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/ws", s.handleWebSocket)

	h := withAuth(withCORS(mux, s.cors), s.auth)
	if s.rateLimiter != nil {
		h = s.rateLimiter.Middleware(h)
	}
	return h
}

// ListenAndServe starts the HTTP server.
func (s *Server) ListenAndServe(addr string) error {
	s.srv = &http.Server{Addr: addr, Handler: s.Handler()}
	return s.srv.ListenAndServe()
}

// Shutdown gracefully shuts down the server and closes all subscriptions.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.cleanupTicker != nil {
		s.cleanupTicker.Stop()
		close(s.cleanupStop)
	}

	if s.rateLimiter != nil {
		s.rateLimiter.Stop()
	}

	s.taskStatesMu.Lock()
	for id, ts := range s.taskStates {
		ts.mu.Lock()
		for _, ch := range ts.subs {
			close(ch)
		}
		ts.subs = nil
		ts.mu.Unlock()
		delete(s.taskStates, id)
	}
	s.taskStatesMu.Unlock()

	if s.srv == nil {
		return nil
	}
	return s.srv.Shutdown(ctx)
}

// ---------------------------------------------------------------------------
// HTTP Handlers
// ---------------------------------------------------------------------------

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) handleAgentCard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	card := s.handler.Card()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(card)
}

func (s *Server) handleJSONRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONRPCError(w, nil, JSONRPCInvalidRequest, "only POST allowed")
		return
	}
	contentType := r.Header.Get("Content-Type")
	if !strings.Contains(contentType, "application/json") {
		writeJSONRPCError(w, nil, JSONRPCInvalidRequest, "Content-Type must be application/json")
		return
	}

	lr := io.LimitReader(r.Body, s.maxRequestBody)
	body, err := io.ReadAll(lr)
	if err != nil {
		writeJSONRPCError(w, nil, JSONRPCParseError, err.Error())
		return
	}

	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		writeJSONRPCError(w, nil, JSONRPCParseError, "empty request body")
		return
	}

	if trimmed[0] == '[' {
		var reqs []JSONRPCRequest
		if err := json.Unmarshal(body, &reqs); err != nil {
			writeJSONRPCError(w, nil, JSONRPCParseError, err.Error())
			return
		}
		s.handleBatchJSONRPC(w, r, reqs)
		return
	}

	var req JSONRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSONRPCError(w, nil, JSONRPCParseError, err.Error())
		return
	}

	s.dispatchJSONRPC(withLastEventID(r.Context(), r.Header.Get("Last-Event-ID")), w, req)
}

func (s *Server) handleBatchJSONRPC(w http.ResponseWriter, r *http.Request, reqs []JSONRPCRequest) {
	if len(reqs) == 0 {
		writeJSONRPCError(w, nil, JSONRPCInvalidRequest, "empty batch")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	var results []JSONRPCResponse

	for _, req := range reqs {
		if req.JSONRPC != "2.0" {
			results = append(results, JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &JSONRPCError{Code: JSONRPCInvalidRequest, Message: "jsonrpc must be 2.0"},
			})
			continue
		}

		if req.Method == "tasks/sendSubscribe" || req.Method == "tasks/resubscribe" {
			results = append(results, JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &JSONRPCError{Code: JSONRPCInvalidRequest, Message: "streaming methods not allowed in batch requests"},
			})
			continue
		}

		rec := httptestNewRecorder()
		s.dispatchJSONRPC(withLastEventID(r.Context(), r.Header.Get("Last-Event-ID")), rec, req)

		var resp JSONRPCResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err == nil {
			results = append(results, resp)
		}
	}

	_ = json.NewEncoder(w).Encode(results)
}

func (s *Server) dispatchJSONRPC(ctx context.Context, w http.ResponseWriter, req JSONRPCRequest) {
	start := time.Now()
	s.logger.Debug("jsonrpc request", "method", req.Method, "id", req.ID)

	if req.JSONRPC != "2.0" {
		writeJSONRPCError(w, req.ID, JSONRPCInvalidRequest, "jsonrpc must be 2.0")
		return
	}

	card := s.handler.Card()

	isStreaming := req.Method == "tasks/sendSubscribe" || req.Method == "tasks/resubscribe"

	if !isStreaming && s.requestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.requestTimeout)
		defer cancel()
	}

	switch req.Method {
	case "tasks/send":
		s.handleSendTask(ctx, w, req)
	case "tasks/sendSubscribe":
		if !card.Capabilities.Streaming {
			writeJSONRPCError(w, req.ID, A2AErrorUnsupportedOperation, "streaming not supported")
			return
		}
		s.handleSendTaskSubscribe(ctx, w, req)
	case "tasks/get":
		s.handleGetTask(ctx, w, req)
	case "tasks/cancel":
		s.handleCancelTask(ctx, w, req)
	case "tasks/query":
		s.handleQueryTasks(ctx, w, req)
	case "tasks/pushNotification/set":
		if !card.Capabilities.PushNotifications {
			writeJSONRPCError(w, req.ID, A2AErrorPushNotSupported, "push notifications not supported")
			return
		}
		s.handleSetPushNotification(ctx, w, req)
	case "tasks/pushNotification/get":
		if !card.Capabilities.PushNotifications {
			writeJSONRPCError(w, req.ID, A2AErrorPushNotSupported, "push notifications not supported")
			return
		}
		s.handleGetPushNotification(ctx, w, req)
	case "tasks/resubscribe":
		if !card.Capabilities.Streaming {
			writeJSONRPCError(w, req.ID, A2AErrorUnsupportedOperation, "streaming not supported")
			return
		}
		s.handleResubscribe(ctx, w, req)
	default:
		writeJSONRPCError(w, req.ID, JSONRPCMethodNotFound, fmt.Sprintf("method %q not found", req.Method))
	}

	s.logger.Debug("jsonrpc complete", "method", req.Method, "id", req.ID, "duration", time.Since(start))
}

func (s *Server) handleSendTask(ctx context.Context, w http.ResponseWriter, req JSONRPCRequest) {
	var params SendTaskRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeJSONRPCError(w, req.ID, JSONRPCInvalidParams, err.Error())
		return
	}

	card := s.handler.Card()
	if len(card.DefaultInputModes) > 0 {
		requestedModes := ExtractInputModes(params.Message)
		if err := ValidateInputModes(requestedModes, card.DefaultInputModes); err != nil {
			writeJSONRPCError(w, req.ID, A2AErrorContentTypeNotSupported, err.Error())
			return
		}
	}

	task, err := s.handler.SendTask(ctx, params)
	if err != nil {
		writeJSONRPCError(w, req.ID, JSONRPCInternalError, err.Error())
		return
	}

	s.recordTask(task)

	if s.sessionMgr != nil && task.SessionID != "" {
		s.sessionMgr.AddTask(task.SessionID, task.ID)
	}

	writeJSONRPCResult(w, req.ID, task)
}

func (s *Server) handleSendTaskSubscribe(ctx context.Context, w http.ResponseWriter, req JSONRPCRequest) {
	var params SendTaskRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeJSONRPCError(w, req.ID, JSONRPCInvalidParams, err.Error())
		return
	}

	card := s.handler.Card()
	if len(card.DefaultInputModes) > 0 {
		requestedModes := ExtractInputModes(params.Message)
		if err := ValidateInputModes(requestedModes, card.DefaultInputModes); err != nil {
			writeJSONRPCError(w, req.ID, A2AErrorContentTypeNotSupported, err.Error())
			return
		}
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONRPCError(w, req.ID, JSONRPCInternalError, "streaming not supported")
		return
	}

	taskID := params.ID
	if taskID == "" {
		taskID = fmt.Sprintf("task-%d", time.Now().UnixNano())
		params.ID = taskID
	}

	ch := make(chan *TaskUpdateEvent, 16)
	ts := s.getTaskState(taskID)
	ts.mu.Lock()
	ts.subs = append(ts.subs, ch)
	ts.mu.Unlock()

	defer func() {
		ts.mu.Lock()
		for i, c := range ts.subs {
			if c == ch {
				ts.subs = append(ts.subs[:i], ts.subs[i+1:]...)
				break
			}
		}
		close(ch)
		ts.mu.Unlock()
	}()

	type taskResult struct {
		task *Task
		err  error
	}
	resultCh := make(chan taskResult, 1)
	go func() {
		task, err := s.handler.SendTask(ctx, params)
		resultCh <- taskResult{task, err}
	}()

	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	seq := 0

	for {
		select {
		case <-ctx.Done():
			return
		case r := <-resultCh:
			if r.err != nil {
				seq++
				s.writeSSEEvent(w, flusher, &TaskUpdateEvent{ID: req.ID, Error: &JSONRPCError{Code: JSONRPCInternalError, Message: r.err.Error()}, Final: true}, seq)
				return
			}
			s.recordTask(r.task)
			if s.sessionMgr != nil && r.task.SessionID != "" {
				s.sessionMgr.AddTask(r.task.SessionID, r.task.ID)
			}
			final := isTerminalState(r.task.State) || r.task.State == TaskStateInputRequired
			seq++
			if !s.writeSSEEvent(w, flusher, &TaskUpdateEvent{ID: req.ID, Result: r.task, Final: final}, seq) {
				return
			}
			if final {
				return
			}
		case <-heartbeat.C:
			if !s.writeSSEComment(w, flusher, "heartbeat") {
				return
			}
		case ev, ok := <-ch:
			if !ok {
				return
			}
			ev.ID = req.ID
			seq++
			if !s.writeSSEEvent(w, flusher, ev, seq) {
				return
			}
			if ev.Final {
				return
			}
		}
	}
}

func (s *Server) handleGetTask(ctx context.Context, w http.ResponseWriter, req JSONRPCRequest) {
	var params GetTaskRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeJSONRPCError(w, req.ID, JSONRPCInvalidParams, err.Error())
		return
	}

	task, err := s.handler.GetTask(ctx, params)
	if err != nil {
		writeJSONRPCError(w, req.ID, A2AErrorTaskNotFound, err.Error())
		return
	}

	s.recordTask(task)
	writeJSONRPCResult(w, req.ID, task)
}

func (s *Server) handleCancelTask(ctx context.Context, w http.ResponseWriter, req JSONRPCRequest) {
	var params CancelTaskRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeJSONRPCError(w, req.ID, JSONRPCInvalidParams, err.Error())
		return
	}

	task, err := s.handler.CancelTask(ctx, params)
	if err != nil {
		writeJSONRPCError(w, req.ID, A2AErrorTaskNotCancelable, err.Error())
		return
	}

	s.recordTask(task)
	writeJSONRPCResult(w, req.ID, task)
}

func (s *Server) handleQueryTasks(ctx context.Context, w http.ResponseWriter, req JSONRPCRequest) {
	var params QueryTasksRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeJSONRPCError(w, req.ID, JSONRPCInvalidParams, err.Error())
		return
	}

	result, err := s.handler.QueryTasks(ctx, params)
	if err != nil {
		writeJSONRPCError(w, req.ID, JSONRPCInternalError, err.Error())
		return
	}

	writeJSONRPCResult(w, req.ID, result)
}

func (s *Server) handleSetPushNotification(ctx context.Context, w http.ResponseWriter, req JSONRPCRequest) {
	var params SetPushNotificationRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeJSONRPCError(w, req.ID, JSONRPCInvalidParams, err.Error())
		return
	}

	if err := s.handler.SetPushNotification(ctx, params); err != nil {
		writeJSONRPCError(w, req.ID, JSONRPCInternalError, err.Error())
		return
	}

	writeJSONRPCResult(w, req.ID, map[string]string{"status": "ok"})
}

func (s *Server) handleGetPushNotification(ctx context.Context, w http.ResponseWriter, req JSONRPCRequest) {
	var params struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeJSONRPCError(w, req.ID, JSONRPCInvalidParams, err.Error())
		return
	}

	cfg, err := s.handler.GetPushNotification(ctx, params.ID)
	if err != nil {
		writeJSONRPCError(w, req.ID, JSONRPCInternalError, err.Error())
		return
	}

	writeJSONRPCResult(w, req.ID, cfg)
}

func (s *Server) handleResubscribe(ctx context.Context, w http.ResponseWriter, req JSONRPCRequest) {
	var params struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeJSONRPCError(w, req.ID, JSONRPCInvalidParams, err.Error())
		return
	}

	if _, err := s.handler.GetTask(ctx, GetTaskRequest{ID: params.ID}); err != nil {
		writeJSONRPCError(w, req.ID, JSONRPCInvalidParams, fmt.Sprintf("task %q not found", params.ID))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONRPCError(w, req.ID, JSONRPCInternalError, "streaming not supported")
		return
	}

	ts := s.getTaskState(params.ID)

	afterSeq := lastEventIDFromCtx(ctx)

	ts.mu.RLock()
	var replay []historyEntry
	if afterSeq > 0 {
		for _, entry := range ts.history {
			if entry.seq > afterSeq {
				replay = append(replay, entry)
			}
		}
	} else {
		replay = make([]historyEntry, len(ts.history))
		copy(replay, ts.history)
	}
	ts.mu.RUnlock()

	for _, entry := range replay {
		evCopy := *entry.event
		evCopy.ID = req.ID
		if !s.writeSSEEvent(w, flusher, &evCopy, entry.seq) {
			return
		}
		if evCopy.Final {
			return
		}
	}

	ch := make(chan *TaskUpdateEvent, 16)
	ts.mu.Lock()
	ts.subs = append(ts.subs, ch)
	ts.mu.Unlock()

	defer func() {
		ts.mu.Lock()
		for i, c := range ts.subs {
			if c == ch {
				ts.subs = append(ts.subs[:i], ts.subs[i+1:]...)
				break
			}
		}
		close(ch)
		ts.mu.Unlock()
	}()

	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			if !s.writeSSEComment(w, flusher, "heartbeat") {
				return
			}
		case ev, ok := <-ch:
			if !ok {
				return
			}
			ev.ID = req.ID
			ts.mu.Lock()
			ts.nextSeq++
			seq := ts.nextSeq
			trimmed := 0
			if len(ts.history) >= s.maxHistoryLen {
				trimmed = len(ts.history) - s.maxHistoryLen + 1
				ts.history = ts.history[trimmed:]
			}
			ts.history = append(ts.history, historyEntry{event: ev, seq: seq})
			chans := make([]chan *TaskUpdateEvent, len(ts.subs))
			copy(chans, ts.subs)
			ts.mu.Unlock()

			s.totalHistSize.Add(1 - int64(trimmed))

			if !s.writeSSEEvent(w, flusher, ev, seq) {
				return
			}
			if ev.Final {
				return
			}
		}
	}
}

// ---------------------------------------------------------------------------
// SSE helpers
// ---------------------------------------------------------------------------

func (s *Server) writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, ev *TaskUpdateEvent, eventID int) bool {
	data, err := json.Marshal(ev)
	if err != nil {
		return false
	}
	if eventID > 0 {
		_, err = fmt.Fprintf(w, "id: %d\ndata: %s\n\n", eventID, data)
	} else {
		_, err = fmt.Fprintf(w, "data: %s\n\n", data)
	}
	if err != nil {
		return false
	}
	flusher.Flush()
	return true
}

func (s *Server) writeSSEComment(w http.ResponseWriter, flusher http.Flusher, comment string) bool {
	_, err := fmt.Fprintf(w, ": %s\n\n", comment)
	if err != nil {
		return false
	}
	flusher.Flush()
	return true
}

// recordTask stores a task snapshot in event history for resubscription.
func (s *Server) recordTask(task *Task) {
	ts := s.getTaskState(task.ID)
	ts.mu.Lock()
	ev := &TaskUpdateEvent{Result: task, Final: isTerminalState(task.State)}
	ts.nextSeq++
	trimmed := 0
	if len(ts.history) >= s.maxHistoryLen {
		trimmed = len(ts.history) - s.maxHistoryLen + 1
		ts.history = ts.history[trimmed:]
	}
	ts.history = append(ts.history, historyEntry{event: ev, seq: ts.nextSeq})
	ts.mu.Unlock()

	newSize := s.totalHistSize.Add(1 - int64(trimmed))

	if newSize > int64(s.maxTotalHist) {
		s.taskStatesMu.Lock()
		for s.totalHistSize.Load() > int64(s.maxTotalHist) {
			oldest := ""
			oldestTime := time.Now()
			for id, t := range s.taskStates {
				t.mu.RLock()
				if len(t.history) > 0 {
					first := t.history[0].event
					if first.Result != nil && len(first.Result.History) > 0 && first.Result.History[0].Timestamp.Before(oldestTime) {
						oldestTime = first.Result.History[0].Timestamp
						oldest = id
					}
				}
				t.mu.RUnlock()
			}
			if oldest == "" {
				break
			}
			ots := s.taskStates[oldest]
			ots.mu.Lock()
			removed := len(ots.history)
			ots.mu.Unlock()
			s.totalHistSize.Add(-int64(removed))
			delete(s.taskStates, oldest)
		}
		s.taskStatesMu.Unlock()
	}
}

func (s *Server) PublishTaskUpdate(taskID string, ev *TaskUpdateEvent) {
	ts := s.getTaskState(taskID)
	ts.mu.Lock()
	ts.nextSeq++
	trimmed := 0
	if len(ts.history) >= s.maxHistoryLen {
		trimmed = len(ts.history) - s.maxHistoryLen + 1
		ts.history = ts.history[trimmed:]
	}
	ts.history = append(ts.history, historyEntry{event: ev, seq: ts.nextSeq})
	chans := make([]chan *TaskUpdateEvent, len(ts.subs))
	copy(chans, ts.subs)
	ts.mu.Unlock()

	s.totalHistSize.Add(1 - int64(trimmed))

	for _, ch := range chans {
		select {
		case ch <- ev:
		default:
		}
	}
}

// ---------------------------------------------------------------------------
// JSON-RPC helpers
// ---------------------------------------------------------------------------

func writeJSONRPCResult(w http.ResponseWriter, id any, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(JSONRPCResponse{JSONRPC: "2.0", ID: id, Result: result})
}

func writeJSONRPCError(w http.ResponseWriter, id any, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &JSONRPCError{Code: code, Message: message},
	})
}

type recordingResponseWriter struct {
	code int
	Body *bytes.Buffer
}

func httptestNewRecorder() *recordingResponseWriter {
	return &recordingResponseWriter{Body: new(bytes.Buffer)}
}

func (r *recordingResponseWriter) Header() http.Header        { return http.Header{} }
func (r *recordingResponseWriter) Write(b []byte) (int, error) { return r.Body.Write(b) }
func (r *recordingResponseWriter) WriteHeader(code int)        { r.code = code }

// ---------------------------------------------------------------------------
// Auth middleware
// ---------------------------------------------------------------------------

func withAuth(next http.Handler, cfg AuthConfig) http.Handler {
	if cfg.APIKey == "" && cfg.BearerToken == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authenticated := false

		if cfg.APIKey != "" {
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-API-Key")), []byte(cfg.APIKey)) == 1 {
				authenticated = true
			}
		}

		if !authenticated && cfg.BearerToken != "" {
			auth := r.Header.Get("Authorization")
			if strings.HasPrefix(auth, "Bearer ") && subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(auth, "Bearer ")), []byte(cfg.BearerToken)) == 1 {
				authenticated = true
			}
		}

		if !authenticated {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(JSONRPCResponse{
				JSONRPC: "2.0",
				Error:   &JSONRPCError{Code: JSONRPCInvalidRequest, Message: "unauthorized"},
			})
			return
		}

		next.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------------------
// CORS middleware
// ---------------------------------------------------------------------------

func withCORS(next http.Handler, cfg CORSConfig) http.Handler {
	if len(cfg.AllowOrigins) == 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowed := false
		// Note: a bare "*" entry only grants a match when credentials are not
		// allowed. Reflecting an arbitrary Origin while also sending
		// Access-Control-Allow-Credentials: true would let any site make
		// credentialed requests, defeating CORS protection.
		for _, o := range cfg.AllowOrigins {
			if o == origin || (o == "*" && !cfg.AllowCredentials) {
				allowed = true
				break
			}
		}
		if allowed {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			if cfg.AllowCredentials {
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}
			if len(cfg.AllowMethods) > 0 {
				w.Header().Set("Access-Control-Allow-Methods", strings.Join(cfg.AllowMethods, ", "))
			}
			if len(cfg.AllowHeaders) > 0 {
				w.Header().Set("Access-Control-Allow-Headers", strings.Join(cfg.AllowHeaders, ", "))
			}
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------------------
// DefaultAgentHandler bridges agentcore.Agent to A2A protocol.
// ---------------------------------------------------------------------------

type DefaultAgentHandler struct {
	card        AgentCard
	agent       *agentcore.Agent
	config      agentcore.Config
	taskTimeout time.Duration

	tasksMu  sync.RWMutex
	tasks    map[string]*Task
	maxTasks int

	pushMu  sync.RWMutex
	pushCfg map[string]*PushNotificationConfig

	notifier      *PushNotifier
	publisher     TaskUpdatePublisher
	inputRequired func(output string) bool

	execSem    chan struct{}
	cancelMu   sync.Mutex
	cancelFuncs map[string]context.CancelFunc
}

// defaultMaxTasks caps how many task records DefaultAgentHandler retains in
// memory. Once exceeded, the oldest terminal-state tasks (completed, failed,
// or canceled) are evicted first; in-flight tasks are never evicted.
const defaultMaxTasks = 10000

func NewDefaultAgentHandler(card AgentCard, agent *agentcore.Agent, cfg agentcore.Config) *DefaultAgentHandler {
	return &DefaultAgentHandler{
		card:        card,
		agent:       agent,
		config:      cfg,
		taskTimeout: defaultTaskTimeout,
		tasks:       make(map[string]*Task),
		maxTasks:    defaultMaxTasks,
		pushCfg:     make(map[string]*PushNotificationConfig),
		notifier:    NewPushNotifier(),
		execSem:     make(chan struct{}, 10),
		cancelFuncs: make(map[string]context.CancelFunc),
	}
}

// SetMaxTasks overrides how many task records are retained in memory before
// the oldest terminal-state tasks start being evicted. Values <= 0 reset it
// to the default (10000).
func (h *DefaultAgentHandler) SetMaxTasks(n int) {
	h.tasksMu.Lock()
	defer h.tasksMu.Unlock()
	if n <= 0 {
		n = defaultMaxTasks
	}
	h.maxTasks = n
	h.evictOldTasksLocked()
}

// evictOldTasksLocked drops the oldest terminal-state tasks (and their
// associated push-notification config) once len(h.tasks) exceeds
// h.maxTasks. Callers must already hold h.tasksMu for writing. Tasks that
// are not yet in a terminal state are never evicted, so clients polling or
// continuing them never lose access.
func (h *DefaultAgentHandler) evictOldTasksLocked() {
	limit := h.maxTasks
	if limit <= 0 {
		limit = defaultMaxTasks
	}
	for len(h.tasks) > limit {
		oldestID := ""
		var oldestTime time.Time
		for id, t := range h.tasks {
			if !isTerminalState(t.State) {
				continue
			}
			var ts time.Time
			if n := len(t.History); n > 0 {
				ts = t.History[n-1].Timestamp
			}
			if oldestID == "" || ts.Before(oldestTime) {
				oldestID = id
				oldestTime = ts
			}
		}
		if oldestID == "" {
			// Nothing evictable: every remaining task is still in flight.
			return
		}
		delete(h.tasks, oldestID)
		h.pushMu.Lock()
		delete(h.pushCfg, oldestID)
		h.pushMu.Unlock()
	}
}

func (h *DefaultAgentHandler) SetMaxConcurrency(n int) {
	if n <= 0 {
		n = 10
	}
	h.execSem = make(chan struct{}, n)
}

func (h *DefaultAgentHandler) SetTaskTimeout(d time.Duration) {
	h.taskTimeout = d
}

func (h *DefaultAgentHandler) SetUpdatePublisher(p TaskUpdatePublisher) {
	h.publisher = p
}

func (h *DefaultAgentHandler) SetInputRequiredPredicate(fn func(output string) bool) {
	h.inputRequired = fn
}

func (h *DefaultAgentHandler) Card() AgentCard { return h.card }

func (h *DefaultAgentHandler) SendTask(ctx context.Context, req SendTaskRequest) (*Task, error) {
	h.tasksMu.RLock()
	existingTask, exists := h.tasks[req.ID]
	h.tasksMu.RUnlock()

	if exists {
		if isTerminalState(existingTask.State) {
			return nil, fmt.Errorf("task %q is already in terminal state %q", req.ID, existingTask.State)
		}
		if existingTask.State != TaskStateInputRequired && existingTask.State != TaskStateSubmitted {
			return nil, fmt.Errorf("task %q is in state %q, cannot append message", req.ID, existingTask.State)
		}
		return h.continueTask(ctx, existingTask, req)
	}

	return h.newTask(ctx, req)
}

func (h *DefaultAgentHandler) newTask(ctx context.Context, req SendTaskRequest) (*Task, error) {
	input := req.inputOverride
	if input == "" {
		for _, p := range req.Message.Parts {
			if p.Type == PartTypeText {
				input += p.Text
			}
		}
	}

	task := &Task{
		ID:        req.ID,
		SessionID: req.SessionID,
		State:     TaskStateSubmitted,
		Messages:  []Message{req.Message},
		Metadata:  req.Metadata,
		History: []TaskStatus{
			{State: TaskStateSubmitted, Timestamp: time.Now()},
		},
	}

	h.tasksMu.Lock()
	h.tasks[task.ID] = task
	h.tasksMu.Unlock()

	return h.runAgent(ctx, task, input)
}

func (h *DefaultAgentHandler) continueTask(ctx context.Context, task *Task, req SendTaskRequest) (*Task, error) {
	input := req.inputOverride
	if input == "" {
		for _, p := range req.Message.Parts {
			if p.Type == PartTypeText {
				input += p.Text
			}
		}
	}

	h.tasksMu.Lock()
	task.Messages = append(task.Messages, req.Message)
	task.State = TaskStateWorking
	task.History = append(task.History, TaskStatus{
		State:     TaskStateWorking,
		Timestamp: time.Now(),
	})
	h.tasksMu.Unlock()

	h.publish(task.ID, &TaskUpdateEvent{Result: &Task{ID: task.ID, State: TaskStateWorking}, Final: false})

	return h.runAgent(ctx, task, input)
}

func (h *DefaultAgentHandler) runAgent(ctx context.Context, task *Task, input string) (*Task, error) {
	select {
	case h.execSem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-h.execSem }()

	h.tasksMu.Lock()
	if task.State != TaskStateWorking {
		task.State = TaskStateWorking
		task.History = append(task.History, TaskStatus{State: TaskStateWorking, Timestamp: time.Now()})
		h.tasksMu.Unlock()
		h.publish(task.ID, &TaskUpdateEvent{Result: &Task{ID: task.ID, State: TaskStateWorking}, Final: false})
	} else {
		h.tasksMu.Unlock()
	}

	var unsub func()
	if h.publisher != nil {
		unsub = h.subscribeAgentEvents(task.ID)
	}

	runCtx, cancel := context.WithTimeout(ctx, h.taskTimeout)
	defer cancel()

	h.cancelMu.Lock()
	h.cancelFuncs[task.ID] = cancel
	h.cancelMu.Unlock()

	defer func() {
		h.cancelMu.Lock()
		delete(h.cancelFuncs, task.ID)
		h.cancelMu.Unlock()
	}()

	output, err := h.agent.Run(runCtx, input)

	if unsub != nil {
		unsub()
	}

	h.tasksMu.Lock()
	defer h.tasksMu.Unlock()

	if err != nil {
		task.State = TaskStateFailed
		task.History = append(task.History, TaskStatus{
			State:     TaskStateFailed,
			Timestamp: time.Now(),
		})
		h.evictOldTasksLocked()
		h.notify(task)
		return task, nil
	}

	if h.inputRequired != nil && h.inputRequired(output) {
		task.State = TaskStateInputRequired
		task.Messages = append(task.Messages, Message{
			Role:  string(RoleAgent),
			Parts: []Part{NewTextPart(output)},
		})
		task.History = append(task.History, TaskStatus{
			State:     TaskStateInputRequired,
			Timestamp: time.Now(),
		})
		h.notify(task)
		return task, nil
	}

	task.State = TaskStateCompleted
	task.Messages = append(task.Messages, Message{
		Role:  string(RoleAgent),
		Parts: []Part{NewTextPart(output)},
	})
	task.Artifacts = append(task.Artifacts, Artifact{
		Name:  "output",
		Parts: []Part{NewTextPart(output)},
	})
	task.History = append(task.History, TaskStatus{
		State:     TaskStateCompleted,
		Timestamp: time.Now(),
	})

	h.evictOldTasksLocked()
	h.notify(task)
	return task, nil
}

func (h *DefaultAgentHandler) publish(taskID string, ev *TaskUpdateEvent) {
	if h.publisher != nil {
		h.publisher.PublishTaskUpdate(taskID, ev)
	}
}

func (h *DefaultAgentHandler) subscribeAgentEvents(taskID string) (unsub func()) {
	if h.publisher == nil {
		return func() {}
	}

	var active int32 = 1
	handler := func(e agentcore.Event) {
		if atomic.LoadInt32(&active) == 0 {
			return
		}
		switch ev := e.(type) {
		case *agentcore.MessageDeltaEvent:
			h.publish(taskID, &TaskUpdateEvent{
				Result: &Task{
					ID:    taskID,
					State: TaskStateWorking,
				},
				Artifact: &Artifact{
					Parts:     []Part{NewTextPart(ev.Delta)},
					Append:    boolPtr(true),
					LastChunk: boolPtr(false),
				},
				Final: false,
			})
		case *agentcore.ToolCallStartEvent:
			h.publish(taskID, &TaskUpdateEvent{
				Result: &Task{
					ID:    taskID,
					State: TaskStateWorking,
				},
				Final: false,
			})
		}
	}

	// Register the handler and return a cleanup that BOTH flips the active
	// flag (so any in-flight dispatch sees the handler is done) AND unregisters
	// it from the agent's event bus. Without the unregister, a pooled/reused
	// agent would accumulate stale handlers across tasks — they'd keep firing
	// into h.publish for tasks that have already finished.
	unregister := h.agent.OnAll(handler)
	return func() {
		atomic.StoreInt32(&active, 0)
		unregister()
	}
}

func boolPtr(b bool) *bool { return &b }

func (h *DefaultAgentHandler) notify(task *Task) {
	h.pushMu.RLock()
	cfg, ok := h.pushCfg[task.ID]
	h.pushMu.RUnlock()
	if !ok || cfg == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.notifier.Notify(ctx, cfg, task); err != nil {
		slog.Default().Warn("push notification failed", "task_id", task.ID, "error", err)
	}
}

// GetTask implements AgentHandler.
func (h *DefaultAgentHandler) GetTask(ctx context.Context, req GetTaskRequest) (*Task, error) {
	h.tasksMu.RLock()
	defer h.tasksMu.RUnlock()

	task, ok := h.tasks[req.ID]
	if !ok {
		return nil, fmt.Errorf("task %q not found", req.ID)
	}

	t := *task
	if req.HistoryLength > 0 && len(t.History) > req.HistoryLength {
		offset := len(t.History) - req.HistoryLength
		t.History = t.History[offset:]
	}
	return &t, nil
}

// CancelTask implements AgentHandler.
func (h *DefaultAgentHandler) CancelTask(ctx context.Context, req CancelTaskRequest) (*Task, error) {
	h.tasksMu.Lock()
	defer h.tasksMu.Unlock()

	task, ok := h.tasks[req.ID]
	if !ok {
		return nil, fmt.Errorf("task %q not found", req.ID)
	}

	if isTerminalState(task.State) {
		return nil, fmt.Errorf("task %q is already in terminal state %q", req.ID, task.State)
	}

	h.cancelMu.Lock()
	if cf, ok := h.cancelFuncs[req.ID]; ok {
		cf()
		delete(h.cancelFuncs, req.ID)
	}
	h.cancelMu.Unlock()

	task.State = TaskStateCanceled
	task.History = append(task.History, TaskStatus{
		State:     TaskStateCanceled,
		Timestamp: time.Now(),
	})

	h.evictOldTasksLocked()
	h.notify(task)
	return task, nil
}

func (h *DefaultAgentHandler) QueryTasks(ctx context.Context, req QueryTasksRequest) (*QueryTasksResult, error) {
	h.tasksMu.RLock()
	defer h.tasksMu.RUnlock()

	var result []*Task
	for _, task := range h.tasks {
		if req.SessionID != "" && task.SessionID != req.SessionID {
			continue
		}
		if req.State != "" && task.State != req.State {
			continue
		}
		t := *task
		result = append(result, &t)
	}

	slices.SortFunc(result, func(a, b *Task) int {
		var ta, tb time.Time
		if len(a.History) > 0 {
			ta = a.History[0].Timestamp
		}
		if len(b.History) > 0 {
			tb = b.History[0].Timestamp
		}
		return ta.Compare(tb)
	})

	if req.Limit > 0 && len(result) > req.Limit {
		result = result[:req.Limit]
	}

	if result == nil {
		result = []*Task{}
	}

	return &QueryTasksResult{Tasks: result}, nil
}

// SetPushNotification implements AgentHandler.
func (h *DefaultAgentHandler) SetPushNotification(ctx context.Context, req SetPushNotificationRequest) error {
	h.pushMu.Lock()
	defer h.pushMu.Unlock()
	h.pushCfg[req.ID] = &req.Config
	return nil
}

// GetPushNotification implements AgentHandler.
func (h *DefaultAgentHandler) GetPushNotification(ctx context.Context, taskID string) (*PushNotificationConfig, error) {
	h.pushMu.RLock()
	defer h.pushMu.RUnlock()
	cfg, ok := h.pushCfg[taskID]
	if !ok {
		return nil, fmt.Errorf("no push notification config for task %q", taskID)
	}
	return cfg, nil
}

// ---------------------------------------------------------------------------
// PushNotifier
// ---------------------------------------------------------------------------

// PushNotifier sends push notifications to configured webhooks with retry.
type PushNotifier struct {
	client       *http.Client
	maxRetries   int
	backoff      time.Duration
	allowPrivate bool
}

// NewPushNotifier creates a new push notifier.
func NewPushNotifier() *PushNotifier {
	return &PushNotifier{
		client:     &http.Client{Timeout: 30 * time.Second},
		maxRetries: 3,
		backoff:    500 * time.Millisecond,
	}
}

func (n *PushNotifier) WithAllowPrivate(allow bool) *PushNotifier {
	n.allowPrivate = allow
	return n
}

// Notify sends a task update to the configured webhook with exponential backoff retry.
func (n *PushNotifier) Notify(ctx context.Context, cfg *PushNotificationConfig, task *Task) error {
	if !n.allowPrivate {
		if err := validateWebhookURL(cfg.URL); err != nil {
			return fmt.Errorf("invalid webhook URL: %w", err)
		}
	}

	payload := map[string]any{
		"taskId": task.ID,
		"state":  task.State,
		"task":   task,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal push payload: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= n.maxRetries; attempt++ {
		if attempt > 0 {
			backoff := n.backoff * time.Duration(1<<uint(attempt-1))
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.URL, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("create push request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")
		if cfg.Token != "" {
			req.Header.Set("Authorization", "Bearer "+cfg.Token)
		}
		for k, v := range cfg.Headers {
			req.Header.Set(k, v)
		}

		resp, err := n.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("send push notification: %w", err)
			continue
		}

		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()

		if resp.StatusCode >= 400 {
			lastErr = fmt.Errorf("push webhook returned %d: %s", resp.StatusCode, string(respBody))
			if resp.StatusCode >= 500 {
				continue
			}
			return lastErr
		}

		return nil
	}

	return fmt.Errorf("push notification failed after %d retries: %w", n.maxRetries, lastErr)
}

func validateWebhookURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("scheme %q not allowed", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("empty host")
	}
	ip := net.ParseIP(host)
	if ip != nil {
		if isPrivateIP(ip) {
			return fmt.Errorf("private IP addresses not allowed")
		}
		return nil
	}
	addrs, err := net.LookupHost(host)
	if err != nil {
		return fmt.Errorf("lookup %q: %w", host, err)
	}
	for _, addr := range addrs {
		ip := net.ParseIP(addr)
		if ip != nil && isPrivateIP(ip) {
			return fmt.Errorf("host %q resolves to private IP %s", host, addr)
		}
	}
	return nil
}

var privateRanges = func() []*net.IPNet {
	cidrs := []string{
		"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
		"127.0.0.0/8", "169.254.0.0/16",
		"::1/128", "fc00::/7", "fe80::/10",
	}
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, s := range cidrs {
		_, n, _ := net.ParseCIDR(s)
		out = append(out, n)
	}
	return out
}()

func isPrivateIP(ip net.IP) bool {
	for _, network := range privateRanges {
		if network != nil && network.Contains(ip) {
			return true
		}
	}
	return false
}
