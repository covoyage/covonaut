package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/agui"
	"github.com/covoyage/covonaut/session"
	"github.com/covoyage/covonaut/skill"
)

// Server exposes an Agent as an HTTP/SSE API.
type Server struct {
	mu       sync.RWMutex
	config   agentcore.Config
	eventBus *agentcore.EventBus
	cors     CORSConfig
	srv      *http.Server

	agentPool sync.Map // threadID -> *agentcore.Agent; cached agents for reuse
	poolMu    sync.Mutex
	poolLimit int
}

type CORSConfig struct {
	AllowOrigins     []string
	AllowMethods     []string
	AllowHeaders     []string
	AllowCredentials bool
}

type threadStore interface {
	CreateThread(ctx context.Context) (*session.ThreadSnapshot, error)
	ListThreads(ctx context.Context) ([]session.Info, error)
	GetThread(ctx context.Context, key string) (*session.ThreadSnapshot, error)
	BranchThread(ctx context.Context, key, entryID string) (*session.ThreadSnapshot, error)
	GetThreadConfig(ctx context.Context, key string) (*agentcore.CallConfig, bool, error)
	SetThreadConfig(ctx context.Context, key string, cfg *agentcore.CallConfig) (*session.ThreadSnapshot, error)
	GetThreadThinking(ctx context.Context, key string) (*agentcore.ThinkingConfig, bool, error)
	SetThreadThinking(ctx context.Context, key string, cfg *agentcore.ThinkingConfig) (*session.ThreadSnapshot, error)
}

type BranchThreadRequest struct {
	EntryID string `json:"entry_id,omitempty"`
}

type ThreadThinkingRequest struct {
	Thinking *agentcore.ThinkingConfig `json:"thinking,omitempty"`
}

type ThreadThinkingResponse struct {
	ThreadID string                    `json:"thread_id"`
	Thinking *agentcore.ThinkingConfig `json:"thinking,omitempty"`
}

type ThreadConfigRequest struct {
	Config *agentcore.CallConfig `json:"config,omitempty"`
}

type ThreadConfigResponse struct {
	ThreadID string                `json:"thread_id"`
	Config   *agentcore.CallConfig `json:"config,omitempty"`
}

type SkillSummary struct {
	Name                   string            `json:"name"`
	Description            string            `json:"description"`
	FilePath               string            `json:"file_path"`
	BaseDir                string            `json:"base_dir"`
	DisableModelInvocation bool              `json:"disable_model_invocation,omitempty"`
	Metadata               map[string]string `json:"metadata,omitempty"`
	SelectedByDefault      bool              `json:"selected_by_default,omitempty"`
}

type SkillsResponse struct {
	Skills []SkillSummary `json:"skills"`
}

type SkillDiagnosticsResponse struct {
	Diagnostics []skill.Diagnostic `json:"diagnostics"`
}

type SkillRegistryStatusResponse struct {
	Skills                  []SkillSummary     `json:"skills"`
	ThreadID                string             `json:"thread_id,omitempty"`
	HasThreadConfig         bool               `json:"has_thread_config,omitempty"`
	SelectedSkills          []string           `json:"selected_skills,omitempty"`
	EffectiveSelectedSkills []string           `json:"effective_selected_skills,omitempty"`
	MissingSelectedSkills   []string           `json:"missing_selected_skills,omitempty"`
	AddedSkills             []string           `json:"added_skills,omitempty"`
	RemovedSkills           []string           `json:"removed_skills,omitempty"`
	UpdatedSkills           []string           `json:"updated_skills,omitempty"`
	AddedDiagnostics        []skill.Diagnostic `json:"added_diagnostics,omitempty"`
	RemovedDiagnostics      []skill.Diagnostic `json:"removed_diagnostics,omitempty"`
	SkillPaths              []string           `json:"skill_paths,omitempty"`
	Reloadable              bool               `json:"reloadable"`
	Diagnostics             []skill.Diagnostic `json:"diagnostics"`
	TotalSkills             int                `json:"total_skills"`
	VisibleSkills           int                `json:"visible_skills"`
	HiddenSkills            int                `json:"hidden_skills"`
	DiagnosticsCount        int                `json:"diagnostics_count"`
}

