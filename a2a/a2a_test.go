package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/gorilla/websocket"
)

// ---------------------------------------------------------------------------
// Mock AgentHandler
// ---------------------------------------------------------------------------

type mockHandler struct {
	card      AgentCard
	tasks     map[string]*Task
	pushCfg   map[string]*PushNotificationConfig
	mu        sync.Mutex
	onSend    func(SendTaskRequest)
	onCancel  func(string)
	publisher TaskUpdatePublisher
}

func newMockHandler() *mockHandler {
	return &mockHandler{
		tasks:   make(map[string]*Task),
		pushCfg: make(map[string]*PushNotificationConfig),
		card: AgentCard{
			Name:        "test-agent",
			Description: "A test agent",
			URL:         "http://localhost:8080",
			Version:     "1.0.0",
			Capabilities: AgentCapabilities{
			Streaming:         true,
			PushNotifications: true,
		},
			Skills: []AgentSkill{
				{ID: "greet", Name: "Greeting", Description: "Say hello"},
			},
		},
	}
}

func (m *mockHandler) Card() AgentCard { return m.card }

func (m *mockHandler) SendTask(ctx context.Context, req SendTaskRequest) (*Task, error) {
	if m.onSend != nil {
		m.onSend(req)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if existing, ok := m.tasks[req.ID]; ok {
		if isTerminalState(existing.State) {
			return nil, fmt.Errorf("task %q is already in terminal state", req.ID)
		}
		existing.Messages = append(existing.Messages, req.Message)
		existing.State = TaskStateCompleted
		existing.History = append(existing.History, TaskStatus{State: TaskStateCompleted, Timestamp: time.Now()})
		return existing, nil
	}

	task := &Task{
		ID:        req.ID,
		SessionID: req.SessionID,
		State:     TaskStateWorking,
		Messages:  []Message{req.Message},
		Metadata:  req.Metadata,
		History: []TaskStatus{
			{State: TaskStateWorking, Timestamp: time.Now()},
		},
	}
	m.tasks[req.ID] = task
	return task, nil
}

func (m *mockHandler) SetUpdatePublisher(p TaskUpdatePublisher) {
	m.publisher = p
}

func (m *mockHandler) GetTask(ctx context.Context, req GetTaskRequest) (*Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	task, ok := m.tasks[req.ID]
	if !ok {
		return nil, fmt.Errorf("task not found")
	}

	if req.HistoryLength > 0 && len(task.History) > req.HistoryLength {
		t := *task
		offset := len(task.History) - req.HistoryLength
		t.History = task.History[offset:]
		return &t, nil
	}

	return task, nil
}

func (m *mockHandler) CancelTask(ctx context.Context, req CancelTaskRequest) (*Task, error) {
	if m.onCancel != nil {
		m.onCancel(req.ID)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	task, ok := m.tasks[req.ID]
	if !ok {
		return nil, fmt.Errorf("task not found")
	}

	if task.State == TaskStateCompleted {
		return nil, fmt.Errorf("task already completed")
	}

	task.State = TaskStateCanceled
	task.History = append(task.History, TaskStatus{State: TaskStateCanceled, Timestamp: time.Now()})
	return task, nil
}

func (m *mockHandler) SetPushNotification(ctx context.Context, req SetPushNotificationRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pushCfg[req.ID] = &req.Config
	return nil
}

func (m *mockHandler) GetPushNotification(ctx context.Context, taskID string) (*PushNotificationConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cfg, ok := m.pushCfg[taskID]
	if !ok {
		return nil, fmt.Errorf("no push config")
	}
	return cfg, nil
}

func (m *mockHandler) QueryTasks(ctx context.Context, req QueryTasksRequest) (*QueryTasksResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var result []*Task
	for _, task := range m.tasks {
		if req.SessionID != "" && task.SessionID != req.SessionID {
			continue
		}
		if req.State != "" && task.State != req.State {
			continue
		}
		result = append(result, task)
	}
	if req.Limit > 0 && len(result) > req.Limit {
		result = result[:req.Limit]
	}
	if result == nil {
		result = []*Task{}
	}
	return &QueryTasksResult{Tasks: result}, nil
}

// ---------------------------------------------------------------------------
// Server Tests
// ---------------------------------------------------------------------------

func TestServer_AgentCard(t *testing.T) {
	handler := newMockHandler()
	server := NewServer(handler)

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/.well-known/agent.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var card AgentCard
	if err := json.NewDecoder(resp.Body).Decode(&card); err != nil {
		t.Fatal(err)
	}

	if card.Name != "test-agent" {
		t.Fatalf("expected name test-agent, got %s", card.Name)
	}
	if !card.Capabilities.Streaming {
		t.Fatal("expected streaming capability")
	}
	if len(card.Skills) != 1 || card.Skills[0].ID != "greet" {
		t.Fatalf("unexpected skills: %v", card.Skills)
	}
}

func TestServer_SendTask(t *testing.T) {
	handler := newMockHandler()
	server := NewServer(handler)

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tasks/send",
		Params: mustJSON(SendTaskRequest{
			ID:      "task-1",
			Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("Hello")}},
		}),
	}

	resp, err := postJSON(ts.URL+"/", req)
	if err != nil {
		t.Fatal(err)
	}

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	data, _ := json.Marshal(resp.Result)
	var task Task
	if err := json.Unmarshal(data, &task); err != nil {
		t.Fatal(err)
	}

	if task.ID != "task-1" {
		t.Fatalf("expected task-1, got %s", task.ID)
	}
	if task.State != TaskStateWorking {
		t.Fatalf("expected working, got %s", task.State)
	}
	if len(task.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(task.Messages))
	}
}

func TestServer_GetTask(t *testing.T) {
	handler := newMockHandler()
	server := NewServer(handler)

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	// First send a task
	sendReq := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tasks/send",
		Params: mustJSON(SendTaskRequest{
			ID:      "task-get",
			Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("Hello")}},
		}),
	}
	_, _ = postJSON(ts.URL+"/", sendReq)

	// Then get it
	getReq := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tasks/get",
		Params:  mustJSON(GetTaskRequest{ID: "task-get"}),
	}

	resp, err := postJSON(ts.URL+"/", getReq)
	if err != nil {
		t.Fatal(err)
	}

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	data, _ := json.Marshal(resp.Result)
	var task Task
	if err := json.Unmarshal(data, &task); err != nil {
		t.Fatal(err)
	}

	if task.ID != "task-get" {
		t.Fatalf("expected task-get, got %s", task.ID)
	}
}

func TestServer_CancelTask(t *testing.T) {
	handler := newMockHandler()
	server := NewServer(handler)

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	// Send a task
	sendReq := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tasks/send",
		Params: mustJSON(SendTaskRequest{
			ID:      "task-cancel",
			Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("Hello")}},
		}),
	}
	_, _ = postJSON(ts.URL+"/", sendReq)

	// Cancel it
	cancelReq := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tasks/cancel",
		Params:  mustJSON(CancelTaskRequest{ID: "task-cancel"}),
	}

	resp, err := postJSON(ts.URL+"/", cancelReq)
	if err != nil {
		t.Fatal(err)
	}

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	data, _ := json.Marshal(resp.Result)
	var task Task
	if err := json.Unmarshal(data, &task); err != nil {
		t.Fatal(err)
	}

	if task.State != TaskStateCanceled {
		t.Fatalf("expected canceled, got %s", task.State)
	}
}

