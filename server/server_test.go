package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/mcp"
	"github.com/covoyage/covonaut/session"
	"github.com/covoyage/covonaut/skill"
)

type memoryStore struct {
	mu    sync.Mutex
	items map[string]agentcore.StateSnapshot
}

func newMemoryStore() *memoryStore {
	return &memoryStore{items: make(map[string]agentcore.StateSnapshot)}
}

func (s *memoryStore) Save(_ context.Context, key string, snap agentcore.StateSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[key] = snap
	return nil
}

func (s *memoryStore) Load(_ context.Context, key string) (agentcore.StateSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, ok := s.items[key]
	if !ok {
		return agentcore.StateSnapshot{}, http.ErrMissingFile
	}
	return snap, nil
}

func (s *memoryStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, key)
	return nil
}

func (s *memoryStore) List(_ context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.items))
	for key := range s.items {
		keys = append(keys, key)
	}
	return keys, nil
}

func (s *memoryStore) Has(_ context.Context, key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.items[key]
	return ok, nil
}

type historyProvider struct{}

func (historyProvider) Complete(ctx context.Context, req *agentcore.ProviderRequest) (*agentcore.ProviderResponse, error) {
	var lastUser string
	var userCount int
	for _, msg := range req.Messages {
		if msg.Role == agentcore.RoleUser {
			userCount++
			lastUser = msg.Content
		}
	}
	return &agentcore.ProviderResponse{
		Content: "users:" + string(rune('0'+userCount)) + " last:" + lastUser,
	}, nil
}

func (historyProvider) Stream(ctx context.Context, req *agentcore.ProviderRequest) (<-chan agentcore.StreamDelta, error) {
	ch := make(chan agentcore.StreamDelta, 1)
	ch <- agentcore.StreamDelta{Done: true}
	close(ch)
	return ch, nil
}

type captureThinkingProvider struct {
	lastModel          string
	lastThinking       *agentcore.ThinkingConfig
	lastResponseFormat *agentcore.ResponseFormat
	lastMessages       []agentcore.Message
}

type failingProvider struct{}

func (failingProvider) Complete(ctx context.Context, req *agentcore.ProviderRequest) (*agentcore.ProviderResponse, error) {
	return nil, errors.New("provider boom")
}

func (failingProvider) Stream(ctx context.Context, req *agentcore.ProviderRequest) (<-chan agentcore.StreamDelta, error) {
	ch := make(chan agentcore.StreamDelta)
	close(ch)
	return ch, nil
}

type blockingProvider struct {
	started chan struct{}
	release chan struct{}
}

func (p *blockingProvider) Complete(ctx context.Context, req *agentcore.ProviderRequest) (*agentcore.ProviderResponse, error) {
	select {
	case <-p.started:
	default:
		close(p.started)
	}
	<-p.release
	return &agentcore.ProviderResponse{Content: "done"}, nil
}

func (p *blockingProvider) Stream(ctx context.Context, req *agentcore.ProviderRequest) (<-chan agentcore.StreamDelta, error) {
	ch := make(chan agentcore.StreamDelta)
	close(ch)
	return ch, nil
}

func (p *captureThinkingProvider) Complete(ctx context.Context, req *agentcore.ProviderRequest) (*agentcore.ProviderResponse, error) {
	p.lastModel = req.Model
	p.lastMessages = append([]agentcore.Message(nil), req.Messages...)
	if req.Thinking != nil {
		cp := *req.Thinking
		p.lastThinking = &cp
	} else {
		p.lastThinking = nil
	}
	p.lastResponseFormat = agentcore.CloneResponseFormat(req.ResponseFormat)
	return &agentcore.ProviderResponse{Content: "ok"}, nil
}

func (p *captureThinkingProvider) Stream(ctx context.Context, req *agentcore.ProviderRequest) (<-chan agentcore.StreamDelta, error) {
	ch := make(chan agentcore.StreamDelta, 1)
	ch <- agentcore.StreamDelta{Done: true}
	close(ch)
	return ch, nil
}

type serverMCPToolProvider struct {
	turn int
}

func (p *serverMCPToolProvider) Complete(ctx context.Context, req *agentcore.ProviderRequest) (*agentcore.ProviderResponse, error) {
	p.turn++
	if p.turn == 1 {
		return &agentcore.ProviderResponse{
			ToolCalls: []agentcore.ToolCall{{
				ID:        "call_1",
				Name:      "mcp.echo",
				Arguments: `{"text":"refresh-tools"}`,
			}},
		}, nil
	}
	return &agentcore.ProviderResponse{Content: "done"}, nil
}

func (p *serverMCPToolProvider) Stream(ctx context.Context, req *agentcore.ProviderRequest) (<-chan agentcore.StreamDelta, error) {
	ch := make(chan agentcore.StreamDelta)
	close(ch)
	return ch, nil
}

func TestServerStreamChatEmitsMCPEvents(t *testing.T) {
	ext := newServerMCPStdioExtension(t)
	defer func() { _ = ext.Dispose() }()

	srv := New(agentcore.Config{
		ModelConfig: agentcore.ModelConfig{
			Model:      "stub",
			Provider:   &serverMCPToolProvider{},
		},
		Extensions: []agentcore.Extension{ext},
	})

	body := postChatStreamRaw(t, srv.Handler(), ChatRequest{
		Message:  "refresh",
		Stream:   true,
		ThreadID: "thread-stream",
	})
	if !strings.Contains(body, "event: mcp_capabilities_updated") {
		t.Fatalf("missing capabilities event in stream: %s", body)
	}
	if !strings.Contains(body, "\"schema\":\"mcp.capabilities_updated.v1\"") {
		t.Fatalf("missing capabilities schema in stream: %s", body)
	}
	if !strings.Contains(body, "\"thread_id\":\"thread-stream\"") {
		t.Fatalf("missing thread id in stream payload: %s", body)
	}
	if !strings.Contains(body, "\"transport\":\"stdio\"") {
		t.Fatalf("missing stdio transport payload in stream: %s", body)
	}
	if !strings.Contains(body, "\"schema\":\"chat.done.v1\"") || !strings.Contains(body, "\"type\":\"done\"") {
		t.Fatalf("missing structured done payload in stream: %s", body)
	}
	if !strings.Contains(body, "event: done") {
		t.Fatalf("missing done event in stream: %s", body)
	}
}

func TestServerStreamChatWrapsAgentEventsInEnvelope(t *testing.T) {
	srv := New(agentcore.Config{
		ModelConfig: agentcore.ModelConfig{
			Model:    "stub",
			Provider: historyProvider{},
		},
	})

	body := postChatStreamRaw(t, srv.Handler(), ChatRequest{
		Message:  "hello",
		Stream:   true,
		ThreadID: "thread-agent-stream",
	})
	if !strings.Contains(body, "event: agent_start") {
		t.Fatalf("missing agent_start event in stream: %s", body)
	}
	if !strings.Contains(body, "\"schema\":\"agent.event.v1\"") {
		t.Fatalf("missing agent event schema in stream: %s", body)
	}
	if !strings.Contains(body, "\"thread_id\":\"thread-agent-stream\"") {
		t.Fatalf("missing thread id in agent event stream: %s", body)
	}
	if !strings.Contains(body, "\"type\":\"agent_start\"") {
		t.Fatalf("missing agent_start envelope type in stream: %s", body)
	}
	if !strings.Contains(body, "\"payload\":{\"input\":\"hello\"}") {
		t.Fatalf("missing normalized agent_start payload in stream: %s", body)
	}
}

func TestServerStreamChatEmitsSkillLoadedEvent(t *testing.T) {
	srv := New(agentcore.Config{
		ModelConfig: agentcore.ModelConfig{
			Model:    "stub",
			Provider: historyProvider{},
		},
		SkillConfig: agentcore.SkillConfig{
			AvailableSkills: []skill.Skill{{
			Name:        "planner",
			Description: "Plans work",
			FilePath:    "/skills/planner/SKILL.md",
			BaseDir:     "/skills/planner",
			Body:        "Plan carefully.",
		}},
		},
	})

	body := postChatStreamRaw(t, srv.Handler(), ChatRequest{
		Message:  "/skill:planner gather requirements",
		Stream:   true,
		ThreadID: "thread-skill-stream",
	})
	if !strings.Contains(body, "event: skill_loaded") {
		t.Fatalf("missing skill_loaded event in stream: %s", body)
	}
	if !strings.Contains(body, "\"schema\":\"agent.event.v1\"") {
		t.Fatalf("missing agent event schema in skill stream: %s", body)
	}
	if !strings.Contains(body, "\"type\":\"skill_loaded\"") {
		t.Fatalf("missing skill_loaded type in stream: %s", body)
	}
	if !strings.Contains(body, "\"payload\":{\"skill_name\":\"planner\",\"path\":\"/skills/planner/SKILL.md\",\"source\":\"explicit_command\",\"arguments\":\"gather requirements\"}") {
		t.Fatalf("missing normalized skill payload in stream: %s", body)
	}
}