func New(cfg agentcore.Config) *Server {
	return &Server{
		config:    cfg,
		eventBus:  agentcore.NewEventBus(),
		poolLimit: 64,
	}
}

func (s *Server) On(t agentcore.EventType, h agentcore.EventHandler) func() { return s.eventBus.On(t, h) }
func (s *Server) OnAll(h agentcore.EventHandler) func()                     { return s.eventBus.OnAll(h) }
func (s *Server) EmitEvent(e agentcore.Event)                        { s.eventBus.Emit(e) }
func (s *Server) Close() {
	s.eventBus.Close()
	s.agentPool.Range(func(key, value any) bool {
		if agent, ok := value.(*agentcore.Agent); ok {
			agent.Close()
		}
		s.agentPool.Delete(key)
		return true
	})
}

// Handler returns an http.Handler wired with all routes.
// Mount it on your own mux or pass directly to http.ListenAndServe.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/chat", s.handleChat)
	mux.HandleFunc("GET /api/skills", s.handleListSkills)
	mux.HandleFunc("GET /api/skills/diagnostics", s.handleSkillDiagnostics)
	mux.HandleFunc("GET /api/skills/events", s.handleSkillEvents)
	mux.HandleFunc("GET /api/skills/status", s.handleSkillStatus)
	mux.HandleFunc("POST /api/skills/reload", s.handleReloadSkills)
	mux.HandleFunc("POST /api/threads", s.handleCreateThread)
	mux.HandleFunc("GET /api/threads", s.handleListThreads)
	mux.HandleFunc("GET /api/threads/{key}", s.handleGetThread)
	mux.HandleFunc("GET /api/threads/{key}/config", s.handleGetThreadConfig)
	mux.HandleFunc("PUT /api/threads/{key}/config", s.handlePutThreadConfig)
	mux.HandleFunc("GET /api/threads/{key}/thinking", s.handleGetThreadThinking)
	mux.HandleFunc("PUT /api/threads/{key}/thinking", s.handlePutThreadThinking)
	mux.HandleFunc("POST /api/threads/{key}/branch", s.handleBranchThread)
	mux.HandleFunc("DELETE /api/threads/{key}", s.handleDeleteThread)
	mux.HandleFunc("GET /api/states", s.handleListStates)
	mux.HandleFunc("GET /api/states/{key}", s.handleGetState)
	mux.HandleFunc("DELETE /api/states/{key}", s.handleDeleteState)
	mux.Handle("/agui/{path}", s.aguiHandler())
	return withCORS(mux, s.cors)
}

func (s *Server) aguiHandler() http.Handler {
	return agui.NewHandler(s.snapshotConfig())
}

func (s *Server) ListenAndServe(addr string) error {
	s.srv = &http.Server{Addr: addr, Handler: s.Handler()}
	return s.srv.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.Close()
	if s.srv == nil {
		return nil
	}
	return s.srv.Shutdown(ctx)
}

// --- request / response types ---

type ChatRequest struct {
	Message        string                    `json:"message"`
	Stream         bool                      `json:"stream"`
	ThreadID       string                    `json:"thread_id,omitempty"`
	Model          string                    `json:"model,omitempty"`
	ResponseFormat *agentcore.ResponseFormat `json:"response_format,omitempty"`
	Thinking       *agentcore.ThinkingConfig `json:"thinking,omitempty"`
	Skills         []string                  `json:"skills,omitempty"`
}

type ChatResponse struct {
	Output   string `json:"output"`
	ThreadID string `json:"thread_id,omitempty"`
	Error    string `json:"error,omitempty"`
}

// --- chat handler ---

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ChatResponse{Error: "invalid request body"})
		return
	}
	threadID, err := s.ensureThreadID(r.Context(), req.ThreadID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ChatResponse{Error: err.Error()})
		return
	}
	req.ThreadID = threadID
	if req.Message == "" {
		writeJSON(w, http.StatusBadRequest, ChatResponse{ThreadID: req.ThreadID, Error: "message is required"})
		return
	}

	if req.Stream {
		s.handleStreamChat(w, r, req)
	} else {
		s.handleSyncChat(w, r, req)
	}
}