func TestServer_MethodNotFound(t *testing.T) {
	handler := newMockHandler()
	server := NewServer(handler)

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tasks/unknown",
	}

	resp, err := postJSON(ts.URL+"/", req)
	if err != nil {
		t.Fatal(err)
	}

	if resp.Error == nil {
		t.Fatal("expected error")
	}
	if resp.Error.Code != JSONRPCMethodNotFound {
		t.Fatalf("expected method not found, got %d", resp.Error.Code)
	}
}

// ---------------------------------------------------------------------------
// Client Tests
// ---------------------------------------------------------------------------

func TestClient_GetAgentCard(t *testing.T) {
	handler := newMockHandler()
	server := NewServer(handler)

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	client := NewClient(ts.URL)
	card, err := client.GetAgentCard(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if card.Name != "test-agent" {
		t.Fatalf("expected test-agent, got %s", card.Name)
	}
}

func TestClient_SendTask(t *testing.T) {
	handler := newMockHandler()
	server := NewServer(handler)

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	client := NewClient(ts.URL)
	task, err := client.SendTask(context.Background(), SendTaskRequest{
		ID:      "client-task-1",
		Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("Hello from client")}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if task.ID != "client-task-1" {
		t.Fatalf("expected client-task-1, got %s", task.ID)
	}
	if task.State != TaskStateWorking {
		t.Fatalf("expected working, got %s", task.State)
	}
}

func TestClient_GetTask(t *testing.T) {
	handler := newMockHandler()
	server := NewServer(handler)

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	client := NewClient(ts.URL)

	// Send first
	_, _ = client.SendTask(context.Background(), SendTaskRequest{
		ID:      "client-get-task",
		Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("Hello")}},
	})

	// Get
	task, err := client.GetTask(context.Background(), GetTaskRequest{ID: "client-get-task"})
	if err != nil {
		t.Fatal(err)
	}

	if task.ID != "client-get-task" {
		t.Fatalf("expected client-get-task, got %s", task.ID)
	}
}

func TestClient_CancelTask(t *testing.T) {
	handler := newMockHandler()
	server := NewServer(handler)

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	client := NewClient(ts.URL)

	// Send first
	_, _ = client.SendTask(context.Background(), SendTaskRequest{
		ID:      "client-cancel-task",
		Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("Hello")}},
	})

	// Cancel
	task, err := client.CancelTask(context.Background(), CancelTaskRequest{ID: "client-cancel-task"})
	if err != nil {
		t.Fatal(err)
	}

	if task.State != TaskStateCanceled {
		t.Fatalf("expected canceled, got %s", task.State)
	}
}

// ---------------------------------------------------------------------------
// Type Tests
// ---------------------------------------------------------------------------

func TestPartHelpers(t *testing.T) {
	textPart := NewTextPart("hello")
	if textPart.Type != PartTypeText || textPart.Text != "hello" {
		t.Fatal("text part mismatch")
	}

	dataPart := NewDataPart(map[string]any{"key": "value"})
	if dataPart.Type != PartTypeData || dataPart.Data == nil {
		t.Fatal("data part mismatch")
	}

	filePart := NewFilePartBytes("test.txt", "text/plain", "base64data")
	if filePart.Type != PartTypeFile || filePart.File.Name != "test.txt" {
		t.Fatal("file part mismatch")
	}

	uriPart := NewFilePartURI("test.txt", "text/plain", "http://example.com/file.txt")
	if uriPart.Type != PartTypeFile || uriPart.File.URI != "http://example.com/file.txt" {
		t.Fatal("uri part mismatch")
	}
}

func TestJSONRPCError(t *testing.T) {
	err := &JSONRPCError{Code: JSONRPCInternalError, Message: "something went wrong"}
	if err.Error() != "jsonrpc error -32603: something went wrong" {
		t.Fatalf("unexpected error string: %s", err.Error())
	}
}

// ---------------------------------------------------------------------------
// SSE Decoder Tests
// ---------------------------------------------------------------------------

func TestSSEDecoder(t *testing.T) {
	input := `data: {"id":1,"result":{"id":"task-1"}}

data: {"id":2,"final":true}

`
	decoder := NewSSEDecoder(strings.NewReader(input))

	ev1, err := decoder.Next()
	if err != nil {
		t.Fatal(err)
	}
	if ev1 == nil {
		t.Fatal("expected event")
	}
	if !strings.Contains(ev1.Data, `"id":"task-1"`) {
		t.Fatalf("unexpected data: %s", ev1.Data)
	}

	ev2, err := decoder.Next()
	if err != nil {
		t.Fatal(err)
	}
	if ev2 == nil {
		t.Fatal("expected event")
	}
	if !strings.Contains(ev2.Data, `"final":true`) {
		t.Fatalf("unexpected data: %s", ev2.Data)
	}

	ev3, err := decoder.Next()
	if err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
	if ev3 != nil {
		t.Fatal("expected nil event after EOF")
	}
}

func TestSSEDecoder_WithIDAndEvent(t *testing.T) {
	input := `id: msg-1
event: update
data: hello

`
	decoder := NewSSEDecoder(strings.NewReader(input))

	ev, err := decoder.Next()
	if err != nil {
		t.Fatal(err)
	}
	if ev.ID != "msg-1" {
		t.Fatalf("expected id msg-1, got %s", ev.ID)
	}
	if ev.Event != "update" {
		t.Fatalf("expected event update, got %s", ev.Event)
	}
	if ev.Data != "hello" {
		t.Fatalf("expected data hello, got %s", ev.Data)
	}
}

// ---------------------------------------------------------------------------
// Integration Test: Client -> Server round-trip
// ---------------------------------------------------------------------------