func TestServerStreamChatStructuredEventsDecode(t *testing.T) {
	ext := newServerMCPStdioExtension(t)
	defer func() { _ = ext.Dispose() }()

	srv := New(agentcore.Config{
		ModelConfig: agentcore.ModelConfig{
			Model:      "stub",
			Provider:   &serverMCPToolProvider{},
		},
		Extensions: []agentcore.Extension{ext},
	})

	events := parseSSEEvents(t, postChatStreamRaw(t, srv.Handler(), ChatRequest{
		Message:  "refresh",
		Stream:   true,
		ThreadID: "thread-structured",
	}))

	var capabilitiesData json.RawMessage
	var agentStartData json.RawMessage
	var toolCallEndData json.RawMessage
	var doneData json.RawMessage
	for _, ev := range events {
		switch ev.Event {
		case "mcp_capabilities_updated":
			capabilitiesData = ev.Data
		case "agent_start":
			agentStartData = ev.Data
		case "tool_call_end":
			toolCallEndData = ev.Data
		case "done":
			doneData = ev.Data
		}
	}
	if len(capabilitiesData) == 0 || len(agentStartData) == 0 || len(toolCallEndData) == 0 || len(doneData) == 0 {
		t.Fatalf("missing structured events: %#v", events)
	}

	var capabilitiesEvent MCPStreamCapabilitiesEvent
	if err := json.Unmarshal(capabilitiesData, &capabilitiesEvent); err != nil {
		t.Fatalf("decode capabilities event: %v", err)
	}
	if capabilitiesEvent.Schema != streamSchemaMCPAbilitiesUpdated || capabilitiesEvent.ThreadID != "thread-structured" {
		t.Fatalf("capabilities event = %#v", capabilitiesEvent)
	}

	var agentEvent struct {
		Schema   string          `json:"schema"`
		Type     string          `json:"type"`
		ThreadID string          `json:"thread_id"`
		Payload  json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(agentStartData, &agentEvent); err != nil {
		t.Fatalf("decode agent event: %v", err)
	}
	if agentEvent.Schema != streamSchemaAgentEvent || agentEvent.Type != "agent_start" || agentEvent.ThreadID != "thread-structured" {
		t.Fatalf("agent event = %#v", agentEvent)
	}
	var agentStartPayload AgentStartStreamPayload
	if err := json.Unmarshal(agentEvent.Payload, &agentStartPayload); err != nil {
		t.Fatalf("decode agent_start payload: %v", err)
	}
	if agentStartPayload.Input != "refresh" {
		t.Fatalf("agent_start payload = %#v", agentStartPayload)
	}

	var toolCallEvent struct {
		Schema   string          `json:"schema"`
		Type     string          `json:"type"`
		ThreadID string          `json:"thread_id"`
		Payload  json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(toolCallEndData, &toolCallEvent); err != nil {
		t.Fatalf("decode tool_call_end event: %v", err)
	}
	if toolCallEvent.Schema != streamSchemaAgentEvent || toolCallEvent.Type != "tool_call_end" || toolCallEvent.ThreadID != "thread-structured" {
		t.Fatalf("tool_call_end event = %#v", toolCallEvent)
	}
	var toolCallPayload ToolCallEndStreamPayload
	if err := json.Unmarshal(toolCallEvent.Payload, &toolCallPayload); err != nil {
		t.Fatalf("decode tool_call_end payload: %v", err)
	}
	if toolCallPayload.ToolCallID != "call_1" || toolCallPayload.ToolName != "mcp.echo" || toolCallPayload.Result == "" {
		t.Fatalf("tool_call_end payload = %#v", toolCallPayload)
	}

	var doneEvent StreamDoneEvent
	if err := json.Unmarshal(doneData, &doneEvent); err != nil {
		t.Fatalf("decode done event: %v", err)
	}
	if doneEvent.Schema != streamSchemaChatDone || doneEvent.Type != "done" || doneEvent.ThreadID != "thread-structured" || doneEvent.Output != "done" {
		t.Fatalf("done event = %#v", doneEvent)
	}
}

func TestServerStreamChatNormalizesAgentErrorEvent(t *testing.T) {
	srv := New(agentcore.Config{
		ModelConfig: agentcore.ModelConfig{
			Model:    "stub",
			Provider: failingProvider{},
		},
	})

	events := parseSSEEvents(t, postChatStreamRaw(t, srv.Handler(), ChatRequest{
		Message:  "explode",
		Stream:   true,
		ThreadID: "thread-error",
	}))

	var agentErrorData json.RawMessage
	var doneData json.RawMessage
	for _, ev := range events {
		switch ev.Event {
		case "agent_error":
			agentErrorData = ev.Data
		case "done":
			doneData = ev.Data
		}
	}
	if len(agentErrorData) == 0 || len(doneData) == 0 {
		t.Fatalf("missing error events: %#v", events)
	}

	var agentErrorEvent struct {
		Schema   string          `json:"schema"`
		Type     string          `json:"type"`
		ThreadID string          `json:"thread_id"`
		Payload  json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(agentErrorData, &agentErrorEvent); err != nil {
		t.Fatalf("decode agent_error event: %v", err)
	}
	if agentErrorEvent.Schema != streamSchemaAgentEvent || agentErrorEvent.Type != "agent_error" || agentErrorEvent.ThreadID != "thread-error" {
		t.Fatalf("agent_error event = %#v", agentErrorEvent)
	}

	var payload AgentErrorStreamPayload
	if err := json.Unmarshal(agentErrorEvent.Payload, &payload); err != nil {
		t.Fatalf("decode agent_error payload: %v", err)
	}
	if !strings.Contains(payload.Error, "provider boom") {
		t.Fatalf("agent_error payload = %#v", payload)
	}

	var doneEvent StreamDoneEvent
	if err := json.Unmarshal(doneData, &doneEvent); err != nil {
		t.Fatalf("decode done event: %v", err)
	}
	if !strings.Contains(doneEvent.Error, "provider boom") {
		t.Fatalf("done event = %#v", doneEvent)
	}
}

func TestStreamEventPayloadShapesMCPToolsRefreshedEvent(t *testing.T) {
	payload := streamEventPayload("thread-tools", mcp.ToolsRefreshedEvent{
		Extension: "mcp-server-test",
		Transport: "stdio",
		OldTools:  []string{"mcp.echo"},
		NewTools:  []string{"mcp.reverse"},
	})

	ev, ok := payload.(MCPStreamToolsRefreshedEvent)
	if !ok {
		t.Fatalf("payload type = %T", payload)
	}
	if ev.Schema != streamSchemaMCPToolsRefreshed || ev.Type != string(mcp.EventMCPToolsRefreshed) || ev.ThreadID != "thread-tools" {
		t.Fatalf("tools refreshed event = %#v", ev)
	}
	if len(ev.OldTools) != 1 || ev.OldTools[0] != "mcp.echo" || len(ev.NewTools) != 1 || ev.NewTools[0] != "mcp.reverse" {
		t.Fatalf("tools refreshed payload = %#v", ev)
	}
}

func TestStreamEventPayloadShapesMCPRuntimeEvents(t *testing.T) {
	transportPayload := streamEventPayload("thread-runtime", mcp.TransportErrorEvent{
		Extension:   "mcp-http-test",
		Transport:   "http",
		Operation:   "server_stream",
		Message:     "boom",
		Reason:      "server_stream_unsupported",
		Recoverable: false,
	})
	transportEvent, ok := transportPayload.(MCPStreamTransportErrorEvent)
	if !ok {
		t.Fatalf("transport payload type = %T", transportPayload)
	}
	if transportEvent.Schema != streamSchemaMCPTransportError || transportEvent.Type != string(mcp.EventMCPTransportError) || transportEvent.Operation != "server_stream" {
		t.Fatalf("transport event = %#v", transportEvent)
	}

	reconnectPayload := streamEventPayload("thread-runtime", mcp.ReconnectEvent{
		Extension:      "mcp-http-test",
		Transport:      "http",
		Phase:          mcp.ReconnectPhaseSucceeded,
		Reason:         mcp.ReconnectReasonSessionExpired,
		Attempt:        1,
		StaleSessionID: "sess-1",
		SessionID:      "sess-2",
	})
	reconnectEvent, ok := reconnectPayload.(MCPStreamReconnectEvent)
	if !ok {
		t.Fatalf("reconnect payload type = %T", reconnectPayload)
	}
	if reconnectEvent.Schema != streamSchemaMCPReconnect || reconnectEvent.Type != string(mcp.EventMCPReconnect) || reconnectEvent.SessionID != "sess-2" {
		t.Fatalf("reconnect event = %#v", reconnectEvent)
	}

	refreshPayload := streamEventPayload("thread-runtime", mcp.RefreshEvent{
		Extension: "mcp-http-test",
		Transport: "http",
		Phase:     mcp.RefreshPhaseCoalesced,
		Reason:    "in_flight",
		Coalesced: true,
		InFlight:  true,
	})
	refreshEvent, ok := refreshPayload.(MCPStreamRefreshEvent)
	if !ok {
		t.Fatalf("refresh payload type = %T", refreshPayload)
	}
	if refreshEvent.Schema != streamSchemaMCPRefresh || refreshEvent.Type != string(mcp.EventMCPRefresh) || !refreshEvent.Coalesced {
		t.Fatalf("refresh event = %#v", refreshEvent)
	}
}

func TestAgentEventPayloadShapesSkillLoadedEvent(t *testing.T) {
	payload := agentEventPayload(agentcore.SkillLoadedEvent{
		SkillName: "planner",
		Path:      "/skills/planner/SKILL.md",
		Source:    "model_selection",
		Arguments: "scope work",
	})
	skillPayload, ok := payload.(SkillLoadedStreamPayload)
	if !ok {
		t.Fatalf("payload type = %T", payload)
	}
	if skillPayload.SkillName != "planner" || skillPayload.Source != "model_selection" || skillPayload.Arguments != "scope work" {
		t.Fatalf("skill payload = %#v", skillPayload)
	}
}

func TestServerStreamChatEmitsHTTPMCPReconnectEvents(t *testing.T) {
	var mu sync.Mutex
	initCount := 0
	callCount := 0

	mcpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
			return
		case http.MethodPost:
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}

		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		method, _ := req["method"].(string)
		switch method {
		case "initialize":
			mu.Lock()
			initCount++
			current := initCount
			mu.Unlock()
			sessionID := "sess-1"
			if current > 1 {
				sessionID = "sess-2"
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Mcp-Session-Id", sessionID)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]any{
					"protocolVersion": "2025-11-25",
					"capabilities":    map[string]any{"tools": map[string]any{}},
				},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]any{
					"tools": []map[string]any{
						{"name": "echo", "inputSchema": map[string]any{"type": "object"}},
					},
				},
			})
		case "tools/call":
			sessionID := r.Header.Get("Mcp-Session-Id")
			mu.Lock()
			callCount++
			currentCall := callCount
			mu.Unlock()
			if sessionID == "sess-1" && currentCall == 1 {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]any{
					"content": []map[string]any{
						{"type": "text", "text": "tool list updated"},
					},
				},
			})
		default:
			t.Fatalf("unexpected method %q", method)
		}
	}))
	defer mcpServer.Close()

	ext, err := mcp.NewHTTPExtension(context.Background(), mcp.HTTPConfig{
		Name:       "mcp-http-server-test",
		Endpoint:   mcpServer.URL,
		ToolPrefix: "mcp.",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ext.Dispose() }()

	srv := New(agentcore.Config{
		ModelConfig: agentcore.ModelConfig{
			Model:      "stub",
			Provider:   &serverMCPToolProvider{},
		},
		Extensions: []agentcore.Extension{ext},
	})

	events := parseSSEEvents(t, postChatStreamRaw(t, srv.Handler(), ChatRequest{
		Message:  "refresh",
		Stream:   true,
		ThreadID: "thread-http-reconnect",
	}))

	var reconnectData json.RawMessage
	for _, ev := range events {
		if ev.Event == "mcp_reconnect" {
			reconnectData = ev.Data
			break
		}
	}
	if len(reconnectData) == 0 {
		t.Fatalf("missing reconnect event: %#v", events)
	}

	var reconnectEvent MCPStreamReconnectEvent
	if err := json.Unmarshal(reconnectData, &reconnectEvent); err != nil {
		t.Fatalf("decode reconnect event: %v", err)
	}
	if reconnectEvent.Schema != streamSchemaMCPReconnect || reconnectEvent.ThreadID != "thread-http-reconnect" || reconnectEvent.Transport != "http" {
		t.Fatalf("reconnect event = %#v", reconnectEvent)
	}
	if reconnectEvent.Reason != mcp.ReconnectReasonSessionExpired && reconnectEvent.Reason != mcp.ReconnectReasonServerStream404 {
		t.Fatalf("unexpected reconnect reason = %#v", reconnectEvent)
	}
}