func (s *Server) handleSyncChat(w http.ResponseWriter, r *http.Request, req ChatRequest) {
	agent, err := s.loadAgent(r.Context(), req.ThreadID, requestCallConfig(req))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ChatResponse{
			ThreadID: req.ThreadID,
			Error:    err.Error(),
		})
		return
	}

	output, err := agent.Run(r.Context(), req.Message)
	if saveErr := s.saveAgentState(r.Context(), agent, req.ThreadID); saveErr != nil && err == nil {
		err = saveErr
	}
	s.releaseAgent(agent, req.ThreadID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ChatResponse{
			ThreadID: req.ThreadID,
			Error:    err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, ChatResponse{Output: output, ThreadID: req.ThreadID})
}

func (s *Server) handleStreamChat(w http.ResponseWriter, r *http.Request, req ChatRequest) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, ChatResponse{Error: "streaming not supported"})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	agent, err := s.loadAgent(r.Context(), req.ThreadID, requestCallConfig(req))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ChatResponse{
			ThreadID: req.ThreadID,
			Error:    err.Error(),
		})
		return
	}

	// dead marks the connection as unwritable (client disconnect, marshal
	// failure, underlying write error). Once set, subsequent writeSSE calls
	// short-circuit — no point doing further work on a dead connection, and
	// continuing to write would just spam the log. Guarded by the writeSSE
	// mutex + atomic for lock-free reads from the handler path.
	var dead atomic.Bool
	var mu sync.Mutex
	writeSSE := func(eventType string, data any) {
		if dead.Load() {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if dead.Load() {
			return
		}
		payload, err := json.Marshal(data)
		if err != nil {
			log.Printf("server: SSE marshal error (event=%s): %v", eventType, err)
			dead.Store(true)
			return
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, payload); err != nil {
			log.Printf("server: SSE write error (event=%s): %v", eventType, err)
			dead.Store(true)
			return
		}
		flusher.Flush()
	}

	// Register the SSE writer as a scoped handler on the agent's event bus.
	// The handler is unregistered BEFORE the agent is released back to the
	// pool — not just via defer. If we relied on defer alone, there would be
	// a window between releaseAgent (agent back in pool) and the deferred
	// unregister (runs at function return): a concurrent request on the same
	// thread could reuse the agent and its events would flow into this dead
	// ResponseWriter. Unregistering before release closes that window.
	// The defer is kept as an idempotent safety net.
	unregister := agent.OnAll(func(e agentcore.Event) {
		writeSSE(string(e.EventKind()), streamEventPayload(req.ThreadID, e))
	})
	defer unregister()
	agent.EmitExtensionSnapshots()

	output, runErr := agent.Run(r.Context(), req.Message)
	saveErr := s.saveAgentState(r.Context(), agent, req.ThreadID)
	if saveErr != nil && runErr == nil {
		runErr = saveErr
	}
	unregister()        // detach BEFORE releasing — see comment above
	s.releaseAgent(agent, req.ThreadID)

	done := StreamDoneEvent{
		Schema:   streamSchemaChatDone,
		Type:     "done",
		ThreadID: req.ThreadID,
		Output:   output,
	}
	if runErr != nil {
		done.Error = runErr.Error()
	}
	writeSSE("done", done)
}

// --- state handlers ---

func (s *Server) handleCreateThread(w http.ResponseWriter, r *http.Request) {
	ts, ok := s.threadStore()
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "thread store not configured"})
		return
	}
	thread, err := ts.CreateThread(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, thread)
}

func (s *Server) handleListThreads(w http.ResponseWriter, r *http.Request) {
	ts, ok := s.threadStore()
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "thread store not configured"})
		return
	}
	threads, err := ts.ListThreads(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"threads": threads})
}

func (s *Server) handleListSkills(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeSkillAPI(w, r, false) {
		return
	}
	writeJSON(w, http.StatusOK, SkillsResponse{
		Skills: s.skillSummaries(),
	})
}

func (s *Server) handleSkillDiagnostics(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeSkillAPI(w, r, false) {
		return
	}
	cfg := s.snapshotConfig()
	writeJSON(w, http.StatusOK, SkillDiagnosticsResponse{
		Diagnostics: cloneSkillDiagnostics(cfg.SkillDiagnostics),
	})
}