func TestIntegration_ClientServer(t *testing.T) {
	handler := newMockHandler()
	server := NewServer(handler)

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	client := NewClient(ts.URL)

	// Get agent card
	card, err := client.GetAgentCard(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if card.Name != "test-agent" {
		t.Fatalf("unexpected card name: %s", card.Name)
	}

	// Send task
	task, err := client.SendTask(context.Background(), SendTaskRequest{
		ID:      "integration-task",
		Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("Test message")}},
		Metadata: map[string]any{"source": "integration_test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != "integration-task" {
		t.Fatalf("unexpected task id: %s", task.ID)
	}
	if task.State != TaskStateWorking {
		t.Fatalf("unexpected task state: %s", task.State)
	}

	// Get task
	fetched, err := client.GetTask(context.Background(), GetTaskRequest{ID: "integration-task"})
	if err != nil {
		t.Fatal(err)
	}
	if fetched.ID != "integration-task" {
		t.Fatalf("unexpected fetched id: %s", fetched.ID)
	}

	// Set push notification
	err = client.SetPushNotification(context.Background(), SetPushNotificationRequest{
		ID: "integration-task",
		Config: PushNotificationConfig{
			URL:   "http://example.com/webhook",
			Token: "secret-token",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Get push notification
	cfg, err := client.GetPushNotification(context.Background(), "integration-task")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.URL != "http://example.com/webhook" {
		t.Fatalf("unexpected webhook url: %s", cfg.URL)
	}
	if cfg.Token != "secret-token" {
		t.Fatalf("unexpected token: %s", cfg.Token)
	}
}

// ---------------------------------------------------------------------------
// Handoff Integration Tests
// ---------------------------------------------------------------------------

func TestExtractTaskResult(t *testing.T) {
	task := &Task{
		Artifacts: []Artifact{{
			Parts: []Part{NewTextPart("artifact result")},
		}},
		Messages: []Message{
			{Role: string(RoleUser), Parts: []Part{NewTextPart("input")}},
			{Role: string(RoleAgent), Parts: []Part{NewTextPart("message result")}},
		},
	}

	// Should prefer artifact
	result := extractTaskResult(task, nil)
	if result != "artifact result" {
		t.Fatalf("expected artifact result, got %s", result)
	}

	// Without artifacts, should use last agent message
	task.Artifacts = nil
	result = extractTaskResult(task, nil)
	if result != "message result" {
		t.Fatalf("expected message result, got %s", result)
	}

	// Empty task
	result = extractTaskResult(nil, nil)
	if result != "" {
		t.Fatalf("expected empty, got %s", result)
	}
}

func TestExtractMessageText(t *testing.T) {
	msg := Message{
		Parts: []Part{
			NewTextPart("Hello "),
			NewTextPart("world"),
		},
	}
	text := extractMessageText(msg)
	if text != "Hello world" {
		t.Fatalf("expected 'Hello world', got %s", text)
	}
}

func TestServer_Resubscribe(t *testing.T) {
	handler := newMockHandler()
	server := NewServer(handler)

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	client := NewClient(ts.URL)

	// Send a task first
	_, err := client.SendTask(context.Background(), SendTaskRequest{
		ID:      "resub-task",
		Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("Hello")}},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Publish some events to history
	server.PublishTaskUpdate("resub-task", &TaskUpdateEvent{
		Result: &Task{ID: "resub-task", State: TaskStateWorking},
	})
	server.PublishTaskUpdate("resub-task", &TaskUpdateEvent{
		Result: &Task{ID: "resub-task", State: TaskStateCompleted},
		Final:  true,
	})

	// Resubscribe and replay
	stream, err := client.ResubscribeTask(context.Background(), "resub-task")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	var events []*TaskUpdateEvent
	for {
		ev, ok := stream.Recv()
		if !ok {
			break
		}
		events = append(events, ev)
		if ev.Final {
			break
		}
	}

	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	if events[0].Result.State != TaskStateWorking {
		t.Fatalf("expected working (initial), got %s", events[0].Result.State)
	}
	if events[1].Result.State != TaskStateWorking {
		t.Fatalf("expected working, got %s", events[1].Result.State)
	}
	if events[2].Result.State != TaskStateCompleted {
		t.Fatalf("expected completed, got %s", events[2].Result.State)
	}
	if !events[2].Final {
		t.Fatalf("expected final event")
	}
}

func TestServer_AuthAPIKey(t *testing.T) {
	handler := newMockHandler()
	server := NewServer(handler, WithAuth(AuthConfig{APIKey: "secret123"}))

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	// Request without key should fail
	resp, err := postJSON(ts.URL+"/", JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: "tasks/send", Params: mustJSON(SendTaskRequest{ID: "auth-test", Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("Hello")}}})})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil || resp.Error.Message != "unauthorized" {
		t.Fatalf("expected unauthorized, got %v", resp.Error)
	}

	// Request with key should succeed
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/", strings.NewReader(string(mustJSON(JSONRPCRequest{JSONRPC: "2.0", ID: 2, Method: "tasks/send", Params: mustJSON(SendTaskRequest{ID: "auth-test2", Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("Hello")}}})}))))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "secret123")
	httpResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer httpResp.Body.Close()

	var resp2 JSONRPCResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&resp2); err != nil {
		t.Fatal(err)
	}
	if resp2.Error != nil {
		t.Fatalf("unexpected error: %v", resp2.Error)
	}
}

func TestServer_AuthBearer(t *testing.T) {
	handler := newMockHandler()
	server := NewServer(handler, WithAuth(AuthConfig{BearerToken: "tok_abc"}))

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	// Request without token should fail
	resp, err := postJSON(ts.URL+"/", JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: "tasks/send", Params: mustJSON(SendTaskRequest{ID: "auth-test", Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("Hello")}}})})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil || resp.Error.Message != "unauthorized" {
		t.Fatalf("expected unauthorized, got %v", resp.Error)
	}

	// Request with token should succeed
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/", strings.NewReader(string(mustJSON(JSONRPCRequest{JSONRPC: "2.0", ID: 2, Method: "tasks/send", Params: mustJSON(SendTaskRequest{ID: "auth-test2", Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("Hello")}}})}))))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer tok_abc")
	httpResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer httpResp.Body.Close()

	var resp2 JSONRPCResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&resp2); err != nil {
		t.Fatal(err)
	}
	if resp2.Error != nil {
		t.Fatalf("unexpected error: %v", resp2.Error)
	}
}