func TestServerStreamChatEmitsHTTPMCPTransportErrorEvents(t *testing.T) {
	var mu sync.Mutex
	getCount := 0
	firstGETStarted := make(chan struct{})
	releaseFirstGET := make(chan struct{})

	mcpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
			return
		case http.MethodPost:
		case http.MethodGet:
			mu.Lock()
			getCount++
			currentGET := getCount
			mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			switch currentGET {
			case 1:
				close(firstGETStarted)
				<-releaseFirstGET
				_, _ = w.Write([]byte("id: first\n"))
				_, _ = w.Write([]byte("retry: 1\n"))
				_, _ = w.Write([]byte("data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/ping\"}\n\n"))
			case 2:
				_, _ = w.Write([]byte("data: {not-json}\n\n"))
			default:
				<-r.Context().Done()
			}
			return
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}

		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		method, _ := req["method"].(string)
		switch method {
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Mcp-Session-Id", "sess-transport")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]any{
					"protocolVersion": "2025-11-25",
					"capabilities":    map[string]any{"tools": map[string]any{}},
				},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]any{
					"tools": []map[string]any{
						{"name": "echo", "inputSchema": map[string]any{"type": "object"}},
					},
				},
			})
		default:
			t.Fatalf("unexpected method %q", method)
		}
	}))
	defer mcpServer.Close()

	ext, err := mcp.NewHTTPExtension(context.Background(), mcp.HTTPConfig{
		Name:               "mcp-http-transport-test",
		Endpoint:           mcpServer.URL,
		EnableServerStream: true,
		NotificationHandler: func(ctx context.Context, method string, params json.RawMessage) error {
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ext.Dispose() }()

	provider := &blockingProvider{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	srv := New(agentcore.Config{
		ModelConfig: agentcore.ModelConfig{
			Model:      "stub",
			Provider:   provider,
		},
		Extensions: []agentcore.Extension{ext},
	})

	bodyCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		reqBody, err := json.Marshal(ChatRequest{
			Message:  "hello",
			Stream:   true,
			ThreadID: "thread-http-transport",
		})
		if err != nil {
			errCh <- err
			return
		}
		req := httptest.NewRequest(http.MethodPost, "/api/chat", bytes.NewReader(reqBody))
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			errCh <- fmt.Errorf("status = %d body = %s", rec.Code, rec.Body.String())
			return
		}
		bodyCh <- rec.Body.String()
	}()

	select {
	case <-firstGETStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first server-stream GET")
	}
	select {
	case <-provider.started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for provider to start")
	}

	close(releaseFirstGET)
	time.Sleep(150 * time.Millisecond)
	close(provider.release)

	select {
	case err := <-errCh:
		t.Fatal(err)
	case body := <-bodyCh:
		events := parseSSEEvents(t, body)

		var transportData json.RawMessage
		for _, ev := range events {
			if ev.Event == "mcp_transport_error" {
				transportData = ev.Data
				break
			}
		}
		if len(transportData) == 0 {
			t.Fatalf("missing transport error event: %#v", events)
		}

		var transportEvent MCPStreamTransportErrorEvent
		if err := json.Unmarshal(transportData, &transportEvent); err != nil {
			t.Fatalf("decode transport error event: %v", err)
		}
		if transportEvent.Schema != streamSchemaMCPTransportError || transportEvent.ThreadID != "thread-http-transport" || transportEvent.Transport != "http" {
			t.Fatalf("transport error event = %#v", transportEvent)
		}
		if transportEvent.Operation != "server_stream" || transportEvent.Reason != mcp.ReconnectReasonServerStreamEOF {
			t.Fatalf("unexpected transport error payload = %#v", transportEvent)
		}
		if !strings.Contains(transportEvent.Message, "invalid server sse payload") {
			t.Fatalf("transport error message = %#v", transportEvent)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for chat stream body")
	}
}

type unknownStreamEvent struct{}

func (unknownStreamEvent) EventKind() agentcore.EventType { return "unknown_event" }
func (unknownStreamEvent) EventTime() time.Time           { return time.Unix(123, 0).UTC() }

func TestStreamEventPayloadFallsBackForUnknownEvent(t *testing.T) {
	payload := streamEventPayload("thread-unknown", unknownStreamEvent{})

	ev, ok := payload.(AgentStreamEvent)
	if !ok {
		t.Fatalf("payload type = %T", payload)
	}
	if ev.Schema != streamSchemaAgentEvent || ev.Type != "unknown_event" || ev.ThreadID != "thread-unknown" {
		t.Fatalf("unknown event envelope = %#v", ev)
	}
	if _, ok := ev.Payload.(unknownStreamEvent); !ok {
		t.Fatalf("unknown payload type = %T", ev.Payload)
	}
}

func TestServerChatThreadPersistsConversationState(t *testing.T) {
	store := newMemoryStore()
	srv := New(agentcore.Config{
		ModelConfig: agentcore.ModelConfig{
			Model:    "stub",
			Provider: historyProvider{},
		},
		Store:    store,
	})

	first := postChat(t, srv.Handler(), ChatRequest{Message: "hello", ThreadID: "thread-1"})
	if first.Output != "users:1 last:hello" {
		t.Fatalf("first output = %q", first.Output)
	}
	if first.ThreadID != "thread-1" {
		t.Fatalf("first thread_id = %q", first.ThreadID)
	}

	second := postChat(t, srv.Handler(), ChatRequest{Message: "again", ThreadID: "thread-1"})
	if second.Output != "users:2 last:again" {
		t.Fatalf("second output = %q", second.Output)
	}
}

func TestServerChatWithoutThreadRemainsStateless(t *testing.T) {
	store := newMemoryStore()
	srv := New(agentcore.Config{
		ModelConfig: agentcore.ModelConfig{
			Model:    "stub",
			Provider: historyProvider{},
		},
		Store:    store,
	})

	first := postChat(t, srv.Handler(), ChatRequest{Message: "hello"})
	second := postChat(t, srv.Handler(), ChatRequest{Message: "again"})

	if first.Output != "users:1 last:hello" {
		t.Fatalf("first output = %q", first.Output)
	}
	if second.Output != "users:1 last:again" {
		t.Fatalf("second output = %q", second.Output)
	}
}

func TestServerChatThreadOverridesCheckpointThreadID(t *testing.T) {
	store := newMemoryStore()
	checkpoints := agentcore.NewMemoryCheckpointSaver()
	srv := New(agentcore.Config{
		ModelConfig: agentcore.ModelConfig{
			Model:    "stub",
			Provider: historyProvider{},
		},
		Store:    store,
		Checkpoint: &agentcore.CheckpointSettings{
			Saver:    checkpoints,
			ThreadID: "default-thread",
		},
	})

	resp := postChat(t, srv.Handler(), ChatRequest{Message: "hello", ThreadID: "thread-override"})
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if len(checkpoints.All("thread-override")) == 0 {
		t.Fatal("expected checkpoints for request thread")
	}
	if len(checkpoints.All("default-thread")) != 0 {
		t.Fatal("did not expect checkpoints for default thread")
	}
}

func TestServerChatUsesDefaultThinkingConfig(t *testing.T) {
	provider := &captureThinkingProvider{}
	srv := New(agentcore.Config{
		ModelConfig: agentcore.ModelConfig{
			Model:    "stub",
			Provider: provider,
			Thinking: &agentcore.ThinkingConfig{
			Display: agentcore.ThinkingDisplaySummarized,
			Effort:  agentcore.ThinkingEffortMedium,
			Budget:  1024,
		},
		},
	})

	resp := postChat(t, srv.Handler(), ChatRequest{Message: "hello"})
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if provider.lastThinking == nil {
		t.Fatal("expected default thinking config to reach provider")
	}
	if provider.lastThinking.Display != agentcore.ThinkingDisplaySummarized {
		t.Fatalf("display = %q", provider.lastThinking.Display)
	}
	if provider.lastThinking.Effort != agentcore.ThinkingEffortMedium {
		t.Fatalf("effort = %q", provider.lastThinking.Effort)
	}
	if provider.lastThinking.Budget != 1024 {
		t.Fatalf("budget = %d", provider.lastThinking.Budget)
	}
}

func TestServerChatRequestThinkingOverridesDefault(t *testing.T) {
	provider := &captureThinkingProvider{}
	srv := New(agentcore.Config{
		ModelConfig: agentcore.ModelConfig{
			Model:    "stub",
			Provider: provider,
			Thinking: &agentcore.ThinkingConfig{
			Display: agentcore.ThinkingDisplaySummarized,
			Effort:  agentcore.ThinkingEffortHigh,
			Budget:  4096,
		},
		},
	})

	resp := postChat(t, srv.Handler(), ChatRequest{
		Message: "hello",
		Thinking: &agentcore.ThinkingConfig{
			Display: agentcore.ThinkingDisplayOmitted,
			Budget:  -1,
		},
	})
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if provider.lastThinking == nil {
		t.Fatal("expected request thinking config to reach provider")
	}
	if provider.lastThinking.Display != agentcore.ThinkingDisplayOmitted {
		t.Fatalf("display = %q", provider.lastThinking.Display)
	}
	if provider.lastThinking.Effort != agentcore.ThinkingEffortDefault {
		t.Fatalf("effort = %q", provider.lastThinking.Effort)
	}
	if provider.lastThinking.Budget != -1 {
		t.Fatalf("budget = %d", provider.lastThinking.Budget)
	}
}

func TestServerThreadThinkingEndpointsAndChatInheritance(t *testing.T) {
	sessionFS, err := session.NewFileStore(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	threadStore := session.NewAgentStore(sessionFS, "/project")
	provider := &captureThinkingProvider{}
	srv := New(agentcore.Config{
		ModelConfig: agentcore.ModelConfig{
			Model:    "stub",
			Provider: provider,
			Thinking: &agentcore.ThinkingConfig{
			Display: agentcore.ThinkingDisplayOmitted,
			Effort:  agentcore.ThinkingEffortLow,
		},
		},
		Store:    threadStore,
	})

	thread := postChat(t, srv.Handler(), ChatRequest{Message: "hello"})
	if thread.ThreadID == "" {
		t.Fatal("expected thread id")
	}

	var putResp ThreadThinkingResponse
	putJSON(t, srv.Handler(), "/api/threads/"+thread.ThreadID+"/thinking", ThreadThinkingRequest{
		Thinking: &agentcore.ThinkingConfig{
			Display: agentcore.ThinkingDisplaySummarized,
			Effort:  agentcore.ThinkingEffortHigh,
			Budget:  2048,
		},
	}, &putResp, http.StatusOK)
	if putResp.Thinking == nil || putResp.Thinking.Display != agentcore.ThinkingDisplaySummarized {
		t.Fatalf("put response = %#v", putResp)
	}

	var getResp ThreadThinkingResponse
	getJSON(t, srv.Handler(), http.MethodGet, "/api/threads/"+thread.ThreadID+"/thinking", &getResp, http.StatusOK)
	if getResp.Thinking == nil || getResp.Thinking.Effort != agentcore.ThinkingEffortHigh {
		t.Fatalf("get response = %#v", getResp)
	}

	resp := postChat(t, srv.Handler(), ChatRequest{Message: "again", ThreadID: thread.ThreadID})
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if provider.lastThinking == nil {
		t.Fatal("expected thread thinking config to reach provider")
	}
	if provider.lastThinking.Display != agentcore.ThinkingDisplaySummarized {
		t.Fatalf("display = %q", provider.lastThinking.Display)
	}
	if provider.lastThinking.Effort != agentcore.ThinkingEffortHigh {
		t.Fatalf("effort = %q", provider.lastThinking.Effort)
	}
	if provider.lastThinking.Budget != 2048 {
		t.Fatalf("budget = %d", provider.lastThinking.Budget)
	}

	var threadResp session.ThreadSnapshot
	getJSON(t, srv.Handler(), http.MethodGet, "/api/threads/"+thread.ThreadID, &threadResp, http.StatusOK)
	if threadResp.Thinking == nil || threadResp.Thinking.Display != agentcore.ThinkingDisplaySummarized {
		t.Fatalf("thread thinking = %#v", threadResp.Thinking)
	}
}

func TestServerThreadConfigEndpointsAndRequestOverride(t *testing.T) {
	sessionFS, err := session.NewFileStore(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	threadStore := session.NewAgentStore(sessionFS, "/project")
	provider := &captureThinkingProvider{}
	srv := New(agentcore.Config{
		ModelConfig: agentcore.ModelConfig{
			Model:    "server-model",
			Provider: provider,
		},
		SkillConfig: agentcore.SkillConfig{
			AvailableSkills: []skill.Skill{
			{
				Name:        "thread-skill",
				Description: "Thread selected skill",
				FilePath:    "/skills/thread/SKILL.md",
				BaseDir:     "/skills/thread",
				Body:        "Thread skill body",
			},
			{
				Name:        "request-skill",
				Description: "Request selected skill",
				FilePath:    "/skills/request/SKILL.md",
				BaseDir:     "/skills/request",
				Body:        "Request skill body",
			},
		},
		},
		Store:    threadStore,
	})

	thread := postChat(t, srv.Handler(), ChatRequest{Message: "hello"})
	if thread.ThreadID == "" {
		t.Fatal("expected thread id")
	}

	var putResp ThreadConfigResponse
	putJSON(t, srv.Handler(), "/api/threads/"+thread.ThreadID+"/config", ThreadConfigRequest{
		Config: &agentcore.CallConfig{
			Model:          "thread-model",
			Skills:         []string{"thread-skill"},
			ResponseFormat: agentcore.NewJSONObjectResponseFormat(),
			Thinking: &agentcore.ThinkingConfig{
				Display: agentcore.ThinkingDisplaySummarized,
			},
		},
	}, &putResp, http.StatusOK)
	if putResp.Config == nil || putResp.Config.Model != "thread-model" {
		t.Fatalf("put response = %#v", putResp)
	}

	var getResp ThreadConfigResponse
	getJSON(t, srv.Handler(), http.MethodGet, "/api/threads/"+thread.ThreadID+"/config", &getResp, http.StatusOK)
	if getResp.Config == nil || getResp.Config.ResponseFormat == nil {
		t.Fatalf("get response = %#v", getResp)
	}
	if len(getResp.Config.Skills) != 1 || getResp.Config.Skills[0] != "thread-skill" {
		t.Fatalf("thread skills = %#v", getResp.Config.Skills)
	}

	resp := postChat(t, srv.Handler(), ChatRequest{Message: "again", ThreadID: thread.ThreadID})
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if provider.lastModel != "thread-model" {
		t.Fatalf("model = %q", provider.lastModel)
	}
	if provider.lastResponseFormat == nil || provider.lastResponseFormat.Type != agentcore.ResponseFormatJSONObject {
		t.Fatalf("response format = %#v", provider.lastResponseFormat)
	}
	if provider.lastThinking == nil || provider.lastThinking.Display != agentcore.ThinkingDisplaySummarized {
		t.Fatalf("thinking = %#v", provider.lastThinking)
	}
	if !messagesContain(t, provider.lastMessages, "Thread skill body") {
		t.Fatalf("expected thread skill prompt in messages: %#v", provider.lastMessages)
	}

	resp = postChat(t, srv.Handler(), ChatRequest{
		Message:        "override",
		ThreadID:       thread.ThreadID,
		Model:          "request-model",
		Skills:         []string{"request-skill"},
		ResponseFormat: agentcore.NewJSONSchemaResponseFormat("answer", map[string]any{"type": "object"}),
	})
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if provider.lastModel != "request-model" {
		t.Fatalf("override model = %q", provider.lastModel)
	}
	if provider.lastResponseFormat == nil || provider.lastResponseFormat.Type != agentcore.ResponseFormatJSONSchema {
		t.Fatalf("override response format = %#v", provider.lastResponseFormat)
	}
	if provider.lastThinking == nil || provider.lastThinking.Display != agentcore.ThinkingDisplaySummarized {
		t.Fatalf("override thinking = %#v", provider.lastThinking)
	}
	if !messagesContain(t, provider.lastMessages, "Request skill body") {
		t.Fatalf("expected request skill prompt in messages: %#v", provider.lastMessages)
	}
	if messagesContain(t, provider.lastMessages, "Thread skill body") {
		t.Fatalf("request skill override should replace thread selection: %#v", provider.lastMessages)
	}
}

func TestServerSkillRegistryEndpoints(t *testing.T) {
	srv := New(agentcore.Config{
		ModelConfig: agentcore.ModelConfig{
			Model:    "stub",
			Provider: historyProvider{},
		},
		SkillConfig: agentcore.SkillConfig{
			AvailableSkills: []skill.Skill{
			{
				Name:        "planner",
				Description: "Plans work",
				FilePath:    "/skills/planner/SKILL.md",
				BaseDir:     "/skills/planner",
				Body:        "secret body should not be exposed",
				Metadata: map[string]string{
					"category": "planning",
				},
			},
			{
				Name:                   "debugger",
				Description:            "Debugs failures",
				FilePath:               "/skills/debugger/SKILL.md",
				BaseDir:                "/skills/debugger",
				DisableModelInvocation: true,
			},
		},
			SelectedSkills: []string{"planner"},
			SkillDiagnostics: []skill.Diagnostic{
			{Path: "/skills/debugger/SKILL.md", Message: "name does not match parent directory"},
			{Path: "/skills/planner/SKILL.md", Message: "description exceeds 1024 characters (1100)"},
		},
		},
	})

	var skillsResp SkillsResponse
	getJSON(t, srv.Handler(), http.MethodGet, "/api/skills", &skillsResp, http.StatusOK)
	if len(skillsResp.Skills) != 2 {
		t.Fatalf("skills = %#v", skillsResp)
	}
	if skillsResp.Skills[0].Name != "planner" || !skillsResp.Skills[0].SelectedByDefault {
		t.Fatalf("planner summary = %#v", skillsResp.Skills[0])
	}
	if skillsResp.Skills[0].Metadata["category"] != "planning" {
		t.Fatalf("planner metadata = %#v", skillsResp.Skills[0].Metadata)
	}
	rawSkillsBody := getRaw(t, srv.Handler(), http.MethodGet, "/api/skills", http.StatusOK)
	if strings.Contains(rawSkillsBody, "secret body should not be exposed") {
		t.Fatalf("skills endpoint leaked body: %s", rawSkillsBody)
	}

	var diagnosticsResp SkillDiagnosticsResponse
	getJSON(t, srv.Handler(), http.MethodGet, "/api/skills/diagnostics", &diagnosticsResp, http.StatusOK)
	if len(diagnosticsResp.Diagnostics) != 2 {
		t.Fatalf("diagnostics = %#v", diagnosticsResp)
	}

	var statusResp SkillRegistryStatusResponse
	getJSON(t, srv.Handler(), http.MethodGet, "/api/skills/status", &statusResp, http.StatusOK)
	if statusResp.TotalSkills != 2 || statusResp.VisibleSkills != 1 || statusResp.HiddenSkills != 1 {
		t.Fatalf("status counts = %#v", statusResp)
	}
	if statusResp.DiagnosticsCount != 2 {
		t.Fatalf("diagnostics count = %#v", statusResp)
	}
	if len(statusResp.SelectedSkills) != 1 || statusResp.SelectedSkills[0] != "planner" {
		t.Fatalf("selected skills = %#v", statusResp.SelectedSkills)
	}
	if statusResp.Reloadable {
		t.Fatalf("reloadable = %#v", statusResp)
	}
}

func TestServerSkillStatusReflectsThreadOverride(t *testing.T) {
	sessionFS, err := session.NewFileStore(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	threadStore := session.NewAgentStore(sessionFS, "/project")
	srv := New(agentcore.Config{
		ModelConfig: agentcore.ModelConfig{
			Model:    "stub",
			Provider: historyProvider{},
		},
		SkillConfig: agentcore.SkillConfig{
			AvailableSkills: []skill.Skill{
			{Name: "planner", Description: "Plans work", FilePath: "/skills/planner/SKILL.md", BaseDir: "/skills/planner"},
			{Name: "debugger", Description: "Debugs failures", FilePath: "/skills/debugger/SKILL.md", BaseDir: "/skills/debugger"},
		},
			SelectedSkills: []string{"planner"},
		},
		Store:    threadStore,
	})

	thread := postChat(t, srv.Handler(), ChatRequest{Message: "hello"})
	var putResp ThreadConfigResponse
	putJSON(t, srv.Handler(), "/api/threads/"+thread.ThreadID+"/config", ThreadConfigRequest{
		Config: &agentcore.CallConfig{
			Skills: []string{"debugger"},
		},
	}, &putResp, http.StatusOK)

	var statusResp SkillRegistryStatusResponse
	getJSON(t, srv.Handler(), http.MethodGet, "/api/skills/status?thread_id="+thread.ThreadID, &statusResp, http.StatusOK)
	if !statusResp.HasThreadConfig || statusResp.ThreadID != thread.ThreadID {
		t.Fatalf("thread status = %#v", statusResp)
	}
	if len(statusResp.SelectedSkills) != 1 || statusResp.SelectedSkills[0] != "planner" {
		t.Fatalf("default selected = %#v", statusResp.SelectedSkills)
	}
	if len(statusResp.EffectiveSelectedSkills) != 1 || statusResp.EffectiveSelectedSkills[0] != "debugger" {
		t.Fatalf("effective selected = %#v", statusResp.EffectiveSelectedSkills)
	}
	if len(statusResp.MissingSelectedSkills) != 0 {
		t.Fatalf("missing selected = %#v", statusResp.MissingSelectedSkills)
	}
}

func TestServerSkillReloadEndpoint(t *testing.T) {
	root := t.TempDir()
	mustWriteSkillFixture(t, filepath.Join(root, "planner", "SKILL.md"), `---
name: planner
description: Plans work
---
Planner body`)
	initialSkills, initialDiagnostics, err := skill.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(agentcore.Config{
		ModelConfig: agentcore.ModelConfig{
			Model:            "stub",
			Provider:         historyProvider{},
		},
		SkillConfig: agentcore.SkillConfig{
			AvailableSkills:  initialSkills,
			SkillDiagnostics: initialDiagnostics,
			SelectedSkills:   []string{"planner"},
			SkillPaths:       []string{root},
		},
	})

	mustWriteSkillFixture(t, filepath.Join(root, "debugger", "SKILL.md"), `---
name: debugger
description: Debugs failures
disable-model-invocation: true
---
Debugger body`)
	mustWriteSkillFixture(t, filepath.Join(root, "broken", "SKILL.md"), `---
name: broken
---
Missing description`)

	var reloadResp SkillRegistryStatusResponse
	postJSON(t, srv.Handler(), "/api/skills/reload", nil, &reloadResp, http.StatusOK)
	if !reloadResp.Reloadable {
		t.Fatalf("reload response = %#v", reloadResp)
	}
	if reloadResp.TotalSkills != 2 || reloadResp.VisibleSkills != 1 || reloadResp.HiddenSkills != 1 {
		t.Fatalf("reload counts = %#v", reloadResp)
	}
	if reloadResp.DiagnosticsCount != 1 {
		t.Fatalf("reload diagnostics = %#v", reloadResp)
	}
	if len(reloadResp.AddedSkills) != 1 || reloadResp.AddedSkills[0] != "debugger" {
		t.Fatalf("reload added skills = %#v", reloadResp)
	}
	if len(reloadResp.RemovedSkills) != 0 || len(reloadResp.UpdatedSkills) != 0 {
		t.Fatalf("reload diff = %#v", reloadResp)
	}
	if len(reloadResp.AddedDiagnostics) != 1 || len(reloadResp.RemovedDiagnostics) != 0 {
		t.Fatalf("reload diagnostics diff = %#v", reloadResp)
	}
	if !strings.Contains(reloadResp.AddedDiagnostics[0].Path, "broken") || !strings.Contains(reloadResp.AddedDiagnostics[0].Message, "description") {
		t.Fatalf("reload added diagnostics = %#v", reloadResp.AddedDiagnostics)
	}
	if len(reloadResp.SkillPaths) != 1 || reloadResp.SkillPaths[0] != root {
		t.Fatalf("reload skill paths = %#v", reloadResp.SkillPaths)
	}

	var statusResp SkillRegistryStatusResponse
	getJSON(t, srv.Handler(), http.MethodGet, "/api/skills/status", &statusResp, http.StatusOK)
	if statusResp.TotalSkills != 2 || statusResp.DiagnosticsCount != 1 {
		t.Fatalf("status after reload = %#v", statusResp)
	}
}

func TestServerSkillReloadReportsDiffs(t *testing.T) {
	root := t.TempDir()
	mustWriteSkillFixture(t, filepath.Join(root, "planner", "SKILL.md"), `---
name: planner
description: Plans work
---
Planner body`)
	mustWriteSkillFixture(t, filepath.Join(root, "debugger", "SKILL.md"), `---
name: debugger
description: Debugs failures
---
Debugger body`)
	initialSkills, initialDiagnostics, err := skill.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(agentcore.Config{
		ModelConfig: agentcore.ModelConfig{
			Model:            "stub",
			Provider:         historyProvider{},
		},
		SkillConfig: agentcore.SkillConfig{
			AvailableSkills:  initialSkills,
			SkillDiagnostics: initialDiagnostics,
			SkillPaths:       []string{root},
		},
	})
	defer srv.Close()

	mustWriteSkillFixture(t, filepath.Join(root, "planner", "SKILL.md"), `---
name: planner
description: Plans work with checklists
---
Updated planner body`)
	if err := os.RemoveAll(filepath.Join(root, "debugger")); err != nil {
		t.Fatal(err)
	}
	mustWriteSkillFixture(t, filepath.Join(root, "reviewer", "SKILL.md"), `---
name: reviewer
description: Reviews changes
---
Reviewer body`)

	var reloadResp SkillRegistryStatusResponse
	postJSON(t, srv.Handler(), "/api/skills/reload", nil, &reloadResp, http.StatusOK)
	if len(reloadResp.AddedSkills) != 1 || reloadResp.AddedSkills[0] != "reviewer" {
		t.Fatalf("added skills = %#v", reloadResp)
	}
	if len(reloadResp.RemovedSkills) != 1 || reloadResp.RemovedSkills[0] != "debugger" {
		t.Fatalf("removed skills = %#v", reloadResp)
	}
	if len(reloadResp.UpdatedSkills) != 1 || reloadResp.UpdatedSkills[0] != "planner" {
		t.Fatalf("updated skills = %#v", reloadResp)
	}
	if len(reloadResp.AddedDiagnostics) != 0 || len(reloadResp.RemovedDiagnostics) != 0 {
		t.Fatalf("diagnostics diff = %#v", reloadResp)
	}
}

func TestServerSkillReloadReportsDiagnosticDiffs(t *testing.T) {
	root := t.TempDir()
	mustWriteSkillFixture(t, filepath.Join(root, "planner", "SKILL.md"), `---
name: planner
description: Plans work
---
Planner body`)
	mustWriteSkillFixture(t, filepath.Join(root, "broken", "SKILL.md"), `---
name: broken
---
Missing description`)
	initialSkills, initialDiagnostics, err := skill.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(initialDiagnostics) != 1 {
		t.Fatalf("initial diagnostics = %#v", initialDiagnostics)
	}
	srv := New(agentcore.Config{
		ModelConfig: agentcore.ModelConfig{
			Model:            "stub",
			Provider:         historyProvider{},
		},
		SkillConfig: agentcore.SkillConfig{
			AvailableSkills:  initialSkills,
			SkillDiagnostics: initialDiagnostics,
			SkillPaths:       []string{root},
		},
	})
	defer srv.Close()

	mustWriteSkillFixture(t, filepath.Join(root, "broken", "SKILL.md"), `---
name: broken
description: Fixed description
---
Recovered body`)

	var reloadResp SkillRegistryStatusResponse
	postJSON(t, srv.Handler(), "/api/skills/reload", nil, &reloadResp, http.StatusOK)
	if len(reloadResp.AddedDiagnostics) != 0 {
		t.Fatalf("unexpected added diagnostics = %#v", reloadResp.AddedDiagnostics)
	}
	if len(reloadResp.RemovedDiagnostics) != 1 {
		t.Fatalf("removed diagnostics = %#v", reloadResp.RemovedDiagnostics)
	}
	if !strings.Contains(reloadResp.RemovedDiagnostics[0].Path, "broken") || !strings.Contains(reloadResp.RemovedDiagnostics[0].Message, "description") {
		t.Fatalf("removed diagnostics payload = %#v", reloadResp.RemovedDiagnostics)
	}
}

func TestServerSkillReloadEmitsEvent(t *testing.T) {
	root := t.TempDir()
	mustWriteSkillFixture(t, filepath.Join(root, "planner", "SKILL.md"), `---
name: planner
description: Plans work
---
Planner body`)
	initialSkills, initialDiagnostics, err := skill.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(agentcore.Config{
		ModelConfig: agentcore.ModelConfig{
			Model:            "stub",
			Provider:         historyProvider{},
		},
		SkillConfig: agentcore.SkillConfig{
			AvailableSkills:  initialSkills,
			SkillDiagnostics: initialDiagnostics,
			SkillPaths:       []string{root},
		},
	})
	defer srv.Close()

	events := make(chan agentcore.SkillsReloadedEvent, 1)
	srv.On(agentcore.EventSkillsReloaded, func(e agentcore.Event) {
		if ev, ok := e.(agentcore.SkillsReloadedEvent); ok {
			events <- ev
		}
		if ev, ok := e.(*agentcore.SkillsReloadedEvent); ok {
			events <- *ev
		}
	})

	mustWriteSkillFixture(t, filepath.Join(root, "debugger", "SKILL.md"), `---
name: debugger
description: Debugs failures
---
Debugger body`)
	mustWriteSkillFixture(t, filepath.Join(root, "broken", "SKILL.md"), `---
name: broken
---
Missing description`)

	var reloadResp SkillRegistryStatusResponse
	postJSON(t, srv.Handler(), "/api/skills/reload", nil, &reloadResp, http.StatusOK)

	select {
	case ev := <-events:
		if ev.EventKind() != agentcore.EventSkillsReloaded || ev.TotalSkills != 2 || ev.VisibleSkills != 2 || ev.DiagnosticsCount != 1 {
			t.Fatalf("reload event = %#v", ev)
		}
		if len(ev.AddedSkills) != 1 || ev.AddedSkills[0] != "debugger" {
			t.Fatalf("reload event diff = %#v", ev)
		}
		if len(ev.AddedDiagnostics) != 1 || len(ev.RemovedDiagnostics) != 0 {
			t.Fatalf("reload event diagnostics diff = %#v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for skills_reloaded event")
	}
}

func TestServerSkillEventsEndpointStreamsReloadEvents(t *testing.T) {
	root := t.TempDir()
	mustWriteSkillFixture(t, filepath.Join(root, "planner", "SKILL.md"), `---
name: planner
description: Plans work
---
Planner body`)
	initialSkills, initialDiagnostics, err := skill.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(agentcore.Config{
		ModelConfig: agentcore.ModelConfig{
			Model:            "stub",
			Provider:         historyProvider{},
		},
		SkillConfig: agentcore.SkillConfig{
			AvailableSkills:  initialSkills,
			SkillDiagnostics: initialDiagnostics,
			SkillPaths:       []string{root},
		},
	})
	defer srv.Close()

	httpSrv := httptest.NewServer(srv.Handler())
	defer httpSrv.Close()

	stream := openSSEStream(t, httpSrv.URL+"/api/skills/events", nil)
	defer stream.cancel()
	<-stream.ready

	snapshot := nextSSEEvent(t, stream, 3*time.Second)
	if snapshot.Event != "skills_snapshot" {
		t.Fatalf("snapshot event = %#v", snapshot)
	}
	var snapshotPayload SkillsSnapshotStreamEvent
	if err := json.Unmarshal(snapshot.Data, &snapshotPayload); err != nil {
		t.Fatalf("decode snapshot payload: %v", err)
	}
	if snapshotPayload.Schema != streamSchemaSkillsSnapshot || snapshotPayload.Type != "skills_snapshot" {
		t.Fatalf("snapshot payload = %#v", snapshotPayload)
	}
	if snapshotPayload.Payload.TotalSkills != 1 || snapshotPayload.Payload.DiagnosticsCount != 0 {
		t.Fatalf("snapshot payload body = %#v", snapshotPayload.Payload)
	}

	mustWriteSkillFixture(t, filepath.Join(root, "debugger", "SKILL.md"), `---
name: debugger
description: Debugs failures
---
Debugger body`)
	mustWriteSkillFixture(t, filepath.Join(root, "broken", "SKILL.md"), `---
name: broken
---
Missing description`)

	var reloadResp SkillRegistryStatusResponse
	postJSON(t, srv.Handler(), "/api/skills/reload", nil, &reloadResp, http.StatusOK)

	ev := nextSSEEvent(t, stream, 3*time.Second)
	if ev.Event != "skills_reloaded" {
		t.Fatalf("event = %#v", ev)
	}
	var payload AgentStreamEvent
	if err := json.Unmarshal(ev.Data, &payload); err != nil {
		t.Fatalf("decode sse payload: %v", err)
	}
	if payload.Schema != streamSchemaAgentEvent || payload.Type != string(agentcore.EventSkillsReloaded) {
		t.Fatalf("payload = %#v", payload)
	}
	body, _ := json.Marshal(payload.Payload)
	if !strings.Contains(string(body), `"added_skills":["debugger"]`) {
		t.Fatalf("payload body = %s", body)
	}
	if !strings.Contains(string(body), `"added_diagnostics":[`) {
		t.Fatalf("payload diagnostics body = %s", body)
	}
}

func TestServerSkillAPIAuthorizationAndDisableSwitches(t *testing.T) {
	root := t.TempDir()
	mustWriteSkillFixture(t, filepath.Join(root, "planner", "SKILL.md"), `---
name: planner
description: Plans work
---
Planner body`)
	loadedSkills, loadedDiagnostics, err := skill.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	disabled := New(agentcore.Config{
		ModelConfig: agentcore.ModelConfig{
			Model:                   "stub",
			Provider:                historyProvider{},
		},
		SkillConfig: agentcore.SkillConfig{
			AvailableSkills:         loadedSkills,
			SkillDiagnostics:        loadedDiagnostics,
			DisableSkillRegistryAPI: true,
			DisableSkillReloadAPI:   true,
			SkillPaths:              []string{root},
		},
	})
	resp := doRequest(t, disabled.Handler(), http.MethodGet, "/api/skills", nil, nil)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("registry status = %d body=%s", resp.Code, resp.Body.String())
	}
	resp = doRequest(t, disabled.Handler(), http.MethodGet, "/api/skills/events", nil, nil)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("events status = %d body=%s", resp.Code, resp.Body.String())
	}
	resp = doRequest(t, disabled.Handler(), http.MethodPost, "/api/skills/reload", nil, nil)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("reload status = %d body=%s", resp.Code, resp.Body.String())
	}

	protected := New(agentcore.Config{
		ModelConfig: agentcore.ModelConfig{
			Model:             "stub",
			Provider:          historyProvider{},
		},
		SkillConfig: agentcore.SkillConfig{
			AvailableSkills:   loadedSkills,
			SkillDiagnostics:  loadedDiagnostics,
			SkillPaths:        []string{root},
			SkillAPIAuthToken: "secret-token",
		},
	})
	resp = doRequest(t, protected.Handler(), http.MethodGet, "/api/skills/status", nil, nil)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d body=%s", resp.Code, resp.Body.String())
	}
	resp = doRequest(t, protected.Handler(), http.MethodGet, "/api/skills/events", nil, nil)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized events status = %d body=%s", resp.Code, resp.Body.String())
	}
	resp = doRequest(t, protected.Handler(), http.MethodGet, "/api/skills/status", nil, map[string]string{
		"Authorization": "Bearer secret-token",
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("authorized status = %d body=%s", resp.Code, resp.Body.String())
	}
	resp = doRequest(t, protected.Handler(), http.MethodPost, "/api/skills/reload", nil, map[string]string{
		"Authorization": "Bearer secret-token",
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("authorized reload status = %d body=%s", resp.Code, resp.Body.String())
	}
}

func TestServerThreadEndpointsWithSessionStore(t *testing.T) {
	sessionFS, err := session.NewFileStore(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	threadStore := session.NewAgentStore(sessionFS, "/project")
	srv := New(agentcore.Config{
		ModelConfig: agentcore.ModelConfig{
			Model:    "stub",
			Provider: historyProvider{},
		},
		Store:    threadStore,
	})

	postChat(t, srv.Handler(), ChatRequest{Message: "hello", ThreadID: "thread-a"})
	postChat(t, srv.Handler(), ChatRequest{Message: "follow up", ThreadID: "thread-a"})
	postChat(t, srv.Handler(), ChatRequest{Message: "other", ThreadID: "thread-b"})

	var listResp struct {
		Threads []session.Info `json:"threads"`
	}
	getJSON(t, srv.Handler(), http.MethodGet, "/api/threads", &listResp, http.StatusOK)
	if len(listResp.Threads) != 2 {
		t.Fatalf("threads len = %d", len(listResp.Threads))
	}

	var threadResp session.ThreadSnapshot
	getJSON(t, srv.Handler(), http.MethodGet, "/api/threads/thread-a", &threadResp, http.StatusOK)
	if threadResp.Info.ID != "thread-a" {
		t.Fatalf("thread id = %q", threadResp.Info.ID)
	}
	if len(threadResp.Transcript) != 4 {
		t.Fatalf("transcript len = %d", len(threadResp.Transcript))
	}
	for i, item := range threadResp.Transcript {
		if item.EntryID == "" {
			t.Fatalf("transcript[%d] missing entry_id", i)
		}
	}
	if len(threadResp.Messages) != 4 {
		t.Fatalf("messages len = %d", len(threadResp.Messages))
	}
	if threadResp.Turn != 2 {
		t.Fatalf("turn = %d", threadResp.Turn)
	}

	var branchResp session.ThreadSnapshot
	postJSON(t, srv.Handler(), "/api/threads/thread-a/branch", nil, &branchResp, http.StatusOK)
	if branchResp.Info.ID == "thread-a" {
		t.Fatal("expected branch to create a new thread id")
	}
	if branchResp.Info.ParentSession != "thread-a" {
		t.Fatalf("parent_session = %q", branchResp.Info.ParentSession)
	}
	if len(branchResp.Messages) != 4 {
		t.Fatalf("branch messages len = %d", len(branchResp.Messages))
	}

	var historicalBranchResp session.ThreadSnapshot
	postJSON(t, srv.Handler(), "/api/threads/thread-a/branch", BranchThreadRequest{
		EntryID: threadResp.Transcript[1].EntryID,
	}, &historicalBranchResp, http.StatusOK)
	if historicalBranchResp.Info.ID == "thread-a" || historicalBranchResp.Info.ID == branchResp.Info.ID {
		t.Fatal("expected historical branch to create a distinct thread id")
	}
	if historicalBranchResp.Info.ParentSession != "thread-a" {
		t.Fatalf("historical parent_session = %q", historicalBranchResp.Info.ParentSession)
	}
	if len(historicalBranchResp.Messages) != 2 {
		t.Fatalf("historical branch messages len = %d", len(historicalBranchResp.Messages))
	}
	if historicalBranchResp.Messages[1].Content != "users:1 last:hello" {
		t.Fatalf("historical branch last message = %#v", historicalBranchResp.Messages[1])
	}

	deleteRequest(t, srv.Handler(), "/api/threads/thread-b", http.StatusNoContent)

	getJSON(t, srv.Handler(), http.MethodGet, "/api/threads", &listResp, http.StatusOK)
	if len(listResp.Threads) != 3 {
		t.Fatalf("threads len after branch/delete = %d", len(listResp.Threads))
	}
	foundOriginal := false
	foundBranch := false
	foundHistoricalBranch := false
	for _, thread := range listResp.Threads {
		if thread.ID == "thread-a" {
			foundOriginal = true
		}
		if thread.ID == branchResp.Info.ID {
			foundBranch = true
		}
		if thread.ID == historicalBranchResp.Info.ID {
			foundHistoricalBranch = true
		}
	}
	if !foundOriginal || !foundBranch || !foundHistoricalBranch {
		t.Fatalf("threads = %#v", listResp.Threads)
	}
}

func TestServerChatAutoCreatesThreadForSessionStore(t *testing.T) {
	sessionFS, err := session.NewFileStore(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	threadStore := session.NewAgentStore(sessionFS, "/project")
	srv := New(agentcore.Config{
		ModelConfig: agentcore.ModelConfig{
			Model:    "stub",
			Provider: historyProvider{},
		},
		Store:    threadStore,
	})

	first := postChat(t, srv.Handler(), ChatRequest{Message: "hello"})
	if first.ThreadID == "" {
		t.Fatal("expected server-generated thread_id")
	}
	if first.Output != "users:1 last:hello" {
		t.Fatalf("first output = %q", first.Output)
	}

	second := postChat(t, srv.Handler(), ChatRequest{Message: "again", ThreadID: first.ThreadID})
	if second.ThreadID != first.ThreadID {
		t.Fatalf("second thread_id = %q want %q", second.ThreadID, first.ThreadID)
	}
	if second.Output != "users:2 last:again" {
		t.Fatalf("second output = %q", second.Output)
	}
}

func TestServerCreateThreadEndpoint(t *testing.T) {
	sessionFS, err := session.NewFileStore(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	threadStore := session.NewAgentStore(sessionFS, "/project")
	srv := New(agentcore.Config{
		ModelConfig: agentcore.ModelConfig{
			Model:    "stub",
			Provider: historyProvider{},
		},
		Store:    threadStore,
	})

	var threadResp session.ThreadSnapshot
	postJSON(t, srv.Handler(), "/api/threads", nil, &threadResp, http.StatusOK)
	if threadResp.Info.ID == "" {
		t.Fatal("expected created thread id")
	}
	if threadResp.Status != agentcore.StatusIdle {
		t.Fatalf("status = %q", threadResp.Status)
	}
	if len(threadResp.Messages) != 0 {
		t.Fatalf("messages len = %d", len(threadResp.Messages))
	}

	var loaded session.ThreadSnapshot
	getJSON(t, srv.Handler(), http.MethodGet, "/api/threads/"+threadResp.Info.ID, &loaded, http.StatusOK)
	if loaded.Info.ID != threadResp.Info.ID {
		t.Fatalf("loaded id = %q want %q", loaded.Info.ID, threadResp.Info.ID)
	}
}

func TestServerThreadEndpointsRequireThreadCapableStore(t *testing.T) {
	srv := New(agentcore.Config{
		ModelConfig: agentcore.ModelConfig{
			Model:    "stub",
			Provider: historyProvider{},
		},
		Store:    newMemoryStore(),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/threads", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/threads", nil)
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}
}

func postChat(t *testing.T, handler http.Handler, req ChatRequest) ChatResponse {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	r := httptest.NewRequest(http.MethodPost, "/api/chat", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}

	var resp ChatResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

func getJSON(t *testing.T, handler http.Handler, method, path string, out any, wantStatus int) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != wantStatus {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}
	if out == nil {
		return
	}
	if err := json.NewDecoder(w.Body).Decode(out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

func getRaw(t *testing.T, handler http.Handler, method, path string, wantStatus int) string {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != wantStatus {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}
	return w.Body.String()
}

func doRequest(t *testing.T, handler http.Handler, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode request: %v", err)
		}
		reader = &buf
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

type sseStreamHandle struct {
	ready  chan struct{}
	events chan sseEventRecord
	errs   chan error
	cancel context.CancelFunc
}

func openSSEStream(t *testing.T, url string, headers map[string]string) sseStreamHandle {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	handle := sseStreamHandle{
		ready:  make(chan struct{}),
		events: make(chan sseEventRecord, 4),
		errs:   make(chan error, 1),
		cancel: cancel,
	}
	go func() {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			handle.errs <- err
			return
		}
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			handle.errs <- err
			return
		}
		defer resp.Body.Close()
		close(handle.ready)

		scanner := bufio.NewScanner(resp.Body)
		var eventName string
		var dataLines []string
		flush := func() bool {
			if eventName == "" && len(dataLines) == 0 {
				return false
			}
			handle.events <- sseEventRecord{
				Event: eventName,
				Data:  json.RawMessage(strings.Join(dataLines, "\n")),
			}
			eventName = ""
			dataLines = nil
			return true
		}
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, ":") {
				continue
			}
			if line == "" {
				flush()
				continue
			}
			if strings.HasPrefix(line, "event: ") {
				eventName = strings.TrimPrefix(line, "event: ")
				continue
			}
			if strings.HasPrefix(line, "data: ") {
				dataLines = append(dataLines, strings.TrimPrefix(line, "data: "))
			}
		}
		if err := scanner.Err(); err != nil && ctx.Err() == nil {
			handle.errs <- err
		}
	}()
	return handle
}

func nextSSEEvent(t *testing.T, stream sseStreamHandle, timeout time.Duration) sseEventRecord {
	t.Helper()
	select {
	case err := <-stream.errs:
		t.Fatalf("stream error: %v", err)
	case ev := <-stream.events:
		return ev
	case <-time.After(timeout):
		t.Fatal("timed out waiting for sse event")
	}
	return sseEventRecord{}
}

func mustWriteSkillFixture(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func postJSON(t *testing.T, handler http.Handler, path string, body any, out any, wantStatus int) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode request: %v", err)
		}
	}
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != wantStatus {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}
	if out == nil {
		return
	}
	if err := json.NewDecoder(w.Body).Decode(out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

func putJSON(t *testing.T, handler http.Handler, path string, body any, out any, wantStatus int) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode request: %v", err)
		}
	}
	req := httptest.NewRequest(http.MethodPut, path, &buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != wantStatus {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}
	if out == nil {
		return
	}
	if err := json.NewDecoder(w.Body).Decode(out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

func deleteRequest(t *testing.T, handler http.Handler, path string, wantStatus int) {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, path, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != wantStatus {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}
}

func postChatStreamRaw(t *testing.T, handler http.Handler, req ChatRequest) string {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/chat", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}
	return w.Body.String()
}

func newServerMCPStdioExtension(t *testing.T) *mcp.StdioExtension {
	t.Helper()
	serverMCPSetToolVersion(1)
	ext, err := mcp.NewStdioExtension(context.Background(), mcp.StdioConfig{
		Name:          "mcp-server-test",
		Command:       os.Args[0],
		Args:          []string{"-test.run=TestServerMCPHelperProcess", "--"},
		Env:           []string{"GO_WANT_SERVER_MCP_HELPER_PROCESS=1"},
		ToolPrefix:    "mcp.",
		ClientName:    "covonaut-server-test",
		ClientVersion: "0.0.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	return ext
}

func TestServerMCPHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_SERVER_MCP_HELPER_PROCESS") != "1" {
		return
	}
	runServerMCPHelper()
	os.Exit(0)
}

var serverMCPToolVersion = 1

func serverMCPSetToolVersion(v int) { serverMCPToolVersion = v }

func runServerMCPHelper() {
	scanner := bufio.NewScanner(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)
	defer writer.Flush()

	for scanner.Scan() {
		line := scanner.Bytes()
		var msg map[string]any
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		method, _ := msg["method"].(string)
		id := msg["id"]
		switch method {
		case "initialize":
			writeServerMCPResponse(writer, id, map[string]any{
				"protocolVersion": "2025-11-25",
				"capabilities": map[string]any{
					"tools": map[string]any{"listChanged": true},
				},
				"serverInfo": map[string]any{
					"name":    "fake-server-mcp",
					"version": "1.0.0",
				},
			})
		case "notifications/initialized":
		case "tools/list":
			writeServerMCPResponse(writer, id, map[string]any{
				"tools": serverMCPTools(),
			})
		case "tools/call":
			params, _ := msg["params"].(map[string]any)
			name, _ := params["name"].(string)
			args, _ := params["arguments"].(map[string]any)
			text, _ := args["text"].(string)
			if name == "echo" && text == "refresh-tools" {
				writeServerMCPResponse(writer, id, map[string]any{
					"content": []map[string]any{
						{"type": "text", "text": "tool list updated"},
					},
				})
				serverMCPSetToolVersion(2)
				writeServerMCPNotification(writer, "notifications/tools/list_changed", nil)
				continue
			}
			if name == "reverse" {
				writeServerMCPResponse(writer, id, map[string]any{
					"content": []map[string]any{
						{"type": "text", "text": reverseServerMCPString(text)},
					},
				})
				continue
			}
			writeServerMCPResponse(writer, id, map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": "echo: " + text},
				},
			})
		default:
			writeServerMCPError(writer, id, -32601, fmt.Sprintf("method not found: %s", method))
		}
	}
}

func serverMCPTools() []map[string]any {
	if serverMCPToolVersion == 2 {
		return []map[string]any{
			{
				"name":        "reverse",
				"description": "Reverse a string",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"text": map[string]any{"type": "string"},
					},
					"required": []string{"text"},
				},
			},
		}
	}
	return []map[string]any{
		{
			"name":        "echo",
			"description": "Echo back a string",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"text": map[string]any{"type": "string"},
				},
				"required": []string{"text"},
			},
		},
	}
}