func (s *Server) handleSkillStatus(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeSkillAPI(w, r, false) {
		return
	}
	cfg := s.snapshotConfig()
	threadID := strings.TrimSpace(r.URL.Query().Get("thread_id"))
	selectedSkills := agentcore.CloneStringSlice(cfg.SelectedSkills)
	effectiveSkills := agentcore.CloneStringSlice(cfg.SelectedSkills)
	hasThreadConfig := false
	if threadID != "" {
		threadCfg, ok, err := s.threadCallConfig(r.Context(), threadID)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		hasThreadConfig = ok
		effectiveSkills = effectiveSkillSelection(cfg.SelectedSkills, threadCfg)
	}
	skills := skillSummariesFor(cfg.AvailableSkills, selectedSkills)
	diagnostics := cloneSkillDiagnostics(cfg.SkillDiagnostics)
	_, missing := skill.ResolveSelection(cfg.AvailableSkills, effectiveSkills)
	var visible, hidden int
	for _, item := range skills {
		if item.DisableModelInvocation {
			hidden++
		} else {
			visible++
		}
	}
	writeJSON(w, http.StatusOK, SkillRegistryStatusResponse{
		Skills:                  skills,
		ThreadID:                threadID,
		HasThreadConfig:         hasThreadConfig,
		SelectedSkills:          selectedSkills,
		EffectiveSelectedSkills: effectiveSkills,
		MissingSelectedSkills:   missing,
		SkillPaths:              agentcore.CloneStringSlice(cfg.SkillPaths),
		Reloadable:              len(cfg.SkillPaths) > 0,
		Diagnostics:             diagnostics,
		TotalSkills:             len(skills),
		VisibleSkills:           visible,
		HiddenSkills:            hidden,
		DiagnosticsCount:        len(diagnostics),
	})
}

func (s *Server) handleSkillEvents(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeSkillAPI(w, r, false) {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming not supported"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	var dead atomic.Bool
	var mu sync.Mutex
	writeSSE := func(eventType string, data any) {
		if dead.Load() {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if dead.Load() {
			return
		}
		payload, err := json.Marshal(data)
		if err != nil {
			log.Printf("server: SSE marshal error (event=%s): %v", eventType, err)
			dead.Store(true)
			return
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, payload); err != nil {
			log.Printf("server: SSE write error (event=%s): %v", eventType, err)
			dead.Store(true)
			return
		}
		flusher.Flush()
	}

	writeSSE("skills_snapshot", skillSnapshotEventPayload(s.snapshotConfig()))

	ch := make(chan agentcore.Event, 8)
	s.On(agentcore.EventSkillsReloaded, func(e agentcore.Event) {
		select {
		case ch <- e:
		default:
		}
	})

	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case e := <-ch:
			writeSSE(string(e.EventKind()), streamEventPayload("", e))
		}
	}
}

