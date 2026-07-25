package a2a

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type streamingV1Handler struct{ *mockHandler }

type completingV1Handler struct{ *mockHandler }

func (h *completingV1Handler) SendTask(ctx context.Context, req SendTaskRequest) (*Task, error) {
	task, err := h.mockHandler.SendTask(ctx, req)
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	task.State = TaskStateCompleted
	task.History = append(task.History, TaskStatus{State: TaskStateCompleted, Timestamp: time.Now()})
	h.mu.Unlock()
	return task, nil
}

func (h *streamingV1Handler) SendTask(ctx context.Context, req SendTaskRequest) (*Task, error) {
	task := &Task{ID: req.ID, SessionID: req.SessionID, State: TaskStateWorking, Messages: []Message{req.Message}}
	h.mu.Lock()
	h.tasks[task.ID] = task
	h.mu.Unlock()
	go h.publisher.PublishTaskUpdate(task.ID, &TaskUpdateEvent{Result: &Task{ID: task.ID, SessionID: task.SessionID, State: TaskStateCompleted}, Final: true})
	return task, nil
}

func TestA2AV1SendMessage(t *testing.T) {
	handler := &completingV1Handler{mockHandler: newMockHandler()}
	server := NewServer(handler)
	text := "hello"
	params, _ := json.Marshal(v1SendMessageRequest{Message: v1Message{
		MessageID: "message-1",
		Role:      "ROLE_USER",
		Parts:     []v1Part{{Text: &text}},
	}})
	body, _ := json.Marshal(JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: "SendMessage", Params: params})
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	var response struct {
		Result struct {
			Task v1Task `json:"task"`
		} `json:"result"`
		Error *JSONRPCError `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Error != nil {
		t.Fatalf("unexpected error: %v", response.Error)
	}
	if response.Result.Task.ID == "" || response.Result.Task.ContextID == "" {
		t.Fatalf("server-generated identifiers missing: %+v", response.Result.Task)
	}
	if response.Result.Task.Status.State != "TASK_STATE_COMPLETED" {
		t.Fatalf("state = %q", response.Result.Task.Status.State)
	}
}

func TestA2AV1AgentCard(t *testing.T) {
	server := NewServer(newMockHandler())
	req := httptest.NewRequest(http.MethodGet, "/.well-known/agent-card.json", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"tags":[]`)) {
		t.Fatalf("required empty skill tags omitted: %s", rec.Body.String())
	}

	var card v1AgentCard
	if err := json.Unmarshal(rec.Body.Bytes(), &card); err != nil {
		t.Fatal(err)
	}
	if len(card.SupportedInterfaces) != 2 || card.SupportedInterfaces[0].ProtocolVersion != "1.0" {
		t.Fatalf("unexpected interfaces: %+v", card.SupportedInterfaces)
	}
}

func TestA2AV1SendStreamingMessage(t *testing.T) {
	handler := &streamingV1Handler{mockHandler: newMockHandler()}
	server := NewServer(handler)
	text := "hello"
	params, _ := json.Marshal(v1SendMessageRequest{Message: v1Message{MessageID: "message-1", Role: "ROLE_USER", Parts: []v1Part{{Text: &text}}}})
	body, _ := json.Marshal(JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: "SendStreamingMessage", Params: params})
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("content type = %q", rec.Header().Get("Content-Type"))
	}
	scanner := bufio.NewScanner(bytes.NewReader(rec.Body.Bytes()))
	var payloads [][]byte
	for scanner.Scan() {
		line := scanner.Bytes()
		if bytes.HasPrefix(line, []byte("data: ")) {
			payloads = append(payloads, append([]byte(nil), line[len("data: "):]...))
		}
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		t.Fatal(err)
	}
	if len(payloads) != 2 {
		t.Fatalf("stream payloads = %d, want 2: %s", len(payloads), rec.Body.String())
	}
	var initial struct {
		Result map[string]json.RawMessage `json:"result"`
	}
	var final struct {
		Result map[string]json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(payloads[0], &initial); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(payloads[1], &final); err != nil {
		t.Fatal(err)
	}
	if initial.Result["task"] == nil || final.Result["statusUpdate"] == nil {
		t.Fatalf("unexpected stream envelopes: initial=%s final=%s", payloads[0], payloads[1])
	}
}