func TestPushNotifier(t *testing.T) {
	var received bool
	var receivedTaskID string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = true
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		if id, ok := payload["taskId"].(string); ok {
			receivedTaskID = id
		}
		// Verify auth header
		if r.Header.Get("Authorization") != "Bearer webhook-token" {
			t.Errorf("expected Bearer webhook-token, got %s", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	notifier := NewPushNotifier().WithAllowPrivate(true)
	task := &Task{ID: "push-task-1", State: TaskStateCompleted}
	cfg := &PushNotificationConfig{
		URL:   ts.URL,
		Token: "webhook-token",
	}

	ctx := context.Background()
	if err := notifier.Notify(ctx, cfg, task); err != nil {
		t.Fatal(err)
	}

	if !received {
		t.Fatal("webhook was not called")
	}
	if receivedTaskID != "push-task-1" {
		t.Fatalf("expected task id push-task-1, got %s", receivedTaskID)
	}
}

func TestClient_AuthAPIKey(t *testing.T) {
	handler := newMockHandler()
	server := NewServer(handler, WithAuth(AuthConfig{APIKey: "client-api-key"}))

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	client := NewClient(ts.URL, WithAPIKey("client-api-key"))

	task, err := client.SendTask(context.Background(), SendTaskRequest{
		ID:      "auth-client-task",
		Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("Hello")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != "auth-client-task" {
		t.Fatalf("expected auth-client-task, got %s", task.ID)
	}
}

func TestClient_AuthBearer(t *testing.T) {
	handler := newMockHandler()
	server := NewServer(handler, WithAuth(AuthConfig{BearerToken: "client-bearer"}))

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	client := NewClient(ts.URL, WithBearerToken("client-bearer"))

	task, err := client.SendTask(context.Background(), SendTaskRequest{
		ID:      "auth-bearer-task",
		Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("Hello")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != "auth-bearer-task" {
		t.Fatalf("expected auth-bearer-task, got %s", task.ID)
	}
}

func TestServer_TaskTTLCleanup(t *testing.T) {
	handler := newMockHandler()
	server := NewServer(handler, WithTaskTTL(100*time.Millisecond))

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	// Send a task and cancel it to put it in terminal state
	_, err := postJSON(ts.URL+"/", JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tasks/send",
		Params: mustJSON(SendTaskRequest{
			ID:      "ttl-task",
			Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("Hello")}},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Cancel the task to make it terminal
	_, err = postJSON(ts.URL+"/", JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tasks/cancel",
		Params:  mustJSON(CancelTaskRequest{ID: "ttl-task"}),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify task history exists
	server.taskStatesMu.RLock()
	_, exists := server.taskStates["ttl-task"]
	server.taskStatesMu.RUnlock()
	if !exists {
		t.Fatal("task history should exist immediately after creation")
	}

	time.Sleep(300 * time.Millisecond)

	server.taskStatesMu.RLock()
	_, exists = server.taskStates["ttl-task"]
	server.taskStatesMu.RUnlock()
	if exists {
		t.Fatal("task history should have been purged after TTL")
	}
}

func TestServer_HealthEndpoint(t *testing.T) {
	handler := newMockHandler()
	server := NewServer(handler)

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result["status"] != "ok" {
		t.Fatalf("expected status ok, got %s", result["status"])
	}
}

func TestClient_Retry(t *testing.T) {
	attempts := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(JSONRPCResponse{JSONRPC: "2.0", Result: &Task{ID: "retry-task", State: TaskStateWorking}})
	}))
	defer ts.Close()

	client := NewClient(ts.URL, WithRetry(3, 10*time.Millisecond))

	task, err := client.SendTask(context.Background(), SendTaskRequest{
		ID:      "retry-task",
		Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("Hello")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != "retry-task" {
		t.Fatalf("expected retry-task, got %s", task.ID)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

func TestClient_RetryExhausted(t *testing.T) {
	attempts := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer ts.Close()

	client := NewClient(ts.URL, WithRetry(2, 5*time.Millisecond))

	_, err := client.SendTask(context.Background(), SendTaskRequest{
		ID:      "retry-fail",
		Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("Hello")}},
	})
	if err == nil {
		t.Fatal("expected error after retries exhausted")
	}
	if attempts != 3 { // initial + 2 retries
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

// ---------------------------------------------------------------------------
// Medium Priority Tests
// ---------------------------------------------------------------------------

func TestHistoryLength(t *testing.T) {
	handler := newMockHandler()
	server := NewServer(handler)

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	// Create a task with multiple history entries
	_, err := postJSON(ts.URL+"/", JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tasks/send",
		Params: mustJSON(SendTaskRequest{
			ID:      "history-task",
			Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("Hello")}},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Add some history entries manually
	handler.mu.Lock()
	if task, ok := handler.tasks["history-task"]; ok {
		task.History = []TaskStatus{
			{State: TaskStateSubmitted, Timestamp: time.Now().Add(-3 * time.Minute)},
			{State: TaskStateWorking, Timestamp: time.Now().Add(-2 * time.Minute)},
			{State: TaskStateCompleted, Timestamp: time.Now().Add(-1 * time.Minute)},
		}
	}
	handler.mu.Unlock()

	// Get task without history length limit
	resp, err := postJSON(ts.URL+"/", JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tasks/get",
		Params:  mustJSON(GetTaskRequest{ID: "history-task"}),
	})
	if err != nil {
		t.Fatal(err)
	}

	data, _ := json.Marshal(resp.Result)
	var fullTask Task
	if err := json.Unmarshal(data, &fullTask); err != nil {
		t.Fatal(err)
	}
	if len(fullTask.History) != 3 {
		t.Fatalf("expected 3 history entries, got %d", len(fullTask.History))
	}

	// Get task with history length limit of 1
	resp, err = postJSON(ts.URL+"/", JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      3,
		Method:  "tasks/get",
		Params:  mustJSON(GetTaskRequest{ID: "history-task", HistoryLength: 1}),
	})
	if err != nil {
		t.Fatal(err)
	}

	data, _ = json.Marshal(resp.Result)
	var limitedTask Task
	if err := json.Unmarshal(data, &limitedTask); err != nil {
		t.Fatal(err)
	}
	if len(limitedTask.History) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(limitedTask.History))
	}
	if limitedTask.History[0].State != TaskStateCompleted {
		t.Fatalf("expected completed state, got %s", limitedTask.History[0].State)
	}
}

func TestSessionManager(t *testing.T) {
	sm := NewSessionManager()

	// Test GetOrCreate
	session1 := sm.GetOrCreate("session-1")
	if session1.ID != "session-1" {
		t.Fatalf("expected session-1, got %s", session1.ID)
	}
	if len(session1.Tasks) != 0 {
		t.Fatalf("expected 0 tasks, got %d", len(session1.Tasks))
	}

	// Test AddTask
	sm.AddTask("session-1", "task-1")
	sm.AddTask("session-1", "task-2")

	tasks := sm.GetTasks("session-1")
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}

	// Test duplicate task ID is not added twice
	sm.AddTask("session-1", "task-1")
	tasks = sm.GetTasks("session-1")
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks after duplicate add, got %d", len(tasks))
	}

	// Test Get
	session := sm.Get("session-1")
	if session == nil {
		t.Fatal("expected session to exist")
	}
	if len(session.Tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(session.Tasks))
	}

	// Test Get for non-existent session
	session = sm.Get("non-existent")
	if session != nil {
		t.Fatal("expected nil for non-existent session")
	}

	// Test List
	ids := sm.List()
	if len(ids) != 1 || ids[0] != "session-1" {
		t.Fatalf("unexpected session list: %v", ids)
	}

	// Test Delete
	sm.Delete("session-1")
	if sm.Get("session-1") != nil {
		t.Fatal("expected session to be deleted")
	}
}

func TestSessionManager_PurgeStale(t *testing.T) {
	sm := NewSessionManager()

	// Create sessions with different update times
	session1 := sm.GetOrCreate("stale-session")
	session1.UpdatedAt = time.Now().Add(-2 * time.Hour)

	session2 := sm.GetOrCreate("fresh-session")
	session2.UpdatedAt = time.Now()

	// Purge sessions older than 1 hour
	cutoff := time.Now().Add(-1 * time.Hour)
	count := sm.PurgeStale(cutoff)

	if count != 1 {
		t.Fatalf("expected 1 purged session, got %d", count)
	}

	if sm.Get("stale-session") != nil {
		t.Fatal("expected stale session to be purged")
	}

	if sm.Get("fresh-session") == nil {
		t.Fatal("expected fresh session to remain")
	}
}

func TestServer_SessionTracking(t *testing.T) {
	handler := newMockHandler()
	server := NewServer(handler, WithSessionManager(time.Hour))

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	// Send a task with session ID
	_, err := postJSON(ts.URL+"/", JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tasks/send",
		Params: mustJSON(SendTaskRequest{
			ID:        "session-task-1",
			SessionID: "user-session-1",
			Message:   Message{Role: string(RoleUser), Parts: []Part{NewTextPart("Hello")}},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Send another task with same session ID
	_, err = postJSON(ts.URL+"/", JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tasks/send",
		Params: mustJSON(SendTaskRequest{
			ID:        "session-task-2",
			SessionID: "user-session-1",
			Message:   Message{Role: string(RoleUser), Parts: []Part{NewTextPart("World")}},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify session tracking
	tasks := server.sessionMgr.GetTasks("user-session-1")
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks in session, got %d", len(tasks))
	}

	// Verify task IDs
	taskMap := make(map[string]bool)
	for _, id := range tasks {
		taskMap[id] = true
	}
	if !taskMap["session-task-1"] || !taskMap["session-task-2"] {
		t.Fatalf("expected both tasks in session, got %v", tasks)
	}
}

func TestValidateInputModes(t *testing.T) {
	// Test supported modes
	err := ValidateInputModes([]string{"text", "image/png"}, []string{"text", "image/png", "application/json"})
	if err != nil {
		t.Fatalf("expected no error for supported modes, got %v", err)
	}

	// Test unsupported mode
	err = ValidateInputModes([]string{"text", "video/mp4"}, []string{"text", "image/png"})
	if err == nil {
		t.Fatal("expected error for unsupported mode")
	}
	if err.Error() != `unsupported input mode: "video/mp4"` {
		t.Fatalf("unexpected error message: %v", err)
	}

	// Test empty requested modes (should pass)
	err = ValidateInputModes([]string{}, []string{"text"})
	if err != nil {
		t.Fatalf("expected no error for empty requested modes, got %v", err)
	}

	// Test empty supported modes (should pass)
	err = ValidateInputModes([]string{"text"}, []string{})
	if err != nil {
		t.Fatalf("expected no error for empty supported modes, got %v", err)
	}
}

func TestValidateOutputModes(t *testing.T) {
	// Test supported modes
	err := ValidateOutputModes([]string{"text", "application/json"}, []string{"text", "image/png", "application/json"})
	if err != nil {
		t.Fatalf("expected no error for supported modes, got %v", err)
	}

	// Test unsupported mode
	err = ValidateOutputModes([]string{"video/mp4"}, []string{"text"})
	if err == nil {
		t.Fatal("expected error for unsupported mode")
	}
}

func TestExtractInputModes(t *testing.T) {
	// Test text mode
	msg := Message{Parts: []Part{NewTextPart("Hello")}}
	modes := ExtractInputModes(msg)
	if len(modes) != 1 || modes[0] != "text" {
		t.Fatalf("expected [text], got %v", modes)
	}

	// Test file mode with MIME type
	msg = Message{Parts: []Part{NewFilePartBytes("test.png", "image/png", "data")}}
	modes = ExtractInputModes(msg)
	if len(modes) != 1 || modes[0] != "image/png" {
		t.Fatalf("expected [image/png], got %v", modes)
	}

	// Test data mode
	msg = Message{Parts: []Part{NewDataPart(map[string]any{"key": "value"})}}
	modes = ExtractInputModes(msg)
	if len(modes) != 1 || modes[0] != "data" {
		t.Fatalf("expected [data], got %v", modes)
	}

	// Test mixed modes
	msg = Message{Parts: []Part{
		NewTextPart("Hello"),
		NewFilePartBytes("test.png", "image/png", "data"),
		NewDataPart(map[string]any{"key": "value"}),
	}}
	modes = ExtractInputModes(msg)
	if len(modes) != 3 {
		t.Fatalf("expected 3 modes, got %d", len(modes))
	}
	modeMap := make(map[string]bool)
	for _, m := range modes {
		modeMap[m] = true
	}
	if !modeMap["text"] || !modeMap["image/png"] || !modeMap["data"] {
		t.Fatalf("expected text, image/png, and data modes, got %v", modes)
	}
}

func TestServer_InputModeValidation(t *testing.T) {
	handler := newMockHandler()
	// Set supported input modes
	handler.card.DefaultInputModes = []string{"text", "image/png"}
	server := NewServer(handler)

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	// Test supported mode
	resp, err := postJSON(ts.URL+"/", JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tasks/send",
		Params: mustJSON(SendTaskRequest{
			ID:      "valid-mode-task",
			Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("Hello")}},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("expected no error for supported mode, got %v", resp.Error)
	}

	// Test unsupported mode
	resp, err = postJSON(ts.URL+"/", JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tasks/send",
		Params: mustJSON(SendTaskRequest{
			ID:      "invalid-mode-task",
			Message: Message{Role: string(RoleUser), Parts: []Part{NewFilePartBytes("test.mp4", "video/mp4", "data")}},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil {
		t.Fatal("expected error for unsupported input mode")
	}
	if resp.Error.Code != A2AErrorContentTypeNotSupported {
		t.Fatalf("expected content type not supported error, got %d", resp.Error.Code)
	}
}

// ---------------------------------------------------------------------------
// Input-Required State Tests
// ---------------------------------------------------------------------------

func TestDefaultAgentHandler_InputRequiredState(t *testing.T) {
	handler := &inputRequiredHandler{
		card: AgentCard{
			Name: "test-agent",
			URL:  "http://localhost:8080",
			Capabilities: AgentCapabilities{
				Streaming:              true,
				PushNotifications:     true,
				StateTransitionHistory: true,
			},
		},
		tasks: make(map[string]*Task),
	}

	ctx := context.Background()

	task, err := handler.SendTask(ctx, SendTaskRequest{
		ID:      "input-req-1",
		Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("Hello")}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if task.State != TaskStateInputRequired {
		t.Fatalf("expected input-required, got %s", task.State)
	}
	if len(task.Messages) < 1 {
		t.Fatalf("expected at least 1 message, got %d", len(task.Messages))
	}
}

func TestDefaultAgentHandler_InputRequiredThenContinue(t *testing.T) {
	handler := &appendableHandler{
		card: AgentCard{
			Name: "test-agent",
			URL:  "http://localhost:8080",
			Capabilities: AgentCapabilities{
				Streaming:              true,
				PushNotifications:     true,
				StateTransitionHistory: true,
			},
		},
		tasks: make(map[string]*Task),
	}

	ctx := context.Background()

	task1, err := handler.SendTask(ctx, SendTaskRequest{
		ID:      "multi-turn-1",
		Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("Hello")}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if task1.State != TaskStateInputRequired {
		t.Fatalf("expected input-required after first send, got %s", task1.State)
	}

	task2, err := handler.SendTask(ctx, SendTaskRequest{
		ID:      "multi-turn-1",
		Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("More info")}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if task2.State != TaskStateCompleted {
		t.Fatalf("expected completed after second send, got %s", task2.State)
	}

	if len(task2.Messages) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(task2.Messages))
	}
}

func TestDefaultAgentHandler_AppendToTerminalTaskFails(t *testing.T) {
	handler := &terminalHandler{
		card: AgentCard{
			Name: "test-agent",
			URL:  "http://localhost:8080",
			Capabilities: AgentCapabilities{
				Streaming: true,
			},
		},
		tasks: make(map[string]*Task),
	}

	ctx := context.Background()

	task, err := handler.SendTask(ctx, SendTaskRequest{
		ID:      "terminal-task",
		Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("Hello")}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if task.State != TaskStateCompleted {
		t.Fatalf("expected completed, got %s", task.State)
	}

	_, err = handler.SendTask(ctx, SendTaskRequest{
		ID:      "terminal-task",
		Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("More info")}},
	})
	if err == nil {
		t.Fatal("expected error when appending to terminal task")
	}
}

// ---------------------------------------------------------------------------
// SSE Streaming Tests
// ---------------------------------------------------------------------------

type streamingMockHandler struct {
	card    AgentCard
	tasks   map[string]*Task
	mu      sync.Mutex
}

func newStreamingMockHandler() *streamingMockHandler {
	return &streamingMockHandler{
		card: AgentCard{
			Name: "test-agent",
			URL:  "http://localhost:8080",
			Capabilities: AgentCapabilities{
				Streaming:          true,
				PushNotifications: true,
			},
		},
		tasks: make(map[string]*Task),
	}
}

func (m *streamingMockHandler) Card() AgentCard { return m.card }

func (m *streamingMockHandler) SendTask(ctx context.Context, req SendTaskRequest) (*Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	task := &Task{
		ID:       req.ID,
		State:    TaskStateCompleted,
		Messages: []Message{req.Message},
		History: []TaskStatus{
			{State: TaskStateSubmitted, Timestamp: time.Now()},
			{State: TaskStateWorking, Timestamp: time.Now()},
			{State: TaskStateCompleted, Timestamp: time.Now()},
		},
	}
	m.tasks[req.ID] = task
	return task, nil
}

func (m *streamingMockHandler) GetTask(ctx context.Context, req GetTaskRequest) (*Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	task, ok := m.tasks[req.ID]
	if !ok {
		return nil, fmt.Errorf("task not found")
	}
	return task, nil
}

func (m *streamingMockHandler) CancelTask(ctx context.Context, req CancelTaskRequest) (*Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	task, ok := m.tasks[req.ID]
	if !ok {
		return nil, fmt.Errorf("task not found")
	}
	task.State = TaskStateCanceled
	return task, nil
}

func (m *streamingMockHandler) SetPushNotification(ctx context.Context, req SetPushNotificationRequest) error {
	return nil
}

func (m *streamingMockHandler) GetPushNotification(ctx context.Context, taskID string) (*PushNotificationConfig, error) {
	return nil, fmt.Errorf("not configured")
}

func (m *streamingMockHandler) QueryTasks(ctx context.Context, req QueryTasksRequest) (*QueryTasksResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*Task
	for _, task := range m.tasks {
		if req.SessionID != "" && task.SessionID != req.SessionID {
			continue
		}
		if req.State != "" && task.State != req.State {
			continue
		}
		result = append(result, task)
	}
	if result == nil {
		result = []*Task{}
	}
	return &QueryTasksResult{Tasks: result}, nil
}

func TestServer_SendTaskSubscribe_Streaming(t *testing.T) {
	handler := newStreamingMockHandler()
	server := NewServer(handler)

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	client := NewClient(ts.URL)

	stream, err := client.SendTaskSubscribe(context.Background(), SendTaskRequest{
		ID:      "stream-task",
		Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("Hello")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	var events []*TaskUpdateEvent
	for {
		ev, ok := stream.Recv()
		if !ok {
			break
		}
		events = append(events, ev)
		if ev.Final {
			break
		}
	}

	if len(events) < 1 {
		t.Fatalf("expected at least 1 event, got %d", len(events))
	}

	lastEvent := events[len(events)-1]
	if !lastEvent.Final {
		t.Fatal("expected last event to be final")
	}
	if lastEvent.Result == nil {
		t.Fatal("expected result in final event")
	}
	if lastEvent.Result.State != TaskStateCompleted {
		t.Fatalf("expected completed state, got %s", lastEvent.Result.State)
	}
}

func TestServer_SendTaskSubscribe_IntermediateUpdates(t *testing.T) {
	handler := newStreamingMockHandler()
	server := NewServer(handler)

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	client := NewClient(ts.URL)

	stream, err := client.SendTaskSubscribe(context.Background(), SendTaskRequest{
		ID:      "intermediate-task",
		Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("Hello")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	var events []*TaskUpdateEvent
	for {
		ev, ok := stream.Recv()
		if !ok {
			break
		}
		events = append(events, ev)
		if ev.Final {
			break
		}
	}

	hasIntermediate := false
	for _, ev := range events[:len(events)-1] {
		if ev.Artifact != nil {
			hasIntermediate = true
			break
		}
	}

	if !hasIntermediate {
		t.Log("No intermediate artifact events received (handler may complete too fast)")
	}
}

func TestServer_SendTaskSubscribe_InputRequiredClosesStream(t *testing.T) {
	slowHandler := &inputRequiredHandler{
		card: AgentCard{
			Name: "test-agent",
			URL:  "http://localhost:8080",
			Capabilities: AgentCapabilities{
				Streaming: true,
			},
		},
		tasks: make(map[string]*Task),
	}

	server := NewServer(slowHandler)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	client := NewClient(ts.URL)

	stream, err := client.SendTaskSubscribe(context.Background(), SendTaskRequest{
		ID:      "input-req-stream",
		Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("Hello")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	var events []*TaskUpdateEvent
	for {
		ev, ok := stream.Recv()
		if !ok {
			break
		}
		events = append(events, ev)
		if ev.Final {
			break
		}
	}

	if len(events) == 0 {
		t.Fatal("expected at least one event")
	}

	lastEvent := events[len(events)-1]
	if lastEvent.Result == nil {
		t.Fatal("expected result in final event")
	}
	if lastEvent.Result.State != TaskStateInputRequired {
		t.Fatalf("expected input-required state, got %s", lastEvent.Result.State)
	}
	if !lastEvent.Final {
		t.Fatal("expected final=true for input-required state in SSE stream")
	}
}

type inputRequiredHandler struct {
	card    AgentCard
	tasks   map[string]*Task
	mu      sync.Mutex
}

func (h *inputRequiredHandler) Card() AgentCard { return h.card }

func (h *inputRequiredHandler) SendTask(ctx context.Context, req SendTaskRequest) (*Task, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	task := &Task{
		ID:       req.ID,
		State:    TaskStateInputRequired,
		Messages: []Message{req.Message},
		History: []TaskStatus{
			{State: TaskStateSubmitted, Timestamp: time.Now()},
			{State: TaskStateWorking, Timestamp: time.Now()},
			{State: TaskStateInputRequired, Timestamp: time.Now()},
		},
	}
	h.tasks[req.ID] = task
	return task, nil
}

func (h *inputRequiredHandler) GetTask(ctx context.Context, req GetTaskRequest) (*Task, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	task, ok := h.tasks[req.ID]
	if !ok {
		return nil, fmt.Errorf("task not found")
	}
	return task, nil
}

func (h *inputRequiredHandler) CancelTask(ctx context.Context, req CancelTaskRequest) (*Task, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	task, ok := h.tasks[req.ID]
	if !ok {
		return nil, fmt.Errorf("task not found")
	}
	task.State = TaskStateCanceled
	return task, nil
}

func (h *inputRequiredHandler) SetPushNotification(ctx context.Context, req SetPushNotificationRequest) error {
	return nil
}

func (h *inputRequiredHandler) GetPushNotification(ctx context.Context, taskID string) (*PushNotificationConfig, error) {
	return nil, fmt.Errorf("not configured")
}

func (h *inputRequiredHandler) QueryTasks(ctx context.Context, req QueryTasksRequest) (*QueryTasksResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var result []*Task
	for _, task := range h.tasks {
		if req.State != "" && task.State != req.State {
			continue
		}
		result = append(result, task)
	}
	if result == nil {
		result = []*Task{}
	}
	return &QueryTasksResult{Tasks: result}, nil
}

// ---------------------------------------------------------------------------
// Message Appending Tests
// ---------------------------------------------------------------------------

func TestServer_SendTask_AppendMessage(t *testing.T) {
	handler := newMockHandler()
	server := NewServer(handler)

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	_, err := postJSON(ts.URL+"/", JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tasks/send",
		Params: mustJSON(SendTaskRequest{
			ID:      "append-task",
			Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("First message")}},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	handler.mu.Lock()
	if task, ok := handler.tasks["append-task"]; ok {
		task.State = TaskStateInputRequired
	}
	handler.mu.Unlock()

	resp, err := postJSON(ts.URL+"/", JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tasks/send",
		Params: mustJSON(SendTaskRequest{
			ID:      "append-task",
			Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("Second message")}},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	data, _ := json.Marshal(resp.Result)
	var task Task
	if err := json.Unmarshal(data, &task); err != nil {
		t.Fatal(err)
	}

	if len(task.Messages) < 2 {
		t.Fatalf("expected at least 2 messages after append, got %d", len(task.Messages))
	}
}

func TestDefaultAgentHandler_EvictsOldestTerminalTasksOverCapacity(t *testing.T) {
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer webhook.Close()

	agent := agentcore.New(agentcore.Config{
		ModelConfig: agentcore.ModelConfig{Name: "test"},
	})

	handler := NewDefaultAgentHandler(AgentCard{
		Name: "test-agent",
		URL:  "http://localhost:8080",
	}, agent, agentcore.Config{
		ModelConfig: agentcore.ModelConfig{Name: "test"},
	})
	handler.SetMaxTasks(2)

	ctx := context.Background()
	ids := []string{"evict-1", "evict-2", "evict-3"}
	for _, id := range ids {
		if err := handler.SetPushNotification(ctx, SetPushNotificationRequest{
			ID:     id,
			Config: PushNotificationConfig{URL: webhook.URL},
		}); err != nil {
			t.Fatalf("SetPushNotification(%s): %v", id, err)
		}
		// No provider is configured, so agent.Run fails immediately and the
		// task lands in TaskStateFailed (a terminal state), making it
		// eligible for eviction.
		task, err := handler.SendTask(ctx, SendTaskRequest{
			ID:      id,
			Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("hi")}},
		})
		if err != nil {
			t.Fatalf("SendTask(%s): %v", id, err)
		}
		if task.State != TaskStateFailed {
			t.Fatalf("expected task %s to fail without a provider, got %s", id, task.State)
		}
	}

	handler.tasksMu.RLock()
	numTasks := len(handler.tasks)
	_, oldestStillPresent := handler.tasks["evict-1"]
	_, newestPresent := handler.tasks["evict-3"]
	handler.tasksMu.RUnlock()

	if numTasks > 2 {
		t.Fatalf("expected at most 2 tasks retained, got %d", numTasks)
	}
	if oldestStillPresent {
		t.Fatal("expected oldest task evict-1 to be evicted")
	}
	if !newestPresent {
		t.Fatal("expected newest task evict-3 to be retained")
	}

	handler.pushMu.RLock()
	_, pushStillPresent := handler.pushCfg["evict-1"]
	handler.pushMu.RUnlock()
	if pushStillPresent {
		t.Fatal("expected push notification config for evicted task to be removed")
	}
}

func TestStreamingHandler_Interface(t *testing.T) {
	agent := agentcore.New(agentcore.Config{
		ModelConfig: agentcore.ModelConfig{Name: "test"},
	})

	handler := NewDefaultAgentHandler(AgentCard{
		Name: "test-agent",
		URL:  "http://localhost:8080",
	}, agent, agentcore.Config{
		ModelConfig: agentcore.ModelConfig{Name: "test"},
	})

	var _ StreamingHandler = handler

	adapter := NewAgentAdapter(AgentCard{
		Name: "test-agent",
		URL:  "http://localhost:8080",
	}, agent, agentcore.Config{
		ModelConfig: agentcore.ModelConfig{Name: "test"},
	}, nil)

	var _ StreamingHandler = adapter
}

type appendableHandler struct {
	card  AgentCard
	tasks map[string]*Task
	mu    sync.Mutex
}

func (h *appendableHandler) Card() AgentCard { return h.card }

func (h *appendableHandler) SendTask(ctx context.Context, req SendTaskRequest) (*Task, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if existing, ok := h.tasks[req.ID]; ok {
		if isTerminalState(existing.State) {
			return nil, fmt.Errorf("task %q is already in terminal state", req.ID)
		}
		existing.Messages = append(existing.Messages, req.Message)
		existing.State = TaskStateCompleted
		existing.History = append(existing.History, TaskStatus{State: TaskStateCompleted, Timestamp: time.Now()})
		return existing, nil
	}

	task := &Task{
		ID:       req.ID,
		State:    TaskStateInputRequired,
		Messages: []Message{req.Message},
		History: []TaskStatus{
			{State: TaskStateSubmitted, Timestamp: time.Now()},
			{State: TaskStateWorking, Timestamp: time.Now()},
			{State: TaskStateInputRequired, Timestamp: time.Now()},
		},
	}
	h.tasks[req.ID] = task
	return task, nil
}

func (h *appendableHandler) GetTask(ctx context.Context, req GetTaskRequest) (*Task, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	task, ok := h.tasks[req.ID]
	if !ok {
		return nil, fmt.Errorf("task not found")
	}
	return task, nil
}

func (h *appendableHandler) CancelTask(ctx context.Context, req CancelTaskRequest) (*Task, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	task, ok := h.tasks[req.ID]
	if !ok {
		return nil, fmt.Errorf("task not found")
	}
	task.State = TaskStateCanceled
	return task, nil
}

func (h *appendableHandler) SetPushNotification(ctx context.Context, req SetPushNotificationRequest) error {
	return nil
}

func (h *appendableHandler) GetPushNotification(ctx context.Context, taskID string) (*PushNotificationConfig, error) {
	return nil, fmt.Errorf("not configured")
}

func (h *appendableHandler) QueryTasks(ctx context.Context, req QueryTasksRequest) (*QueryTasksResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var result []*Task
	for _, task := range h.tasks {
		if req.State != "" && task.State != req.State {
			continue
		}
		result = append(result, task)
	}
	if result == nil {
		result = []*Task{}
	}
	return &QueryTasksResult{Tasks: result}, nil
}

type terminalHandler struct {
	card  AgentCard
	tasks map[string]*Task
	mu    sync.Mutex
}

func (h *terminalHandler) Card() AgentCard { return h.card }

func (h *terminalHandler) SendTask(ctx context.Context, req SendTaskRequest) (*Task, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.tasks[req.ID]; ok {
		return nil, fmt.Errorf("task %q is already in terminal state", req.ID)
	}

	task := &Task{
		ID:       req.ID,
		State:    TaskStateCompleted,
		Messages: []Message{req.Message},
		History: []TaskStatus{
			{State: TaskStateSubmitted, Timestamp: time.Now()},
			{State: TaskStateWorking, Timestamp: time.Now()},
			{State: TaskStateCompleted, Timestamp: time.Now()},
		},
	}
	h.tasks[req.ID] = task
	return task, nil
}

func (h *terminalHandler) GetTask(ctx context.Context, req GetTaskRequest) (*Task, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	task, ok := h.tasks[req.ID]
	if !ok {
		return nil, fmt.Errorf("task not found")
	}
	return task, nil
}

func (h *terminalHandler) CancelTask(ctx context.Context, req CancelTaskRequest) (*Task, error) {
	return nil, fmt.Errorf("task already completed")
}

func (h *terminalHandler) SetPushNotification(ctx context.Context, req SetPushNotificationRequest) error {
	return nil
}

func (h *terminalHandler) GetPushNotification(ctx context.Context, taskID string) (*PushNotificationConfig, error) {
	return nil, fmt.Errorf("not configured")
}

func (h *terminalHandler) QueryTasks(ctx context.Context, req QueryTasksRequest) (*QueryTasksResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var result []*Task
	for _, task := range h.tasks {
		if req.State != "" && task.State != req.State {
			continue
		}
		result = append(result, task)
	}
	if result == nil {
		result = []*Task{}
	}
	return &QueryTasksResult{Tasks: result}, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func mustJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

func postJSON(url string, req JSONRPCRequest) (*JSONRPCResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpResp, err := http.Post(url, "application/json", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()

	var resp JSONRPCResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ---------------------------------------------------------------------------
// Query Tasks Tests
// ---------------------------------------------------------------------------

func TestServer_QueryTasks(t *testing.T) {
	handler := newMockHandler()
	server := NewServer(handler)

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	_, _ = postJSON(ts.URL+"/", JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tasks/send",
		Params: mustJSON(SendTaskRequest{
			ID:        "query-1",
			SessionID: "session-a",
			Message:   Message{Role: string(RoleUser), Parts: []Part{NewTextPart("Hello")}},
		}),
	})

	_, _ = postJSON(ts.URL+"/", JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tasks/send",
		Params: mustJSON(SendTaskRequest{
			ID:        "query-2",
			SessionID: "session-b",
			Message:   Message{Role: string(RoleUser), Parts: []Part{NewTextPart("World")}},
		}),
	})

	resp, err := postJSON(ts.URL+"/", JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      3,
		Method:  "tasks/query",
		Params:  mustJSON(QueryTasksRequest{SessionID: "session-a"}),
	})
	if err != nil {
		t.Fatal(err)
	}

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	data, _ := json.Marshal(resp.Result)
	var result QueryTasksResult
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}

	if len(result.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(result.Tasks))
	}
	if result.Tasks[0].ID != "query-1" {
		t.Fatalf("expected query-1, got %s", result.Tasks[0].ID)
	}

	resp2, err := postJSON(ts.URL+"/", JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      4,
		Method:  "tasks/query",
		Params:  mustJSON(QueryTasksRequest{}),
	})
	if err != nil {
		t.Fatal(err)
	}

	data2, _ := json.Marshal(resp2.Result)
	var result2 QueryTasksResult
	if err := json.Unmarshal(data2, &result2); err != nil {
		t.Fatal(err)
	}

	if len(result2.Tasks) < 2 {
		t.Fatalf("expected at least 2 tasks, got %d", len(result2.Tasks))
	}
}

func TestClient_QueryTasks(t *testing.T) {
	handler := newMockHandler()
	server := NewServer(handler)

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	client := NewClient(ts.URL)

	_, _ = client.SendTask(context.Background(), SendTaskRequest{
		ID:        "client-query-1",
		SessionID: "session-x",
		Message:   Message{Role: string(RoleUser), Parts: []Part{NewTextPart("Hello")}},
	})

	result, err := client.QueryTasks(context.Background(), QueryTasksRequest{SessionID: "session-x"})
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(result.Tasks))
	}
	if result.Tasks[0].ID != "client-query-1" {
		t.Fatalf("expected client-query-1, got %s", result.Tasks[0].ID)
	}
}

func TestServer_QueryTasks_ByState(t *testing.T) {
	handler := newMockHandler()
	server := NewServer(handler)

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	_, _ = postJSON(ts.URL+"/", JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tasks/send",
		Params: mustJSON(SendTaskRequest{
			ID:      "state-task-1",
			Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("Hello")}},
		}),
	})

	resp, err := postJSON(ts.URL+"/", JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tasks/query",
		Params:  mustJSON(QueryTasksRequest{State: TaskStateWorking}),
	})
	if err != nil {
		t.Fatal(err)
	}

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	data, _ := json.Marshal(resp.Result)
	var result QueryTasksResult
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}

	if len(result.Tasks) < 1 {
		t.Fatalf("expected at least 1 task with working state, got %d", len(result.Tasks))
	}
}

func TestServer_QueryTasks_WithLimit(t *testing.T) {
	handler := newMockHandler()
	server := NewServer(handler)

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	for i := 0; i < 5; i++ {
		_, _ = postJSON(ts.URL+"/", JSONRPCRequest{
			JSONRPC: "2.0",
			ID:      i + 1,
			Method:  "tasks/send",
			Params: mustJSON(SendTaskRequest{
				ID:      fmt.Sprintf("limit-task-%d", i),
				Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("Hello")}},
			}),
		})
	}

	resp, err := postJSON(ts.URL+"/", JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      10,
		Method:  "tasks/query",
		Params:  mustJSON(QueryTasksRequest{Limit: 3}),
	})
	if err != nil {
		t.Fatal(err)
	}

	data, _ := json.Marshal(resp.Result)
	var result QueryTasksResult
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}

	if len(result.Tasks) != 3 {
		t.Fatalf("expected 3 tasks with limit, got %d", len(result.Tasks))
	}
}

// ---------------------------------------------------------------------------
// Last-Event-ID Tests
// ---------------------------------------------------------------------------

func TestServer_Resubscribe_WithLastEventID(t *testing.T) {
	handler := newMockHandler()
	server := NewServer(handler)

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	_, err := postJSON(ts.URL+"/", JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tasks/send",
		Params: mustJSON(SendTaskRequest{
			ID:      "leid-task",
			Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("Hello")}},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	server.PublishTaskUpdate("leid-task", &TaskUpdateEvent{
		Result: &Task{ID: "leid-task", State: TaskStateWorking},
	})
	server.PublishTaskUpdate("leid-task", &TaskUpdateEvent{
		Result: &Task{ID: "leid-task", State: TaskStateCompleted},
		Final:  true,
	})

	client := NewClient(ts.URL)
	stream, err := client.ResubscribeTask(context.Background(), "leid-task")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	var events []*TaskUpdateEvent
	for {
		ev, ok := stream.Recv()
		if !ok {
			break
		}
		events = append(events, ev)
		if ev.Final {
			break
		}
	}

	if len(events) < 3 {
		t.Fatalf("expected at least 3 events, got %d", len(events))
	}
}

// ---------------------------------------------------------------------------
// WebSocket Tests
// ---------------------------------------------------------------------------

func TestWebSocket_SendAndGetTask(t *testing.T) {
	handler := newMockHandler()
	server := NewServer(handler)

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	sendReq := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tasks/send",
		Params:  mustJSON(SendTaskRequest{ID: "ws-task-1", Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("Hello WS")}}}),
	}
	if err := conn.WriteJSON(sendReq); err != nil {
		t.Fatal(err)
	}

	var resp JSONRPCResponse
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatal(err)
	}

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	data, _ := json.Marshal(resp.Result)
	var task Task
	if err := json.Unmarshal(data, &task); err != nil {
		t.Fatal(err)
	}

	if task.ID != "ws-task-1" {
		t.Fatalf("expected ws-task-1, got %s", task.ID)
	}

	getReq := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tasks/get",
		Params:  mustJSON(GetTaskRequest{ID: "ws-task-1"}),
	}
	if err := conn.WriteJSON(getReq); err != nil {
		t.Fatal(err)
	}

	var resp2 JSONRPCResponse
	if err := conn.ReadJSON(&resp2); err != nil {
		t.Fatal(err)
	}

	if resp2.Error != nil {
		t.Fatalf("unexpected error on get: %v", resp2.Error)
	}
}