func (s *Server) handleReloadSkills(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeSkillAPI(w, r, true) {
		return
	}
	cfg := s.snapshotConfig()
	if len(cfg.SkillPaths) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "skill reload not configured"})
		return
	}
	skills, diagnostics, err := skill.Load(cfg.SkillPaths...)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.mu.Lock()
	oldSkills := cloneSkills(s.config.AvailableSkills)
	oldDiagnostics := cloneSkillDiagnostics(s.config.SkillDiagnostics)
	s.config.AvailableSkills = cloneSkills(skills)
	s.config.SkillDiagnostics = cloneSkillDiagnostics(diagnostics)
	s.mu.Unlock()
	cfg = s.snapshotConfig()
	skillsSummary := skillSummariesFor(cfg.AvailableSkills, cfg.SelectedSkills)
	oldSkillSummaries := skillSummariesFor(oldSkills, cfg.SelectedSkills)
	addedSkills, removedSkills, updatedSkills := diffSkillSummaries(oldSkillSummaries, skillsSummary)
	addedDiagnostics, removedDiagnostics := diffSkillDiagnostics(oldDiagnostics, cfg.SkillDiagnostics)
	var visible, hidden int
	for _, item := range skillsSummary {
		if item.DisableModelInvocation {
			hidden++
		} else {
			visible++
		}
	}
	_, missing := skill.ResolveSelection(cfg.AvailableSkills, cfg.SelectedSkills)
	s.EmitEvent(agentcore.NewSkillsReloadedEvent(
		cfg.SkillPaths,
		len(skillsSummary),
		visible,
		hidden,
		len(cfg.SkillDiagnostics),
		addedSkills,
		removedSkills,
		updatedSkills,
		addedDiagnostics,
		removedDiagnostics,
	))
	writeJSON(w, http.StatusOK, SkillRegistryStatusResponse{
		Skills:                  skillsSummary,
		SelectedSkills:          agentcore.CloneStringSlice(cfg.SelectedSkills),
		EffectiveSelectedSkills: agentcore.CloneStringSlice(cfg.SelectedSkills),
		MissingSelectedSkills:   missing,
		AddedSkills:             addedSkills,
		RemovedSkills:           removedSkills,
		UpdatedSkills:           updatedSkills,
		AddedDiagnostics:        addedDiagnostics,
		RemovedDiagnostics:      removedDiagnostics,
		SkillPaths:              agentcore.CloneStringSlice(cfg.SkillPaths),
		Reloadable:              true,
		Diagnostics:             cloneSkillDiagnostics(cfg.SkillDiagnostics),
		TotalSkills:             len(skillsSummary),
		VisibleSkills:           visible,
		HiddenSkills:            hidden,
		DiagnosticsCount:        len(cfg.SkillDiagnostics),
	})
}

func (s *Server) handleGetThread(w http.ResponseWriter, r *http.Request) {
	ts, ok := s.threadStore()
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "thread store not configured"})
		return
	}
	thread, err := ts.GetThread(r.Context(), r.PathValue("key"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, thread)
}

func (s *Server) handleDeleteThread(w http.ResponseWriter, r *http.Request) {
	store := s.snapshotConfig().Store
	if store == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no store configured"})
		return
	}
	if _, ok := s.threadStore(); !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "thread store not configured"})
		return
	}
	if err := store.Delete(r.Context(), r.PathValue("key")); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGetThreadConfig(w http.ResponseWriter, r *http.Request) {
	ts, ok := s.threadStore()
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "thread store not configured"})
		return
	}
	key := r.PathValue("key")
	cfg, _, err := ts.GetThreadConfig(r.Context(), key)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, ThreadConfigResponse{
		ThreadID: key,
		Config:   agentcore.CloneCallConfig(cfg),
	})
}

func (s *Server) handlePutThreadConfig(w http.ResponseWriter, r *http.Request) {
	ts, ok := s.threadStore()
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "thread store not configured"})
		return
	}
	var req ThreadConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	thread, err := ts.SetThreadConfig(r.Context(), r.PathValue("key"), req.Config)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, ThreadConfigResponse{
		ThreadID: thread.Info.ID,
		Config:   agentcore.CloneCallConfig(thread.Config),
	})
}

func (s *Server) handleGetThreadThinking(w http.ResponseWriter, r *http.Request) {
	ts, ok := s.threadStore()
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "thread store not configured"})
		return
	}
	key := r.PathValue("key")
	thinking, _, err := ts.GetThreadThinking(r.Context(), key)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, ThreadThinkingResponse{
		ThreadID: key,
		Thinking: cloneThinkingConfig(thinking),
	})
}

func (s *Server) handlePutThreadThinking(w http.ResponseWriter, r *http.Request) {
	ts, ok := s.threadStore()
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "thread store not configured"})
		return
	}
	var req ThreadThinkingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	thread, err := ts.SetThreadThinking(r.Context(), r.PathValue("key"), req.Thinking)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, ThreadThinkingResponse{
		ThreadID: thread.Info.ID,
		Thinking: cloneThinkingConfig(thread.Thinking),
	})
}

func (s *Server) handleBranchThread(w http.ResponseWriter, r *http.Request) {
	ts, ok := s.threadStore()
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "thread store not configured"})
		return
	}
	var req BranchThreadRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
	}
	thread, err := ts.BranchThread(r.Context(), r.PathValue("key"), req.EntryID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, thread)
}

func (s *Server) handleListStates(w http.ResponseWriter, r *http.Request) {
	store := s.snapshotConfig().Store
	if store == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no store configured"})
		return
	}
	keys, err := store.List(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
}

