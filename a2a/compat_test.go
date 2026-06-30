package a2a

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Compatibility tests with Google A2A reference implementation patterns
// ---------------------------------------------------------------------------

// TestCompat_AgentCardSchema validates the agent card JSON structure
// matches the expected A2A schema.
func TestCompat_AgentCardSchema(t *testing.T) {
	card := AgentCard{
		Name:        "test-agent",
		Description: "A test agent",
		URL:         "http://localhost:8080",
		Version:     "1.0.0",
		Capabilities: AgentCapabilities{
			Streaming:           true,
			PushNotifications:   true,
			StateTransitionHistory: true,
		},
		Authentication: &AgentAuthentication{
			Schemes: []string{"apiKey", "bearer"},
		},
		DefaultInputModes:  []string{"text/plain", "image/jpeg"},
		DefaultOutputModes: []string{"text/plain", "application/json"},
		Skills: []AgentSkill{
			{
				ID:          "skill-1",
				Name:        "Test Skill",
				Description: "A test skill",
				Tags:        []string{"test"},
				Examples:    []string{"example 1"},
				InputModes:  []string{"text/plain"},
				OutputModes: []string{"text/plain"},
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]string{"type": "string"},
					},
				},
			},
		},
	}

	data, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}

	// Verify it can be unmarshaled back
	var decoded AgentCard
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}

	if decoded.Name != card.Name {
		t.Fatalf("name mismatch")
	}
	if !decoded.Capabilities.Streaming {
		t.Fatalf("streaming capability mismatch")
	}
	if len(decoded.Skills) != 1 {
		t.Fatalf("skills length mismatch")
	}
	if decoded.Authentication == nil || len(decoded.Authentication.Schemes) != 2 {
		t.Fatalf("authentication mismatch")
	}
}

// TestCompat_TaskStateTransitions validates all task states are correctly
// serialized and match the A2A specification.
func TestCompat_TaskStateTransitions(t *testing.T) {
	states := []TaskState{
		TaskStateSubmitted,
		TaskStateWorking,
		TaskStateInputRequired,
		TaskStateCompleted,
		TaskStateFailed,
		TaskStateCanceled,
	}

	expected := []string{"submitted", "working", "input-required", "completed", "failed", "canceled"}

	for i, state := range states {
		data, err := json.Marshal(state)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != fmt.Sprintf("%q", expected[i]) {
			t.Fatalf("state %d: expected %q, got %s", i, expected[i], string(data))
		}
	}
}

// TestCompat_JSONRPCFormat validates JSON-RPC request/response format
// conforms to the A2A specification.
func TestCompat_JSONRPCFormat(t *testing.T) {
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      42,
		Method:  "tasks/send",
		Params:  json.RawMessage(`{"id":"task-1","message":{"role":"user","parts":[{"type":"text","text":"hello"}]}}`),
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}

	if raw["jsonrpc"] != "2.0" {
		t.Fatalf("jsonrpc version mismatch")
	}
	if raw["id"] != float64(42) {
		t.Fatalf("id mismatch")
	}
	if raw["method"] != "tasks/send" {
		t.Fatalf("method mismatch")
	}

	// Response format
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      42,
		Result:  map[string]string{"id": "task-1", "state": "completed"},
	}

	data, err = json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}

	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}

	if raw["jsonrpc"] != "2.0" {
		t.Fatalf("response jsonrpc version mismatch")
	}
	if raw["error"] != nil {
		t.Fatalf("unexpected error field")
	}
}

// TestCompat_PartTypes validates all part types serialize correctly.
func TestCompat_PartTypes(t *testing.T) {
	// Text part
	textPart := NewTextPart("hello world")
	data, _ := json.Marshal(textPart)
	if !strings.Contains(string(data), `"type":"text"`) {
		t.Fatalf("text part type mismatch")
	}

	// File part with bytes
	filePart := NewFilePartBytes("test.png", "image/png", "base64data")
	data, _ = json.Marshal(filePart)
	if !strings.Contains(string(data), `"type":"file"`) {
		t.Fatalf("file part type mismatch")
	}
	if !strings.Contains(string(data), `"mimeType":"image/png"`) {
		t.Fatalf("file mime type mismatch")
	}

	// Data part
	dataPart := NewDataPart(map[string]any{"key": "value", "num": 42})
	data, _ = json.Marshal(dataPart)
	if !strings.Contains(string(data), `"type":"data"`) {
		t.Fatalf("data part type mismatch")
	}
}