func writeServerMCPResponse(w *bufio.Writer, id any, result any) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
	_ = w.Flush()
}

func writeServerMCPNotification(w *bufio.Writer, method string, params any) {
	msg := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if params != nil {
		msg["params"] = params
	}
	_ = json.NewEncoder(w).Encode(msg)
	_ = w.Flush()
}

func writeServerMCPError(w *bufio.Writer, id any, code int64, message string) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
	_ = w.Flush()
}

func reverseServerMCPString(v string) string {
	runes := []rune(v)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes)
}

type sseEventRecord struct {
	Event string
	Data  json.RawMessage
}

func parseSSEEvents(t *testing.T, body string) []sseEventRecord {
	t.Helper()
	scanner := bufio.NewScanner(strings.NewReader(body))
	var out []sseEventRecord
	var eventName string
	var dataLines []string
	flush := func() {
		if eventName == "" && len(dataLines) == 0 {
			return
		}
		out = append(out, sseEventRecord{
			Event: eventName,
			Data:  json.RawMessage(strings.Join(dataLines, "\n")),
		})
		eventName = ""
		dataLines = nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, "event: ") {
			eventName = strings.TrimPrefix(line, "event: ")
			continue
		}
		if strings.HasPrefix(line, "data: ") {
			dataLines = append(dataLines, strings.TrimPrefix(line, "data: "))
		}
	}
	flush()
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan sse body: %v", err)
	}
	return out
}

func messagesContain(t *testing.T, msgs []agentcore.Message, needle string) bool {
	t.Helper()
	for _, msg := range msgs {
		if strings.Contains(msg.Content, needle) {
			return true
		}
	}
	return false
}