func (s *Server) handleGetState(w http.ResponseWriter, r *http.Request) {
	store := s.snapshotConfig().Store
	if store == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no store configured"})
		return
	}
	key := r.PathValue("key")
	snap, err := store.Load(r.Context(), key)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *Server) handleDeleteState(w http.ResponseWriter, r *http.Request) {
	store := s.snapshotConfig().Store
	if store == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no store configured"})
		return
	}
	key := r.PathValue("key")
	if err := store.Delete(r.Context(), key); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (s *Server) loadAgent(ctx context.Context, threadID string, callCfg *agentcore.CallConfig) (*agentcore.Agent, error) {
	if threadID != "" && callCfg == nil {
		if cached, ok := s.agentPool.Load(threadID); ok {
			if agent, ok := cached.(*agentcore.Agent); ok {
				if ts, ok := s.threadStore(); ok {
					if threadCfg, hasCfg, err := ts.GetThreadConfig(ctx, threadID); err == nil && hasCfg {
						agent.ApplyCallConfig(threadCfg)
					}
				}
				if err := agent.LoadState(ctx, threadID); err == nil {
					return agent, nil
				}
				s.agentPool.Delete(threadID)
				agent.Close()
			}
		}
	}

	cfg := s.snapshotConfig()
	var provider agentcore.ThreadConfigProvider
	if ts, ok := s.threadStore(); ok {
		provider = ts
	}
	return agentcore.LoadAgent(ctx, cfg, agentcore.LoadAgentOptions{
		ThreadID:          threadID,
		CallCfg:           callCfg,
		ThreadCfgProvider: provider,
	})
}

func (s *Server) releaseAgent(agent *agentcore.Agent, threadID string) {
	if threadID == "" {
		agent.Close()
		return
	}
	s.poolMu.Lock()
	count := 0
	s.agentPool.Range(func(_, _ any) bool {
		count++
		return count < s.poolLimit
	})
	if count >= s.poolLimit {
		s.agentPool.Range(func(key, value any) bool {
			if a, ok := value.(*agentcore.Agent); ok {
				a.Close()
			}
			s.agentPool.Delete(key)
			return false
		})
	}
	s.poolMu.Unlock()

	if prev, loaded := s.agentPool.LoadOrStore(threadID, agent); loaded {
		prev.(*agentcore.Agent).Close()
		s.agentPool.Store(threadID, agent)
	}
}

func (s *Server) saveAgentState(ctx context.Context, agent *agentcore.Agent, threadID string) error {
	store := s.snapshotConfig().Store
	if threadID == "" || store == nil {
		return nil
	}
	return agent.SaveState(ctx, threadID)
}

func mergeThinkingConfig(base, override *agentcore.ThinkingConfig) *agentcore.ThinkingConfig {
	if override == nil {
		if base == nil {
			return nil
		}
		cp := *base
		return &cp
	}
	cp := *override
	return &cp
}

func requestCallConfig(req ChatRequest) *agentcore.CallConfig {
	if req.Model == "" && req.ResponseFormat == nil && req.Thinking == nil && len(req.Skills) == 0 {
		return nil
	}
	return &agentcore.CallConfig{
		Model:          req.Model,
		ResponseFormat: agentcore.CloneResponseFormat(req.ResponseFormat),
		Thinking:       agentcore.CloneThinkingConfig(req.Thinking),
		Skills:         agentcore.CloneStringSlice(req.Skills),
	}
}

func cloneThinkingConfig(cfg *agentcore.ThinkingConfig) *agentcore.ThinkingConfig {
	if cfg == nil {
		return nil
	}
	cp := *cfg
	return &cp
}

func (s *Server) skillSummaries() []SkillSummary {
	cfg := s.snapshotConfig()
	return skillSummariesFor(cfg.AvailableSkills, cfg.SelectedSkills)
}