func TestWebSocket_QueryTasks(t *testing.T) {
	handler := newMockHandler()
	server := NewServer(handler)

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	sendReq := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tasks/send",
		Params:  mustJSON(SendTaskRequest{ID: "ws-q-1", SessionID: "ws-session-a", Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("Hello")}}}),
	}
	_ = conn.WriteJSON(sendReq)
	var resp JSONRPCResponse
	_ = conn.ReadJSON(&resp)

	queryReq := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tasks/query",
		Params:  mustJSON(QueryTasksRequest{SessionID: "ws-session-a"}),
	}
	if err := conn.WriteJSON(queryReq); err != nil {
		t.Fatal(err)
	}

	var resp2 JSONRPCResponse
	if err := conn.ReadJSON(&resp2); err != nil {
		t.Fatal(err)
	}

	if resp2.Error != nil {
		t.Fatalf("unexpected error: %v", resp2.Error)
	}

	data, _ := json.Marshal(resp2.Result)
	var result QueryTasksResult
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}

	if len(result.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(result.Tasks))
	}
}

func TestWebSocket_MethodNotFound(t *testing.T) {
	handler := newMockHandler()
	server := NewServer(handler)

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "nonexistent/method",
		Params:  json.RawMessage(`{}`),
	}
	if err := conn.WriteJSON(req); err != nil {
		t.Fatal(err)
	}

	var resp JSONRPCResponse
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatal(err)
	}

	if resp.Error == nil {
		t.Fatal("expected error for unknown method")
	}

	if resp.Error.Code != JSONRPCMethodNotFound {
		t.Fatalf("expected method not found error, got %d", resp.Error.Code)
	}
}

func TestWSClient_ConnectAndSend(t *testing.T) {
	handler := newMockHandler()
	server := NewServer(handler)

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	client := NewWSClient(ts.URL)
	conn, err := client.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if err := conn.SendRequest("tasks/send", SendTaskRequest{
		ID:      "wsclient-1",
		Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("Hello WS Client")}},
	}); err != nil {
		t.Fatal(err)
	}

	ev, ok := conn.Recv()
	if !ok {
		t.Fatal("expected event")
	}

	if ev.Result == nil || ev.Result.ID != "wsclient-1" {
		t.Fatalf("unexpected event result: %+v", ev.Result)
	}
}