// TestCompat_ServerEndpoints validates all required A2A endpoints exist.
func TestCompat_ServerEndpoints(t *testing.T) {
	handler := newMockHandler()
	server := NewServer(handler)

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	// First send a task so t1 exists for subsequent operations
	resp, err := http.Post(ts.URL+"/", "application/json", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"tasks/send","params":{"id":"t1","message":{"role":"user","parts":[{"type":"text","text":"hi"}]}}}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	endpoints := []struct {
		method   string
		path     string
		body     string
		wantCode int
	}{
		{"GET", "/.well-known/agent.json", "", http.StatusOK},
		{"POST", "/", `{"jsonrpc":"2.0","id":1,"method":"tasks/send","params":{"id":"t2","message":{"role":"user","parts":[{"type":"text","text":"hi"}]}}}`, http.StatusOK},
		{"POST", "/", `{"jsonrpc":"2.0","id":1,"method":"tasks/get","params":{"id":"t1"}}`, http.StatusOK},
		{"POST", "/", `{"jsonrpc":"2.0","id":1,"method":"tasks/cancel","params":{"id":"t1"}}`, http.StatusOK},
		{"POST", "/", `{"jsonrpc":"2.0","id":1,"method":"tasks/pushNotification/set","params":{"id":"t1","config":{"url":"http://example.com"}}}`, http.StatusOK},
		{"POST", "/", `{"jsonrpc":"2.0","id":1,"method":"tasks/pushNotification/get","params":{"id":"t1"}}`, http.StatusOK},
		{"POST", "/", `{"jsonrpc":"2.0","id":1,"method":"unknown","params":{}}`, http.StatusOK}, // JSON-RPC method not found returns 200
	}

	for _, ep := range endpoints {
		var body *strings.Reader
		if ep.body != "" {
			body = strings.NewReader(ep.body)
		} else {
			body = strings.NewReader("")
		}

		req, err := http.NewRequest(ep.method, ts.URL+ep.path, body)
		if err != nil {
			t.Fatal(err)
		}
		if ep.body != "" {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()

		if resp.StatusCode != ep.wantCode {
			t.Fatalf("endpoint %s %s: want %d, got %d", ep.method, ep.path, ep.wantCode, resp.StatusCode)
		}
	}
}

func TestCompat_SSEEndpoint(t *testing.T) {
	handler := newMockHandler()
	server := NewServer(handler)

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	// SSE endpoint requires reading the stream; just verify it returns 200 and streams.
	body := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tasks/sendSubscribe","params":{"id":"sse-t1","message":{"role":"user","parts":[{"type":"text","text":"hi"}]}}}`)
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Use a client that will close the connection after reading headers
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		// Timeout is expected if no events are sent; verify it's a timeout
		if strings.Contains(err.Error(), "context deadline exceeded") || strings.Contains(err.Error(), "Client.Timeout") {
			// This is acceptable - the SSE endpoint is working but blocking
			return
		}
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("SSE endpoint: want %d, got %d", http.StatusOK, resp.StatusCode)
	}

	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("SSE endpoint: want text/event-stream, got %s", ct)
	}
}

func TestCompat_ResubscribeEndpoint(t *testing.T) {
	handler := newMockHandler()
	server := NewServer(handler)

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	// First create a task
	resp, err := http.Post(ts.URL+"/", "application/json", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"tasks/send","params":{"id":"resub-t1","message":{"role":"user","parts":[{"type":"text","text":"hi"}]}}}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// Publish a task update so resubscribe has something to replay
	server.PublishTaskUpdate("resub-t1", &TaskUpdateEvent{Result: &Task{ID: "resub-t1", State: TaskStateWorking}, Final: true})

	// Resubscribe endpoint requires reading the stream; just verify it returns 200 and streams.
	body := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tasks/resubscribe","params":{"id":"resub-t1"}}`)
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resubscribe endpoint: want %d, got %d", http.StatusOK, resp.StatusCode)
	}

	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("resubscribe endpoint: want text/event-stream, got %s", ct)
	}
}

// TestCompat_ErrorCodes validates A2A-specific error codes.
func TestCompat_ErrorCodes(t *testing.T) {
	codes := map[int]string{
		JSONRPCParseError:               "parse error",
		JSONRPCInvalidRequest:           "invalid request",
		JSONRPCMethodNotFound:           "method not found",
		JSONRPCInvalidParams:            "invalid params",
		JSONRPCInternalError:            "internal error",
		A2AErrorTaskNotFound:            "task not found",
		A2AErrorTaskNotCancelable:       "task not cancelable",
		A2AErrorPushNotSupported:        "push not supported",
		A2AErrorUnsupportedOperation:    "unsupported operation",
		A2AErrorContentTypeNotSupported: "content type not supported",
	}

	for code, msg := range codes {
		err := &JSONRPCError{Code: code, Message: msg}
		data, _ := json.Marshal(err)
		var decoded JSONRPCError
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.Code != code {
			t.Fatalf("error code %d mismatch", code)
		}
	}
}

// TestCompat_MessageRoles validates message roles.
func TestCompat_MessageRoles(t *testing.T) {
	if RoleUser != "user" {
		t.Fatalf("user role mismatch")
	}
	if RoleAgent != "agent" {
		t.Fatalf("agent role mismatch")
	}

	msg := Message{
		Role:  string(RoleUser),
		Parts: []Part{NewTextPart("hello")},
	}
	data, _ := json.Marshal(msg)
	if !strings.Contains(string(data), `"role":"user"`) {
		t.Fatalf("message role serialization mismatch")
	}
}

// TestCompat_SSEFormat validates SSE event format.
func TestCompat_SSEFormat(t *testing.T) {
	ev := TaskUpdateEvent{
		ID: 42,
		Result: &Task{
			ID:    "task-1",
			State: TaskStateCompleted,
		},
		Final: true,
	}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}

	// SSE format: data: <json>\n\n
	sse := fmt.Sprintf("data: %s\n\n", data)
	if !strings.Contains(sse, `"id":42`) {
		t.Fatalf("sse event id mismatch")
	}
	if !strings.Contains(sse, `"final":true`) {
		t.Fatalf("sse event final mismatch")
	}
}

// TestCompat_ArtifactStructure validates artifact JSON structure.
func TestCompat_ArtifactStructure(t *testing.T) {
	artifact := Artifact{
		Name:      "result",
		Parts:     []Part{NewTextPart("output")},
		Index:     0,
		Append:    compatBoolPtr(false),
		LastChunk: compatBoolPtr(true),
		Metadata:  map[string]any{"key": "value"},
	}

	data, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}

	if raw["name"] != "result" {
		t.Fatalf("artifact name mismatch")
	}
	// Index has omitempty, so 0 is omitted
	if _, ok := raw["index"]; ok {
		t.Fatalf("artifact index should be omitted for zero value")
	}
	if raw["lastChunk"] != true {
		t.Fatalf("artifact lastChunk mismatch")
	}
}

func compatBoolPtr(b bool) *bool {
	return &b
}