func cloneSkillMetadata(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneSkillDiagnostics(in []skill.Diagnostic) []skill.Diagnostic {
	if len(in) == 0 {
		return nil
	}
	out := make([]skill.Diagnostic, len(in))
	copy(out, in)
	return out
}

func cloneSkills(in []skill.Skill) []skill.Skill {
	if len(in) == 0 {
		return nil
	}
	out := make([]skill.Skill, 0, len(in))
	for _, item := range in {
		cp := item
		cp.AllowedTools = agentcore.CloneStringSlice(item.AllowedTools)
		cp.Metadata = cloneSkillMetadata(item.Metadata)
		out = append(out, cp)
	}
	return out
}

func (s *Server) ensureThreadID(ctx context.Context, threadID string) (string, error) {
	if threadID != "" {
		return threadID, nil
	}
	ts, ok := s.threadStore()
	if !ok {
		return "", nil
	}
	thread, err := ts.CreateThread(ctx)
	if err != nil {
		return "", err
	}
	return thread.Info.ID, nil
}

func (s *Server) threadStore() (threadStore, bool) {
	store := s.snapshotConfig().Store
	if store == nil {
		return nil, false
	}
	ts, ok := store.(threadStore)
	return ts, ok
}

func (s *Server) snapshotConfig() agentcore.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cfg := s.config
	cfg.SelectedSkills = agentcore.CloneStringSlice(cfg.SelectedSkills)
	cfg.SkillPaths = agentcore.CloneStringSlice(cfg.SkillPaths)
	cfg.AvailableSkills = cloneSkills(cfg.AvailableSkills)
	cfg.SkillDiagnostics = cloneSkillDiagnostics(cfg.SkillDiagnostics)
	return cfg
}

func (s *Server) authorizeSkillAPI(w http.ResponseWriter, r *http.Request, reload bool) bool {
	cfg := s.snapshotConfig()
	if (!reload && cfg.DisableSkillRegistryAPI) || (reload && cfg.DisableSkillReloadAPI) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "skill API disabled"})
		return false
	}
	token := strings.TrimSpace(cfg.SkillAPIAuthToken)
	if token == "" {
		return true
	}
	if r == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing authorization"})
		return false
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if auth == "Bearer "+token {
		return true
	}
	w.Header().Set("WWW-Authenticate", `Bearer realm="skills"`)
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid authorization"})
	return false
}

func skillSummariesFor(skills []skill.Skill, selected []string) []SkillSummary {
	if len(skills) == 0 {
		return nil
	}
	selectedSet := make(map[string]bool, len(selected))
	for _, name := range selected {
		selectedSet[name] = true
	}
	out := make([]SkillSummary, 0, len(skills))
	for _, item := range skills {
		out = append(out, SkillSummary{
			Name:                   item.Name,
			Description:            item.Description,
			FilePath:               item.FilePath,
			BaseDir:                item.BaseDir,
			DisableModelInvocation: item.DisableModelInvocation,
			Metadata:               cloneSkillMetadata(item.Metadata),
			SelectedByDefault:      selectedSet[item.Name],
		})
	}
	return out
}

func (s *Server) threadCallConfig(ctx context.Context, threadID string) (*agentcore.CallConfig, bool, error) {
	ts, ok := s.threadStore()
	if !ok {
		return nil, false, nil
	}
	cfg, hasCfg, err := ts.GetThreadConfig(ctx, threadID)
	if err != nil {
		return nil, false, err
	}
	return cfg, hasCfg, nil
}

func effectiveSkillSelection(defaultSkills []string, threadCfg *agentcore.CallConfig) []string {
	base := &agentcore.CallConfig{Skills: agentcore.CloneStringSlice(defaultSkills)}
	merged := agentcore.MergeCallConfig(base, threadCfg)
	if merged == nil {
		return nil
	}
	return agentcore.CloneStringSlice(merged.Skills)
}

func skillSnapshotEventPayload(cfg agentcore.Config) SkillsSnapshotStreamEvent {
	skills := skillSummariesFor(cfg.AvailableSkills, cfg.SelectedSkills)
	var visible, hidden int
	for _, item := range skills {
		if item.DisableModelInvocation {
			hidden++
		} else {
			visible++
		}
	}
	return SkillsSnapshotStreamEvent{
		Schema:    streamSchemaSkillsSnapshot,
		Type:      "skills_snapshot",
		Timestamp: time.Now(),
		Payload: SkillRegistryStatusResponse{
			Skills:                  skills,
			SelectedSkills:          agentcore.CloneStringSlice(cfg.SelectedSkills),
			EffectiveSelectedSkills: agentcore.CloneStringSlice(cfg.SelectedSkills),
			SkillPaths:              agentcore.CloneStringSlice(cfg.SkillPaths),
			Reloadable:              len(cfg.SkillPaths) > 0,
			Diagnostics:             cloneSkillDiagnostics(cfg.SkillDiagnostics),
			TotalSkills:             len(skills),
			VisibleSkills:           visible,
			HiddenSkills:            hidden,
			DiagnosticsCount:        len(cfg.SkillDiagnostics),
		},
	}
}