func TestA2AVersionNegotiationRejectsUnsupportedVersion(t *testing.T) {
	server := NewServer(newMockHandler())
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"GetTask","params":{"id":"x"}}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("A2A-Version", "9.9")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	var response JSONRPCResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Error == nil || response.Error.Code != A2AErrorVersionNotSupported {
		t.Fatalf("response error = %+v", response.Error)
	}
}

func TestA2AV1ListTasksPaginatesAndOmitsArtifacts(t *testing.T) {
	handler := newMockHandler()
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("task-%d", i)
		handler.tasks[id] = &Task{
			ID:        id,
			State:     TaskStateCompleted,
			Messages:  []Message{{Role: string(RoleUser), Parts: []Part{NewTextPart("hello")}}},
			Artifacts: []Artifact{{Name: "output", Parts: []Part{NewTextPart("result")}}},
			History:   []TaskStatus{{State: TaskStateCompleted, Timestamp: time.Now().Add(time.Duration(i) * time.Second)}},
		}
	}
	server := NewServer(handler)
	params, _ := json.Marshal(v1ListTasksRequest{PageSize: 2, HistoryLength: intPointer(0)})
	body, _ := json.Marshal(JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: "ListTasks", Params: params})
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	var response struct {
		Result struct {
			Tasks         []v1Task `json:"tasks"`
			NextPageToken string   `json:"nextPageToken"`
			PageSize      int      `json:"pageSize"`
			TotalSize     int      `json:"totalSize"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Result.Tasks) != 2 || response.Result.TotalSize != 3 || response.Result.NextPageToken == "" {
		t.Fatalf("unexpected page: %+v", response.Result)
	}
	if len(response.Result.Tasks[0].History) != 0 || len(response.Result.Tasks[0].Artifacts) != 0 {
		t.Fatalf("history/artifacts should be omitted: %+v", response.Result.Tasks[0])
	}
}

func TestA2AV1ListTasksCursorDoesNotRepeatAfterInsertion(t *testing.T) {
	handler := newMockHandler()
	base := time.Now().Add(-time.Minute)
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("cursor-%d", i)
		handler.tasks[id] = &Task{ID: id, State: TaskStateCompleted, History: []TaskStatus{{State: TaskStateCompleted, Timestamp: base.Add(time.Duration(i) * time.Second)}}}
	}
	server := httptest.NewServer(NewServer(handler).Handler())
	defer server.Close()
	client := NewClient(server.URL).V1()
	first, err := client.ListTasks(context.Background(), V1ListTasksRequest{PageSize: 2})
	if err != nil || first.NextPageToken == "" {
		t.Fatalf("first page = (%+v, %v)", first, err)
	}
	handler.tasks["cursor-new"] = &Task{ID: "cursor-new", State: TaskStateCompleted, History: []TaskStatus{{State: TaskStateCompleted, Timestamp: time.Now()}}}
	second, err := client.ListTasks(context.Background(), V1ListTasksRequest{PageSize: 2, PageToken: first.NextPageToken})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, task := range first.Tasks {
		seen[task.ID] = true
	}
	for _, task := range second.Tasks {
		if seen[task.ID] {
			t.Fatalf("task %q repeated across cursor pages", task.ID)
		}
	}
}

func TestA2AV1ListTasksStatusTimestampAfter(t *testing.T) {
	handler := newMockHandler()
	cutoff := time.Now().Add(-time.Minute)
	handler.tasks["old"] = &Task{ID: "old", State: TaskStateCompleted, History: []TaskStatus{{State: TaskStateCompleted, Timestamp: cutoff.Add(-time.Second)}}}
	handler.tasks["new"] = &Task{ID: "new", State: TaskStateCompleted, History: []TaskStatus{{State: TaskStateCompleted, Timestamp: cutoff.Add(time.Second)}}}
	server := httptest.NewServer(NewServer(handler).Handler())
	defer server.Close()
	result, err := NewClient(server.URL).V1().ListTasks(context.Background(), V1ListTasksRequest{
		StatusTimestampAfter: cutoff.Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalSize != 1 || len(result.Tasks) != 1 || result.Tasks[0].ID != "new" {
		t.Fatalf("unexpected filtered tasks: %+v", result)
	}
}

func intPointer(value int) *int { return &value }

func TestA2AV1ClientCoreOperations(t *testing.T) {
	handler := newMockHandler()
	server := httptest.NewServer(NewServer(handler).Handler())
	defer server.Close()
	client := NewClient(server.URL).V1()

	sent, err := client.SendMessage(context.Background(), V1SendMessageRequest{Message: V1Message{
		MessageID: "message-client-1",
		Role:      "ROLE_USER",
		Parts:     []V1Part{NewV1TextPart("hello")},
	}, Configuration: &V1SendConfiguration{ReturnImmediately: true}})
	if err != nil {
		t.Fatal(err)
	}
	if sent.Task == nil || sent.Task.ID == "" {
		t.Fatalf("unexpected send result: %+v", sent)
	}

	got, err := client.GetTask(context.Background(), V1GetTaskRequest{ID: sent.Task.ID})
	if err != nil || got.ID != sent.Task.ID {
		t.Fatalf("GetTask = (%+v, %v)", got, err)
	}

	listed, err := client.ListTasks(context.Background(), V1ListTasksRequest{PageSize: 10})
	if err != nil || listed.TotalSize != 1 {
		t.Fatalf("ListTasks = (%+v, %v)", listed, err)
	}

	cancelled, err := client.CancelTask(context.Background(), sent.Task.ID)
	if err != nil || cancelled.Status.State != "TASK_STATE_CANCELED" {
		t.Fatalf("CancelTask = (%+v, %v)", cancelled, err)
	}
}

func TestA2AV1ClientStreaming(t *testing.T) {
	handler := &streamingV1Handler{mockHandler: newMockHandler()}
	server := httptest.NewServer(NewServer(handler).Handler())
	defer server.Close()
	client := NewClient(server.URL).V1()

	stream, err := client.SendStreamingMessage(context.Background(), V1SendMessageRequest{Message: V1Message{
		MessageID: "stream-client-1",
		Role:      "ROLE_USER",
		Parts:     []V1Part{NewV1TextPart("hello")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	initial, err := stream.Recv()
	if err != nil || initial.Task == nil {
		t.Fatalf("initial Recv = (%+v, %v)", initial, err)
	}
	final, err := stream.Recv()
	if err != nil || final.StatusUpdate == nil || final.StatusUpdate.Status.State != "TASK_STATE_COMPLETED" {
		t.Fatalf("final Recv = (%+v, %v)", final, err)
	}
}

func TestA2AV1PushConfigCRUDAndExtendedCard(t *testing.T) {
	handler := newMockHandler()
	handler.tasks["task-push"] = &Task{ID: "task-push", State: TaskStateWorking}
	extended := V1AgentCard{Name: "extended", Description: "private capabilities", Version: "1.0.0"}
	server := NewServer(handler, WithExtendedAgentCard(extended), WithAuth(AuthConfig{APIKey: "secret"}))

	call := func(method string, params any) JSONRPCResponse {
		t.Helper()
		paramsData, _ := json.Marshal(params)
		body, _ := json.Marshal(JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: method, Params: paramsData})
		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("A2A-Version", "1.0")
		req.Header.Set("X-API-Key", "secret")
		rec := httptest.NewRecorder()
		server.Handler().ServeHTTP(rec, req)
		var response JSONRPCResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response
	}

	created := call("CreateTaskPushNotificationConfig", V1TaskPushNotificationConfig{ID: "config-1", TaskID: "task-push", URL: "https://example.com/hook"})
	if created.Error != nil {
		t.Fatalf("create: %v", created.Error)
	}
	got := call("GetTaskPushNotificationConfig", V1PushConfigRef{TaskID: "task-push", ID: "config-1"})
	if got.Error != nil {
		t.Fatalf("get: %v", got.Error)
	}
	listed := call("ListTaskPushNotificationConfigs", V1ListPushConfigsRequest{TaskID: "task-push"})
	if listed.Error != nil {
		t.Fatalf("list: %v", listed.Error)
	}
	deleted := call("DeleteTaskPushNotificationConfig", V1PushConfigRef{TaskID: "task-push", ID: "config-1"})
	if deleted.Error != nil {
		t.Fatalf("delete: %v", deleted.Error)
	}
	if _, err := handler.GetPushNotification(context.Background(), "task-push"); err == nil {
		t.Fatal("deleted standard config remained active in legacy notifier")
	}
	card := call("GetExtendedAgentCard", map[string]any{})
	if card.Error != nil {
		t.Fatalf("extended card: %v", card.Error)
	}
}

func TestA2AV1PushDeliversEveryConfigWithStandardPayload(t *testing.T) {
	type delivery struct {
		contentType string
		body        []byte
	}
	deliveries := make(chan delivery, 2)
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		deliveries <- delivery{contentType: r.Header.Get("Content-Type"), body: body}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer webhook.Close()

	server := NewServer(newMockHandler(), WithAllowPrivateV1PushTargets(true))
	server.storeV1PushConfig(V1TaskPushNotificationConfig{ID: "one", TaskID: "task", URL: webhook.URL})
	server.storeV1PushConfig(V1TaskPushNotificationConfig{ID: "two", TaskID: "task", URL: webhook.URL})
	server.PublishTaskUpdate("task", &TaskUpdateEvent{Result: &Task{ID: "task", SessionID: "context", State: TaskStateCompleted}, Final: true})

	for i := 0; i < 2; i++ {
		select {
		case got := <-deliveries:
			if got.contentType != "application/a2a+json" {
				t.Fatalf("content type = %q", got.contentType)
			}
			var payload V1StreamResponse
			if err := json.Unmarshal(got.body, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.StatusUpdate == nil || payload.Task != nil || payload.Message != nil || payload.ArtifactUpdate != nil {
				t.Fatalf("invalid StreamResponse payload: %+v", payload)
			}
		case <-time.After(time.Second):
			t.Fatal("missing v1 push delivery")
		}
	}
}

func TestA2AV1EmbeddedPushConfigReceivesTerminalTask(t *testing.T) {
	delivery := make(chan V1StreamResponse, 1)
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload V1StreamResponse
		_ = json.NewDecoder(r.Body).Decode(&payload)
		delivery <- payload
		w.WriteHeader(http.StatusNoContent)
	}))
	defer webhook.Close()

	handler := &completingV1Handler{mockHandler: newMockHandler()}
	server := httptest.NewServer(NewServer(handler, WithAllowPrivateV1PushTargets(true)).Handler())
	defer server.Close()
	client := NewClient(server.URL).V1()
	response, err := client.SendMessage(context.Background(), V1SendMessageRequest{
		Message:       V1Message{MessageID: "embedded-push", Role: "ROLE_USER", Parts: []V1Part{NewV1TextPart("hello")}},
		Configuration: &V1SendConfiguration{TaskPushNotificationConfig: &V1TaskPushNotificationConfig{URL: webhook.URL}},
	})
	if err != nil || response.Task == nil {
		t.Fatalf("SendMessage = (%+v, %v)", response, err)
	}
	select {
	case payload := <-delivery:
		if payload.Task == nil || payload.Task.ID != response.Task.ID {
			t.Fatalf("unexpected push payload: %+v", payload)
		}
	case <-time.After(time.Second):
		t.Fatal("embedded push config received no terminal task")
	}
}

func TestA2AV1ClientOptionalOperations(t *testing.T) {
	handler := newMockHandler()
	handler.tasks["task-client-push"] = &Task{ID: "task-client-push", State: TaskStateWorking}
	server := httptest.NewServer(NewServer(handler, WithExtendedAgentCard(V1AgentCard{
		Name: "extended", Description: "private", Version: "1.0.0",
	}), WithAuth(AuthConfig{BearerToken: "secret"})).Handler())
	defer server.Close()
	client := NewClient(server.URL, WithBearerToken("secret")).V1()

	created, err := client.CreateTaskPushNotificationConfig(context.Background(), V1TaskPushNotificationConfig{
		ID: "client-config", TaskID: "task-client-push", URL: "https://example.com/hook",
	})
	if err != nil || created.ID != "client-config" {
		t.Fatalf("create = (%+v, %v)", created, err)
	}
	ref := V1PushConfigRef{TaskID: "task-client-push", ID: created.ID}
	if _, err := client.GetTaskPushNotificationConfig(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	listed, err := client.ListTaskPushNotificationConfigs(context.Background(), V1ListPushConfigsRequest{TaskID: ref.TaskID})
	if err != nil || len(listed.Configs) != 1 {
		t.Fatalf("list = (%+v, %v)", listed, err)
	}
	if err := client.DeleteTaskPushNotificationConfig(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	card, err := client.GetExtendedAgentCard(context.Background())
	if err != nil || card.Name != "extended" || len(card.SupportedInterfaces) == 0 || len(card.DefaultInputModes) == 0 || len(card.SecuritySchemes) == 0 {
		t.Fatalf("extended card = (%+v, %v)", card, err)
	}
}

func TestA2AV1AgentCardSecurityAndRequiredExtensions(t *testing.T) {
	handler := &completingV1Handler{mockHandler: newMockHandler()}
	server := NewServer(handler,
		WithAuth(AuthConfig{APIKey: "secret", BearerToken: "token"}),
		WithA2AExtensions(V1AgentExtension{URI: "https://example.com/ext/v1", Required: true}),
	)

	cardReq := httptest.NewRequest(http.MethodGet, "/.well-known/agent-card.json", nil)
	cardRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(cardRec, cardReq)
	if cardRec.Code != http.StatusOK {
		t.Fatalf("public Agent Card status = %d", cardRec.Code)
	}
	var card V1AgentCard
	if err := json.Unmarshal(cardRec.Body.Bytes(), &card); err != nil {
		t.Fatal(err)
	}
	if len(card.SecuritySchemes) != 2 || len(card.SecurityRequirements) != 2 || len(card.Capabilities.Extensions) != 1 {
		t.Fatalf("incomplete security/extension metadata: %+v", card)
	}

	text := "hello"
	params, _ := json.Marshal(V1SendMessageRequest{Message: V1Message{MessageID: "extension-message", Role: "ROLE_USER", Parts: []V1Part{{Text: &text}}}})
	body, _ := json.Marshal(JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: "SendMessage", Params: params})
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "secret")
	req.Header.Set("A2A-Version", "1.0")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	var missing JSONRPCResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &missing)
	if missing.Error == nil || missing.Error.Code != A2AErrorExtensionSupportRequired {
		t.Fatalf("missing extension response = %+v", missing)
	}

	req = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "secret")
	req.Header.Set("A2A-Version", "1.0")
	req.Header.Set("A2A-Extensions", "https://example.com/ext/v1")
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	var accepted JSONRPCResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &accepted)
	if accepted.Error != nil {
		t.Fatalf("requested extension rejected: %v", accepted.Error)
	}

	legacyParams, _ := json.Marshal(QueryTasksRequest{})
	legacyBody, _ := json.Marshal(JSONRPCRequest{JSONRPC: "2.0", ID: 2, Method: "tasks/query", Params: legacyParams})
	legacyReq := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(legacyBody))
	legacyReq.Header.Set("Content-Type", "application/json")
	legacyReq.Header.Set("X-API-Key", "secret")
	legacyRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(legacyRec, legacyReq)
	var legacyResponse JSONRPCResponse
	_ = json.Unmarshal(legacyRec.Body.Bytes(), &legacyResponse)
	if legacyResponse.Error != nil {
		t.Fatalf("required v1 extension broke legacy request: %v", legacyResponse.Error)
	}

	client := NewClient("http://unused", WithRequestedA2AExtensions("https://example.com/ext/v1"))
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	client.setV1ExtensionHeader(request)
	if got := request.Header.Get("A2A-Extensions"); got != "https://example.com/ext/v1" {
		t.Fatalf("client extension header = %q", got)
	}
}

func TestA2AV1ClientAgentCard(t *testing.T) {
	server := httptest.NewServer(NewServer(newMockHandler(), WithAuth(AuthConfig{BearerToken: "secret"})).Handler())
	defer server.Close()
	card, err := NewClient(server.URL).V1().GetAgentCard(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if card.Name != "test-agent" || len(card.SupportedInterfaces) != 2 || len(card.SecuritySchemes) != 1 {
		t.Fatalf("unexpected v1 card: %+v", card)
	}
}

func TestLegacyClientConvertsStandardAgentCard(t *testing.T) {
	server := httptest.NewServer(NewServer(newMockHandler(), WithAuth(AuthConfig{BearerToken: "secret"})).Handler())
	defer server.Close()
	card, err := NewClient(server.URL).GetAgentCard(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if card.URL != "http://localhost:8080" || card.Authentication == nil || len(card.Authentication.Schemes) != 1 {
		t.Fatalf("standard card metadata lost in legacy conversion: %+v", card)
	}
}

type directMessageV1Handler struct{ *mockHandler }

func (*directMessageV1Handler) SendMessage(context.Context, V1SendMessageRequest) (*V1SendMessageResponse, error) {
	return &V1SendMessageResponse{Message: &V1Message{
		MessageID: "response-message", Role: "ROLE_AGENT", Parts: []V1Part{NewV1TextPart("direct")},
	}}, nil
}

func TestA2AV1SendMessageDirectResponse(t *testing.T) {
	server := httptest.NewServer(NewServer(&directMessageV1Handler{mockHandler: newMockHandler()}).Handler())
	defer server.Close()
	client := NewClient(server.URL).V1()
	response, err := client.SendMessage(context.Background(), V1SendMessageRequest{Message: V1Message{
		MessageID: "request-message", Role: "ROLE_USER", Parts: []V1Part{NewV1TextPart("hello")},
	}})
	if err != nil || response.Message == nil || response.Task != nil {
		t.Fatalf("direct response = (%+v, %v)", response, err)
	}
}

func TestA2AV1SendMessageReturnImmediately(t *testing.T) {
	handler := &blockingWSHandler{mockHandler: newMockHandler(), release: make(chan struct{})}
	server := httptest.NewServer(NewServer(handler).Handler())
	defer server.Close()
	client := NewClient(server.URL).V1()
	response, err := client.SendMessage(context.Background(), V1SendMessageRequest{
		Message:       V1Message{MessageID: "async-message", Role: "ROLE_USER", Parts: []V1Part{NewV1TextPart("slow")}},
		Configuration: &V1SendConfiguration{ReturnImmediately: true},
	})
	if err != nil {
		close(handler.release)
		t.Fatal(err)
	}
	if response.Task == nil || response.Task.Status.State != "TASK_STATE_SUBMITTED" {
		close(handler.release)
		t.Fatalf("response = %+v", response)
	}
	close(handler.release)
	deadline := time.Now().Add(time.Second)
	for {
		task, getErr := client.GetTask(context.Background(), V1GetTaskRequest{ID: response.Task.ID})
		if getErr == nil && task.ID == response.Task.ID {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("detached task did not become queryable: %v", getErr)
		}
		time.Sleep(time.Millisecond)
	}
}

type cancellationAwareV1Handler struct {
	*mockHandler
	cancelled chan struct{}
}

func (h *cancellationAwareV1Handler) SendTask(ctx context.Context, req SendTaskRequest) (*Task, error) {
	<-ctx.Done()
	close(h.cancelled)
	return nil, ctx.Err()
}

func TestA2AV1DetachedTaskIsCancelledOnShutdown(t *testing.T) {
	handler := &cancellationAwareV1Handler{mockHandler: newMockHandler(), cancelled: make(chan struct{})}
	server := NewServer(handler)
	text := "slow"
	params, _ := json.Marshal(V1SendMessageRequest{
		Message:       V1Message{MessageID: "shutdown-task", Role: "ROLE_USER", Parts: []V1Part{{Text: &text}}},
		Configuration: &V1SendConfiguration{ReturnImmediately: true},
	})
	body, _ := json.Marshal(JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: "SendMessage", Params: params})
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-handler.cancelled:
	default:
		t.Fatal("detached task did not observe server shutdown")
	}
}

type directStreamingV1Handler struct{ *mockHandler }

func (*directStreamingV1Handler) SendStreamingMessage(context.Context, V1SendMessageRequest) (<-chan V1StreamResponse, error) {
	stream := make(chan V1StreamResponse, 1)
	stream <- V1StreamResponse{Message: &V1Message{
		MessageID: "stream-response", Role: "ROLE_AGENT", Parts: []V1Part{NewV1TextPart("direct stream")},
	}}
	close(stream)
	return stream, nil
}

func TestA2AV1DirectMessageStream(t *testing.T) {
	server := httptest.NewServer(NewServer(&directStreamingV1Handler{mockHandler: newMockHandler()}).Handler())
	defer server.Close()
	client := NewClient(server.URL).V1()
	stream, err := client.SendStreamingMessage(context.Background(), V1SendMessageRequest{Message: V1Message{
		MessageID: "stream-request", Role: "ROLE_USER", Parts: []V1Part{NewV1TextPart("hello")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	event, err := stream.Recv()
	if err != nil || event.Message == nil || event.Task != nil {
		t.Fatalf("stream event = (%+v, %v)", event, err)
	}
	if _, err := stream.Recv(); err != io.EOF {
		t.Fatalf("second Recv error = %v, want EOF", err)
	}
}

type terminalNativeV1Handler struct{ *mockHandler }

func (*terminalNativeV1Handler) SendStreamingMessage(context.Context, V1SendMessageRequest) (<-chan V1StreamResponse, error) {
	stream := make(chan V1StreamResponse, 2)
	stream <- V1StreamResponse{Task: &V1Task{ID: "native-task", Status: V1TaskStatus{State: "TASK_STATE_WORKING"}}}
	stream <- V1StreamResponse{StatusUpdate: &V1TaskStatusUpdateEvent{TaskID: "native-task", Status: V1TaskStatus{State: "TASK_STATE_COMPLETED"}}}
	return stream, nil
}

func TestA2AV1NativeStreamClosesOnTerminalStatus(t *testing.T) {
	server := httptest.NewServer(NewServer(&terminalNativeV1Handler{mockHandler: newMockHandler()}).Handler())
	defer server.Close()
	stream, err := NewClient(server.URL).V1().SendStreamingMessage(context.Background(), V1SendMessageRequest{Message: V1Message{
		MessageID: "native-request", Role: "ROLE_USER", Parts: []V1Part{NewV1TextPart("hello")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := stream.Recv(); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != io.EOF {
		t.Fatalf("post-terminal Recv error = %v, want EOF", err)
	}
}

type largeStreamingV1Handler struct{ *mockHandler }

func (*largeStreamingV1Handler) SendStreamingMessage(context.Context, V1SendMessageRequest) (<-chan V1StreamResponse, error) {
	stream := make(chan V1StreamResponse, 1)
	stream <- V1StreamResponse{Message: &V1Message{
		MessageID: "large-response", Role: "ROLE_AGENT", Parts: []V1Part{NewV1TextPart(strings.Repeat("x", 100*1024))},
	}}
	close(stream)
	return stream, nil
}

func TestA2AV1ClientAcceptsLargeStreamEvent(t *testing.T) {
	server := httptest.NewServer(NewServer(&largeStreamingV1Handler{mockHandler: newMockHandler()}).Handler())
	defer server.Close()
	client := NewClient(server.URL).V1()
	stream, err := client.SendStreamingMessage(context.Background(), V1SendMessageRequest{Message: V1Message{
		MessageID: "large-request", Role: "ROLE_USER", Parts: []V1Part{NewV1TextPart("hello")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	event, err := stream.Recv()
	if err != nil || event == nil || event.Message == nil || len(event.Message.Parts) == 0 || event.Message.Parts[0].Text == nil || len(*event.Message.Parts[0].Text) != 100*1024 {
		t.Fatalf("large stream event = (%+v, %v)", event, err)
	}
}

func TestA2AV1PartOneofAndScalarData(t *testing.T) {
	text := "text"
	raw := "AA=="
	if _, err := messageToLegacy(V1Message{MessageID: "bad", Role: "ROLE_USER", Parts: []V1Part{{Text: &text, Raw: &raw}}}); err == nil {
		t.Fatal("expected multiple Part content fields to be rejected")
	}
	message, err := messageToLegacy(V1Message{
		MessageID:        "scalar",
		ContextID:        "context",
		Role:             "ROLE_USER",
		Parts:            []V1Part{NewV1DataPart([]any{"a", 1.0}, "application/json")},
		Extensions:       []string{"https://example.com/ext"},
		ReferenceTaskIDs: []string{"task-ref"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(message.Parts) != 1 || message.Parts[0].Data == nil || message.Parts[0].Data.Value == nil {
		t.Fatal("scalar data was not preserved")
	}
	roundTrip := messageFromLegacy(message, "fallback", "", "")
	if roundTrip.MessageID != "scalar" || len(roundTrip.Extensions) != 1 || len(roundTrip.ReferenceTaskIDs) != 1 {
		t.Fatalf("message metadata lost: %+v", roundTrip)
	}
}

func TestA2AV1NullDataPartRoundTrips(t *testing.T) {
	part := NewV1DataPart(nil, "application/json")
	data, err := json.Marshal(part)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"data":null`)) {
		t.Fatalf("explicit null data omitted: %s", data)
	}
	var decoded V1Part
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, err := messageToLegacy(V1Message{MessageID: "null", Role: "ROLE_USER", Parts: []V1Part{decoded}}); err != nil {
		t.Fatalf("null data part rejected: %v", err)
	}
}

func TestA2AV1ScalarDataSurvivesTaskSnapshot(t *testing.T) {
	message, err := messageToLegacy(V1Message{
		MessageID: "snapshot-data", Role: "ROLE_USER", Parts: []V1Part{NewV1DataPart([]any{"a", 2.0}, "application/json")},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := cloneTask(&Task{ID: "snapshot", Messages: []Message{message}})
	if snapshot == nil || len(snapshot.Messages) != 1 || len(snapshot.Messages[0].Parts) != 1 {
		t.Fatalf("invalid snapshot: %+v", snapshot)
	}
	value, ok := snapshot.Messages[0].Parts[0].Data.Value.([]any)
	if !ok || len(value) != 2 {
		t.Fatalf("scalar data lost in snapshot: %#v", snapshot.Messages[0].Parts[0].Data.Value)
	}
}
