package agui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

type Handler struct {
	mu     sync.RWMutex
	config agentcore.Config
}

func NewHandler(cfg agentcore.Config) *Handler {
	return &Handler{config: cfg}
}

func (h *Handler) UpdateConfig(cfg agentcore.Config) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.config = cfg
}

func (h *Handler) snapshotConfig() agentcore.Config {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.config
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.handleRun(w, r)
	case http.MethodGet:
		h.handleCapabilities(w, r)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (h *Handler) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	cfg := h.snapshotConfig()
	caps := CapabilitiesFromConfig(cfg)
	writeJSON(w, http.StatusOK, caps)
}

func (h *Handler) handleRun(w http.ResponseWriter, r *http.Request) {
	var input RunAgentInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
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

	cfg := h.snapshotConfig()
	threadID := input.ThreadID
	if threadID == "" {
		threadID = generateID("thread")
	}
	runID := input.RunID
	if runID == "" {
		runID = generateID("run")
	}

	if threadID != "" && cfg.Store != nil {
		hasState, err := cfg.Store.Has(r.Context(), threadID)
		if err == nil && hasState {
			agent := agentcore.New(cfg)
			if err := agent.LoadState(r.Context(), threadID); err == nil {
				msgs := agent.State().Messages()
				snapshot := MessagesSnapshotEvent{
					BaseEvent: baseEvent(EventMessagesSnapshot, time.Now()),
					Messages:  MessagesFromAgent(msgs),
				}
				writeSSE(w, flusher, string(EventMessagesSnapshot), snapshot)
				agent.Close()
			}
		}
	}

	callCfg := callConfigFromInput(input)
	agent, err := agentcore.LoadAgent(r.Context(), cfg, agentcore.LoadAgentOptions{
		ThreadID:          threadID,
		CallCfg:           callCfg,
		ThreadCfgProvider: threadCfgProviderFromConfig(cfg),
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	defer agent.Close()

	converter := NewConverterWithParent(threadID, runID, input.ParentRunID)

	if input.State != nil {
		writeSSE(w, flusher, string(EventStateSnapshot), converter.StateSnapshot(time.Now(), input.State))
	}

	if len(input.Resume) > 0 {
		for _, r := range input.Resume {
			agent.Steer(agentcore.Message{
				Role:    agentcore.RoleSystem,
				Content: fmt.Sprintf("Interrupt %s resolved with status: %s", r.InterruptID, r.Status),
			})
		}
	}

	// Register the SSE writer as a scoped handler. The agent may be pooled
	// and reused across requests; without unregister, this closure would
	// leak onto the agent and keep writing to this dead ResponseWriter on
	// subsequent requests.
	var mu sync.Mutex
	unregister := agent.OnAll(func(e agentcore.Event) {
		agEvents := converter.Convert(e)
		mu.Lock()
		defer mu.Unlock()
		for _, agEv := range agEvents {
			evtType := extractEventType(agEv)
			writeSSE(w, flusher, evtType, agEv)
		}
	})
	defer unregister()

	var message string
	if len(input.Messages) > 0 {
		last := input.Messages[len(input.Messages)-1]
		if last.Role == MessageRoleUser {
			message = last.Content
		}
	}
	if message == "" {
		writeSSE(w, flusher, string(EventRunError), RunErrorEvent{
			BaseEvent: baseEvent(EventRunError, time.Now()),
			ThreadID:  threadID,
			RunID:     runID,
			Message:   "no user message provided",
		})
		return
	}

	if cfg.Provider == nil {
		writeSSE(w, flusher, string(EventRunError), RunErrorEvent{
			BaseEvent: baseEvent(EventRunError, time.Now()),
			ThreadID:  threadID,
			RunID:     runID,
			Message:   "no provider configured",
		})
		return
	}

	_, runErr := agent.Run(r.Context(), message)
	if threadID != "" && cfg.Store != nil {
		_ = agent.SaveState(r.Context(), threadID)
	}

	if runErr != nil {
		for _, agEv := range converter.closeAll(time.Now()) {
			evtType := extractEventType(agEv)
			writeSSE(w, flusher, evtType, agEv)
		}
		writeSSE(w, flusher, string(EventRunFinished), converter.RunFinished(time.Now()))
	}
}

func callConfigFromInput(input RunAgentInput) *agentcore.CallConfig {
	if input.Tools == nil && input.State == nil {
		return nil
	}
	return &agentcore.CallConfig{}
}

func threadCfgProviderFromConfig(cfg agentcore.Config) agentcore.ThreadConfigProvider {
	type provider interface {
		GetThreadConfig(ctx context.Context, threadID string) (*agentcore.CallConfig, bool, error)
	}
	if cfg.Store == nil {
		return nil
	}
	p, ok := cfg.Store.(provider)
	if !ok {
		return nil
	}
	return threadConfigProvider{p}
}

type threadConfigProvider struct {
	inner interface {
		GetThreadConfig(ctx context.Context, threadID string) (*agentcore.CallConfig, bool, error)
	}
}

func (t threadConfigProvider) GetThreadConfig(ctx context.Context, threadID string) (*agentcore.CallConfig, bool, error) {
	return t.inner.GetThreadConfig(ctx, threadID)
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, eventType string, data any) {
	payload, _ := json.Marshal(data)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, payload)
	flusher.Flush()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func extractEventType(ev any) string {
	type typed interface {
		GetType() EventType
	}
	if t, ok := ev.(typed); ok {
		return string(t.GetType())
	}
	return string(EventCustom)
}

var idCounter atomic.Uint64

func generateID(prefix string) string {
	n := idCounter.Add(1)
	return fmt.Sprintf("%s_%d", prefix, n)
}