func diffSkillSummaries(oldSkills, newSkills []SkillSummary) (added, removed, updated []string) {
	oldByName := make(map[string]SkillSummary, len(oldSkills))
	newByName := make(map[string]SkillSummary, len(newSkills))
	for _, item := range oldSkills {
		oldByName[item.Name] = item
	}
	for _, item := range newSkills {
		newByName[item.Name] = item
	}
	for name, newItem := range newByName {
		oldItem, ok := oldByName[name]
		if !ok {
			added = append(added, name)
			continue
		}
		if !skillSummaryEqual(oldItem, newItem) {
			updated = append(updated, name)
		}
	}
	for name := range oldByName {
		if _, ok := newByName[name]; !ok {
			removed = append(removed, name)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	sort.Strings(updated)
	return added, removed, updated
}

func diffSkillDiagnostics(oldDiagnostics, newDiagnostics []skill.Diagnostic) (added, removed []skill.Diagnostic) {
	oldByKey := make(map[string]skill.Diagnostic, len(oldDiagnostics))
	newByKey := make(map[string]skill.Diagnostic, len(newDiagnostics))
	for _, item := range oldDiagnostics {
		oldByKey[item.Path+"\x00"+item.Message] = item
	}
	for _, item := range newDiagnostics {
		newByKey[item.Path+"\x00"+item.Message] = item
	}
	for key, item := range newByKey {
		if _, ok := oldByKey[key]; !ok {
			added = append(added, item)
		}
	}
	for key, item := range oldByKey {
		if _, ok := newByKey[key]; !ok {
			removed = append(removed, item)
		}
	}
	sort.Slice(added, func(i, j int) bool {
		if added[i].Path == added[j].Path {
			return added[i].Message < added[j].Message
		}
		return added[i].Path < added[j].Path
	})
	sort.Slice(removed, func(i, j int) bool {
		if removed[i].Path == removed[j].Path {
			return removed[i].Message < removed[j].Message
		}
		return removed[i].Path < removed[j].Path
	})
	return added, removed
}

func skillSummaryEqual(a, b SkillSummary) bool {
	if a.Name != b.Name ||
		a.Description != b.Description ||
		a.FilePath != b.FilePath ||
		a.BaseDir != b.BaseDir ||
		a.DisableModelInvocation != b.DisableModelInvocation {
		return false
	}
	if len(a.Metadata) != len(b.Metadata) {
		return false
	}
	for key, value := range a.Metadata {
		if b.Metadata[key] != value {
			return false
		}
	}
	return true
}

func withCORS(next http.Handler, cfg CORSConfig) http.Handler {
	origins := cfg.AllowOrigins
	if len(origins) == 0 {
		origins = []string{"*"}
	}
	methods := cfg.AllowMethods
	if len(methods) == 0 {
		methods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}
	}
	headers := cfg.AllowHeaders
	if len(headers) == 0 {
		headers = []string{"Content-Type"}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if len(origins) == 1 && origins[0] == "*" && !cfg.AllowCredentials {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		} else if origin != "" {
			for _, allowed := range origins {
				if allowed == "*" || allowed == origin {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					break
				}
			}
		}
		w.Header().Set("Access-Control-Allow-Methods", strings.Join(methods, ", "))
		w.Header().Set("Access-Control-Allow-Headers", strings.Join(headers, ", "))
		if cfg.AllowCredentials {
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// SSEKeepAlive sends periodic comment lines to prevent proxy/browser timeouts.
// Call this in a goroutine and cancel the context when the stream ends.
func SSEKeepAlive(ctx context.Context, w http.ResponseWriter, interval time.Duration) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}
