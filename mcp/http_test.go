package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

func TestHTTPClient_ListToolsAndCall(t *testing.T) {
	server, state := newHTTPMCPTestServer(t)
	defer server.Close()

	client, err := NewHTTPClient(context.Background(), HTTPConfig{
		Endpoint:      server.URL,
		ToolPrefix:    "mcp.http.",
		ClientName:    "covonaut-test",
		ClientVersion: "0.0.0",
		Headers: map[string]string{
			"X-Test-Header": "yes",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %#v", tools)
	}

	result, err := client.CallTool(context.Background(), "echo", map[string]any{"text": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if got := formatToolResult(result); got != "echo: hello" {
		t.Fatalf("result = %q", got)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if !state.sawDelete {
		t.Fatal("expected session delete on close")
	}
	if state.initCount != 1 || state.notifyCount != 1 {
		t.Fatalf("init=%d notify=%d", state.initCount, state.notifyCount)
	}
	if state.sessionHeaderCount < 3 {
		t.Fatalf("session header count = %d", state.sessionHeaderCount)
	}
	if state.protocolHeaderCount < 3 {
		t.Fatalf("protocol header count = %d", state.protocolHeaderCount)
	}
}

func TestHTTPClient_ParsesCapabilities(t *testing.T) {
	server, _ := newHTTPMCPTestServer(t)
	defer server.Close()

	client, err := NewHTTPClient(context.Background(), HTTPConfig{Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	if !client.SupportsToolListChanged() {
		t.Fatal("expected tools.listChanged support")
	}
	if !client.SupportsResourceSubscribe() {
		t.Fatal("expected resources.subscribe support")
	}
	if !client.SupportsResourceListChanged() {
		t.Fatal("expected resources.listChanged support")
	}
	if !client.SupportsPromptListChanged() {
		t.Fatal("expected prompts.listChanged support")
	}
}

func TestHTTPClient_SubscribeRequiresCapability(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		method, _ := req["method"].(string)
		switch method {
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set(headerSessionID, "sess-cap")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]any{
					"protocolVersion": protocolVersion,
					"capabilities": map[string]any{
						"resources": map[string]any{},
					},
				},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Fatalf("unexpected method %q", method)
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient(context.Background(), HTTPConfig{Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	err = client.SubscribeResource(context.Background(), "file:///notes/readme.txt")
	if err == nil || !strings.Contains(err.Error(), "does not advertise resources.subscribe") {
		t.Fatalf("err = %v", err)
	}
}

func TestHTTPClient_ListsResourcesAndPrompts(t *testing.T) {
	server, _ := newHTTPMCPTestServer(t)
	defer server.Close()

	client, err := NewHTTPClient(context.Background(), HTTPConfig{Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	resources, err := client.ListResources(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 1 || resources[0].URI != "file:///notes/readme.txt" {
		t.Fatalf("resources = %#v", resources)
	}

	prompts, err := client.ListPrompts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 1 || prompts[0].Name != "review" {
		t.Fatalf("prompts = %#v", prompts)
	}

	prompt, err := client.GetPrompt(context.Background(), "review", map[string]any{"topic": "HTTP"})
	if err != nil {
		t.Fatal(err)
	}
	if len(prompt.Messages) != 1 || prompt.Messages[0].Content.Text != "Review HTTP" {
		t.Fatalf("prompt = %#v", prompt)
	}
}

func TestHTTPExtension_HotReloadsToolsOnListChanged(t *testing.T) {
	server, _ := newHTTPMCPTestServer(t)
	defer server.Close()

	ext, err := NewHTTPExtension(context.Background(), HTTPConfig{
		Name:               "mcp-http-hot-reload",
		Endpoint:           server.URL,
		ToolPrefix:         "mcp.",
		EnableServerStream: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ext.Dispose() }()

	agent := agentcore.New(agentcore.Config{
		ModelConfig: agentcore.ModelConfig{
			Name:     "mcp-http-agent-hot-reload",
			Model:    "stub",
			Provider: &toolCallingProvider{},
		},
		Extensions: []agentcore.Extension{ext},
	})
	defer agent.Close()

	if _, err := ext.Client().CallTool(context.Background(), "echo", map[string]any{"text": "refresh-tools"}); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		names := agent.ToolNames()
		hasEcho := false
		hasReverse := false
		for _, name := range names {
			if name == "mcp.echo" {
				hasEcho = true
			}
			if name == "mcp.reverse" {
				hasReverse = true
			}
		}
		if hasReverse && !hasEcho {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("tool names after hot reload = %#v", agent.ToolNames())
}

func TestHTTPClient_DiscoveryNotificationsRefreshCaches(t *testing.T) {
	updatedCh := make(chan *ReadResourceResult, 1)
	resourceListChangedCh := make(chan struct{}, 1)
	promptListChangedCh := make(chan struct{}, 1)

	server, state := newHTTPMCPTestServer(t)
	defer server.Close()

	client, err := NewHTTPClient(context.Background(), HTTPConfig{
		Endpoint:           server.URL,
		EnableServerStream: true,
		Discovery: DiscoveryConfig{
			ResourceUpdatedHandler: func(ctx context.Context, uri string, result *ReadResourceResult) {
				updatedCh <- result
			},
			ResourcesListChangedHandler: func(ctx context.Context) {
				resourceListChangedCh <- struct{}{}
			},
			PromptsListChangedHandler: func(ctx context.Context) {
				promptListChangedCh <- struct{}{}
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	readResult, err := client.ReadResource(context.Background(), "file:///notes/readme.txt")
	if err != nil {
		t.Fatal(err)
	}
	if got := readResult.Contents[0].Text; got != "hello from resource" {
		t.Fatalf("initial read text = %q", got)
	}
	if _, err := client.ListResources(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListPrompts(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := client.SubscribeResource(context.Background(), "file:///notes/readme.txt"); err != nil {
		t.Fatal(err)
	}

	select {
	case updated := <-updatedCh:
		if updated == nil || len(updated.Contents) != 1 || updated.Contents[0].Text != "hello refreshed" {
			t.Fatalf("updated = %#v", updated)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for resource update")
	}
	select {
	case <-resourceListChangedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for resource list changed")
	}
	select {
	case <-promptListChangedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for prompt list changed")
	}

	resources, err := client.ListResources(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 1 || resources[0].Name != "readme-v2.txt" {
		t.Fatalf("resources = %#v", resources)
	}
	prompts, err := client.ListPrompts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 1 || prompts[0].Name != "review-v2" {
		t.Fatalf("prompts = %#v", prompts)
	}
	if err := client.UnsubscribeResource(context.Background(), "file:///notes/readme.txt"); err != nil {
		t.Fatal(err)
	}

	state.mu.Lock()
	if state.subscribedResources["file:///notes/readme.txt"] {
		state.mu.Unlock()
		t.Fatal("expected resource subscription to be removed on server")
	}
	state.mu.Unlock()
}

func TestHTTPExtension_WorksWithAgent(t *testing.T) {
	server, _ := newHTTPMCPTestServer(t)
	defer server.Close()

	ext, err := NewHTTPExtension(context.Background(), HTTPConfig{
		Name:       "mcp-http-test",
		Endpoint:   server.URL,
		ToolPrefix: "mcp.",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ext.Dispose() }()

	provider := &toolCallingProvider{}
	agent := agentcore.New(agentcore.Config{
		ModelConfig: agentcore.ModelConfig{
			Name:     "mcp-http-agent",
			Model:    "stub",
			Provider: provider,
		},
		Extensions: []agentcore.Extension{ext},
	})

	out, err := agent.Run(context.Background(), "say hello")
	if err != nil {
		t.Fatal(err)
	}
	if out != "done" {
		t.Fatalf("out = %q", out)
	}
	if !provider.seenTool {
		t.Fatal("expected HTTP MCP tool to be exposed to provider")
	}

	msgs := agent.State().Messages()
	if len(msgs) < 3 {
		t.Fatalf("messages len = %d", len(msgs))
	}
	toolMsg := msgs[len(msgs)-2]
	if toolMsg.Role != agentcore.RoleTool || toolMsg.Content != "echo: hello from agent" {
		t.Fatalf("tool message = %#v", toolMsg)
	}
}

func TestHTTPClient_ParsesStreamingResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		method, _ := req["method"].(string)
		if method == "initialize" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]any{
					"protocolVersion": protocolVersion,
					"capabilities":    map[string]any{"tools": map[string]any{}},
				},
			})
			return
		}
		if method == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		id := req["id"]
		_, _ = fmt.Fprintf(w, "id: prime-%v\n", id)
		_, _ = w.Write([]byte("data:\n\n"))
		_, _ = w.Write([]byte("event: message\n"))
		_, _ = w.Write([]byte("data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/message\",\"params\":{\"level\":\"info\"}}\n\n"))
		_, _ = fmt.Fprintf(w, "id: response-%v\n", id)
		_, _ = fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%q,\"result\":{\"tools\":[{\"name\":\"echo\",\"description\":\"Echo back a string\",\"inputSchema\":{\"type\":\"object\"}}]}}\n\n", id)
	}))
	defer server.Close()

	client, err := NewHTTPClient(context.Background(), HTTPConfig{Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %#v", tools)
	}
}

func TestHTTPClient_ResumesStreamingResponseWithLastEventID(t *testing.T) {
	const sessionID = "sess-resume"

	var mu sync.Mutex
	initialRequestID := ""
	getResumeCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			method, _ := req["method"].(string)
			switch method {
			case "initialize":
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set(headerSessionID, sessionID)
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      req["id"],
					"result": map[string]any{
						"protocolVersion": protocolVersion,
						"capabilities":    map[string]any{"tools": map[string]any{}},
					},
				})
				return
			case "notifications/initialized":
				if got := r.Header.Get(headerSessionID); got != sessionID {
					t.Fatalf("initialized session header = %q", got)
				}
				if got := r.Header.Get(headerProtocolVersion); got != protocolVersion {
					t.Fatalf("initialized protocol header = %q", got)
				}
				w.WriteHeader(http.StatusAccepted)
				return
			case "tools/list":
				if got := r.Header.Get(headerSessionID); got != sessionID {
					t.Fatalf("tools/list session header = %q", got)
				}
				if got := r.Header.Get(headerProtocolVersion); got != protocolVersion {
					t.Fatalf("tools/list protocol header = %q", got)
				}
				mu.Lock()
				initialRequestID = fmt.Sprint(req["id"])
				mu.Unlock()

				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("id: resume-1\n"))
				_, _ = w.Write([]byte("retry: 1\n"))
				_, _ = w.Write([]byte("data:\n\n"))
				return
			default:
				t.Fatalf("unexpected POST method %q", method)
			}
		case http.MethodGet:
			if got := r.Header.Get("Accept"); !strings.Contains(got, "text/event-stream") {
				t.Fatalf("resume accept = %q", got)
			}
			if got := r.Header.Get(headerSessionID); got != sessionID {
				t.Fatalf("resume session header = %q", got)
			}
			if got := r.Header.Get(headerProtocolVersion); got != protocolVersion {
				t.Fatalf("resume protocol header = %q", got)
			}
			if got := r.Header.Get(headerLastEventID); got != "resume-1" {
				t.Fatalf("Last-Event-ID = %q", got)
			}
			mu.Lock()
			getResumeCount++
			id := initialRequestID
			mu.Unlock()

			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("event: message\n"))
			_, _ = w.Write([]byte("data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{\"step\":1}}\n\n"))
			_, _ = fmt.Fprintf(w, "id: resume-2\n")
			_, _ = fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%q,\"result\":{\"tools\":[{\"name\":\"echo\",\"description\":\"Echo back a string\",\"inputSchema\":{\"type\":\"object\"}}]}}\n\n", id)
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient(context.Background(), HTTPConfig{Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %#v", tools)
	}
	if getResumeCount != 1 {
		t.Fatalf("resume GET count = %d", getResumeCount)
	}
}

func TestHTTPClient_ReinitializesAfterSession404(t *testing.T) {
	var mu sync.Mutex
	initCount := 0
	notifyCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
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
			mu.Lock()
			initCount++
			sessionID := fmt.Sprintf("sess-%d", initCount)
			mu.Unlock()

			w.Header().Set("Content-Type", "application/json")
			w.Header().Set(headerSessionID, sessionID)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]any{
					"protocolVersion": protocolVersion,
					"capabilities":    map[string]any{"tools": map[string]any{}},
				},
			})
		case "notifications/initialized":
			mu.Lock()
			notifyCount++
			current := fmt.Sprintf("sess-%d", initCount)
			mu.Unlock()
			if got := r.Header.Get(headerSessionID); got != current {
				t.Fatalf("initialized session header = %q want %q", got, current)
			}
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			if got := r.Header.Get(headerProtocolVersion); got != protocolVersion {
				t.Fatalf("tools/list protocol header = %q", got)
			}
			switch r.Header.Get(headerSessionID) {
			case "sess-1":
				w.WriteHeader(http.StatusNotFound)
			case "sess-2":
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
				t.Fatalf("unexpected session header %q", r.Header.Get(headerSessionID))
			}
		default:
			t.Fatalf("unexpected method %q", method)
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient(context.Background(), HTTPConfig{Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %#v", tools)
	}

	mu.Lock()
	defer mu.Unlock()
	if initCount != 2 {
		t.Fatalf("initCount = %d", initCount)
	}
	if notifyCount != 2 {
		t.Fatalf("notifyCount = %d", notifyCount)
	}
}

func TestHTTPClient_Concurrent404sOnlyReinitializeOnce(t *testing.T) {
	const concurrentCalls = 3

	var mu sync.Mutex
	initCount := 0
	notifyCount := 0
	staleHits := 0
	staleReady := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
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
			mu.Lock()
			initCount++
			sessionID := fmt.Sprintf("sess-%d", initCount)
			mu.Unlock()

			w.Header().Set("Content-Type", "application/json")
			w.Header().Set(headerSessionID, sessionID)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]any{
					"protocolVersion": protocolVersion,
					"capabilities":    map[string]any{"tools": map[string]any{}},
				},
			})
		case "notifications/initialized":
			mu.Lock()
			notifyCount++
			current := fmt.Sprintf("sess-%d", initCount)
			mu.Unlock()
			if got := r.Header.Get(headerSessionID); got != current {
				t.Fatalf("initialized session header = %q want %q", got, current)
			}
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			if got := r.Header.Get(headerProtocolVersion); got != protocolVersion {
				t.Fatalf("tools/list protocol header = %q", got)
			}
			switch r.Header.Get(headerSessionID) {
			case "sess-1":
				mu.Lock()
				staleHits++
				if staleHits == concurrentCalls {
					close(staleReady)
				}
				mu.Unlock()
				<-staleReady
				w.WriteHeader(http.StatusNotFound)
			case "sess-2":
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
				t.Fatalf("unexpected session header %q", r.Header.Get(headerSessionID))
			}
		default:
			t.Fatalf("unexpected method %q", method)
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient(context.Background(), HTTPConfig{Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	errCh := make(chan error, concurrentCalls)
	for i := 0; i < concurrentCalls; i++ {
		go func() {
			_, err := client.ListTools(context.Background())
			errCh <- err
		}()
	}
	for i := 0; i < concurrentCalls; i++ {
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if initCount != 2 {
		t.Fatalf("initCount = %d", initCount)
	}
	if notifyCount != 2 {
		t.Fatalf("notifyCount = %d", notifyCount)
	}
}

func TestHTTPClient_CloseCancelsInflightListTools(t *testing.T) {
	listStarted := make(chan struct{})
	listCanceled := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set(headerSessionID, "sess-close")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]any{
					"protocolVersion": protocolVersion,
					"capabilities":    map[string]any{"tools": map[string]any{}},
				},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			close(listStarted)
			<-r.Context().Done()
			close(listCanceled)
		default:
			t.Fatalf("unexpected method %q", method)
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient(context.Background(), HTTPConfig{Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := client.ListTools(context.Background())
		errCh <- err
	}()

	select {
	case <-listStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for tools/list to start")
	}

	if err := client.Close(); err != nil {
		t.Fatal(err)
	}

	select {
	case <-listCanceled:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for request cancellation")
	}
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected inflight request error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for inflight request to return")
	}
}

func TestHTTPClient_ReinitUpdatesCapabilities(t *testing.T) {
	var mu sync.Mutex
	initCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			caps := map[string]any{"tools": map[string]any{"listChanged": true}}
			sessionID := "sess-1"
			if current > 1 {
				caps = map[string]any{"tools": map[string]any{}}
				sessionID = "sess-2"
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set(headerSessionID, sessionID)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]any{
					"protocolVersion": protocolVersion,
					"capabilities":    caps,
				},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			if r.Header.Get(headerSessionID) == "sess-1" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
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
	defer server.Close()

	client, err := NewHTTPClient(context.Background(), HTTPConfig{Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	if !client.SupportsToolListChanged() {
		t.Fatal("expected initial tools.listChanged support")
	}
	if _, err := client.ListTools(context.Background()); err != nil {
		t.Fatal(err)
	}
	if client.SupportsToolListChanged() {
		t.Fatal("expected capabilities to update after reinit")
	}
}

func TestHTTPClient_StreamingResponseIgnoresDuplicateUnmatchedEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set(headerSessionID, "sess-dup")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]any{
					"protocolVersion": protocolVersion,
					"capabilities":    map[string]any{"tools": map[string]any{}},
				},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("data: {\"jsonrpc\":\"2.0\",\"id\":\"other\",\"result\":{\"tools\":[{\"name\":\"ignore\",\"inputSchema\":{\"type\":\"object\"}}]}}\n\n"))
			_, _ = w.Write([]byte(fmt.Sprintf("data: {\"jsonrpc\":\"2.0\",\"id\":%q,\"result\":{\"tools\":[{\"name\":\"echo\",\"inputSchema\":{\"type\":\"object\"}}]}}\n\n", req["id"])))
		default:
			t.Fatalf("unexpected method %q", method)
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient(context.Background(), HTTPConfig{Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %#v", tools)
	}
}

func TestHTTPClient_CallToolWhileServerStreamReconnects(t *testing.T) {
	reconnectStarted := make(chan struct{})
	reconnectRelease := make(chan struct{})
	var getCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
			return
		case http.MethodPost:
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			method, _ := req["method"].(string)
			switch method {
			case "initialize":
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set(headerSessionID, "sess-stream-rpc")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      req["id"],
					"result": map[string]any{
						"protocolVersion": protocolVersion,
						"capabilities":    map[string]any{"tools": map[string]any{}},
					},
				})
			case "notifications/initialized":
				w.WriteHeader(http.StatusAccepted)
			case "tools/call":
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      req["id"],
					"result": map[string]any{
						"content": []map[string]any{
							{"type": "text", "text": "echo: reconnect"},
						},
					},
				})
			default:
				t.Fatalf("unexpected method %q", method)
			}
		case http.MethodGet:
			current := getCount.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			switch current {
			case 1:
				_, _ = w.Write([]byte("id: first\n"))
				_, _ = w.Write([]byte("retry: 1\n"))
				_, _ = w.Write([]byte("data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/ping\"}\n\n"))
			case 2:
				close(reconnectStarted)
				<-reconnectRelease
			default:
			}
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient(context.Background(), HTTPConfig{
		Endpoint:           server.URL,
		EnableServerStream: true,
		NotificationHandler: func(ctx context.Context, method string, params json.RawMessage) error {
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	select {
	case <-reconnectStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for server stream reconnect")
	}
	result, err := client.CallTool(context.Background(), "echo", map[string]any{"text": "reconnect"})
	close(reconnectRelease)
	if err != nil {
		t.Fatal(err)
	}
	if got := formatToolResult(result); got != "echo: reconnect" {
		t.Fatalf("result = %q", got)
	}
}

func TestHTTPClient_ServerStreamErrorEmitsTransportEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
			return
		case http.MethodPost:
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			method, _ := req["method"].(string)
			switch method {
			case "initialize":
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set(headerSessionID, "sess-bad-stream")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      req["id"],
					"result": map[string]any{
						"protocolVersion": protocolVersion,
						"capabilities":    map[string]any{"tools": map[string]any{}},
					},
				})
			case "notifications/initialized":
				w.WriteHeader(http.StatusAccepted)
			default:
				t.Fatalf("unexpected method %q", method)
			}
		case http.MethodGet:
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("data: {not-json}\n\n"))
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient(context.Background(), HTTPConfig{
		Endpoint:           server.URL,
		EnableServerStream: true,
		NotificationHandler: func(ctx context.Context, method string, params json.RawMessage) error {
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	eventCh := make(chan agentcore.Event, 4)
	client.SetEventSink(func(event agentcore.Event) {
		eventCh <- event
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case event := <-eventCh:
			transportEvent, ok := event.(TransportErrorEvent)
			if !ok {
				continue
			}
			if transportEvent.Transport != "http" || transportEvent.Operation != "server_stream" || transportEvent.Reason != ReconnectReasonServerStreamEOF {
				t.Fatalf("transport event = %#v", transportEvent)
			}
			if transportEvent.Message == "" || !transportEvent.Recoverable {
				t.Fatalf("transport payload = %#v", transportEvent)
			}
			return
		case <-time.After(20 * time.Millisecond):
		}
	}
	t.Fatal("timed out waiting for transport error event")
}

func TestHTTPClient_ServerStreamDispatchesNotificationsAndRequests(t *testing.T) {
	notificationCh := make(chan string, 1)
	requestCh := make(chan string, 1)
	responseCh := make(chan map[string]any, 1)
	errCh := make(chan error, 4)

	var mu sync.Mutex
	getCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			method, _ := req["method"].(string)
			switch method {
			case "initialize":
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set(headerSessionID, "sess-stream")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      req["id"],
					"result": map[string]any{
						"protocolVersion": protocolVersion,
						"capabilities":    map[string]any{"tools": map[string]any{}},
					},
				})
			case "notifications/initialized":
				w.WriteHeader(http.StatusAccepted)
			default:
				if got := r.Header.Get(headerSessionID); got != "sess-stream" {
					t.Fatalf("response session header = %q", got)
				}
				if got := r.Header.Get(headerProtocolVersion); got != protocolVersion {
					t.Fatalf("response protocol header = %q", got)
				}
				responseCh <- req
				w.WriteHeader(http.StatusAccepted)
			}
		case http.MethodGet:
			if got := r.Header.Get("Accept"); !strings.Contains(got, "text/event-stream") {
				t.Fatalf("GET accept = %q", got)
			}
			if got := r.Header.Get(headerSessionID); got != "sess-stream" {
				t.Fatalf("GET session header = %q", got)
			}
			if got := r.Header.Get(headerProtocolVersion); got != protocolVersion {
				t.Fatalf("GET protocol header = %q", got)
			}

			mu.Lock()
			getCount++
			currentGet := getCount
			mu.Unlock()

			switch currentGet {
			case 1:
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				if got := r.Header.Get(headerLastEventID); got != "" {
					t.Fatalf("first GET Last-Event-ID = %q", got)
				}
				_, _ = w.Write([]byte("id: s1\n"))
				_, _ = w.Write([]byte("retry: 1\n"))
				_, _ = w.Write([]byte("data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/ping\",\"params\":{\"value\":\"hello\"}}\n\n"))
			case 2:
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				if got := r.Header.Get(headerLastEventID); got != "s1" {
					t.Fatalf("second GET Last-Event-ID = %q", got)
				}
				_, _ = w.Write([]byte("id: s2\n"))
				_, _ = w.Write([]byte("data: {\"jsonrpc\":\"2.0\",\"id\":\"req-1\",\"method\":\"client/ping\",\"params\":{\"value\":\"world\"}}\n\n"))
			default:
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient(context.Background(), HTTPConfig{
		Endpoint:           server.URL,
		EnableServerStream: true,
		NotificationHandler: func(ctx context.Context, method string, params json.RawMessage) error {
			notificationCh <- method + ":" + string(params)
			return nil
		},
		RequestHandler: func(ctx context.Context, method string, params json.RawMessage) (any, error) {
			requestCh <- method + ":" + string(params)
			return map[string]any{"ok": true}, nil
		},
		ErrorHandler: func(ctx context.Context, err error) {
			errCh <- err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	select {
	case got := <-notificationCh:
		if !strings.Contains(got, "notifications/ping") || !strings.Contains(got, "hello") {
			t.Fatalf("notification = %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for notification")
	}

	select {
	case got := <-requestCh:
		if !strings.Contains(got, "client/ping") || !strings.Contains(got, "world") {
			t.Fatalf("request = %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for request")
	}

	select {
	case resp := <-responseCh:
		if got := fmt.Sprint(resp["id"]); got != "req-1" {
			t.Fatalf("response id = %v", resp["id"])
		}
		result, _ := resp["result"].(map[string]any)
		if ok, _ := result["ok"].(bool); !ok {
			t.Fatalf("response result = %#v", resp["result"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for response POST")
	}

	select {
	case err := <-errCh:
		t.Fatalf("unexpected async error: %v", err)
	default:
	}
}

type httpMCPServerState struct {
	mu                  sync.Mutex
	initCount           int
	notifyCount         int
	sessionHeaderCount  int
	protocolHeaderCount int
	sawDelete           bool
	resourceName        string
	resourceText        string
	promptName          string
	subscribedResources map[string]bool
	serverStreamOnce    sync.Once
	toolSetVersion      int
	toolListChanged     bool
}

func newHTTPMCPTestServer(t *testing.T) (*httptest.Server, *httpMCPServerState) {
	t.Helper()
	state := &httpMCPServerState{}
	const sessionID = "sess-123"
	state.resourceName = "readme.txt"
	state.resourceText = "hello from resource"
	state.promptName = "review"
	state.subscribedResources = map[string]bool{}
	state.toolSetVersion = 1

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodDelete:
			if got := r.Header.Get(headerSessionID); got != sessionID {
				t.Fatalf("delete session header = %q", got)
			}
			state.mu.Lock()
			state.sawDelete = true
			state.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			return
		case http.MethodPost:
		case http.MethodGet:
			if got := r.Header.Get("Accept"); !strings.Contains(got, "text/event-stream") {
				t.Fatalf("GET accept = %q", got)
			}
			if got := r.Header.Get(headerSessionID); got != sessionID {
				t.Fatalf("GET session header = %q", got)
			}
			if got := r.Header.Get(headerProtocolVersion); got != protocolVersion {
				t.Fatalf("GET protocol header = %q", got)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			state.mu.Lock()
			hasSubscription := len(state.subscribedResources) > 0
			emitToolListChanged := state.toolListChanged
			if emitToolListChanged {
				state.toolListChanged = false
			}
			state.mu.Unlock()
			if emitToolListChanged {
				_, _ = w.Write([]byte("id: tools-1\n"))
				_, _ = w.Write([]byte("retry: 1\n"))
				_, _ = w.Write([]byte("data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/tools/list_changed\"}\n\n"))
				return
			}
			if hasSubscription {
				state.serverStreamOnce.Do(func() {
					_, _ = w.Write([]byte("id: discovery-1\n"))
					_, _ = w.Write([]byte("retry: 1\n"))
					_, _ = w.Write([]byte("data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/resources/updated\",\"params\":{\"uri\":\"file:///notes/readme.txt\"}}\n\n"))
					_, _ = w.Write([]byte("id: discovery-2\n"))
					_, _ = w.Write([]byte("data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/resources/list_changed\"}\n\n"))
					_, _ = w.Write([]byte("id: discovery-3\n"))
					_, _ = w.Write([]byte("data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/prompts/list_changed\"}\n\n"))
				})
			}
			return
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}

		if got := r.Header.Get("X-Test-Header"); got != "" && got != "yes" {
			t.Fatalf("unexpected X-Test-Header %q", got)
		}

		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		method, _ := req["method"].(string)

		if method != "initialize" {
			if got := r.Header.Get(headerSessionID); got != sessionID {
				t.Fatalf("%s session header = %q", method, got)
			}
			if got := r.Header.Get(headerProtocolVersion); got != protocolVersion {
				t.Fatalf("%s protocol header = %q", method, got)
			}
			state.mu.Lock()
			state.sessionHeaderCount++
			state.protocolHeaderCount++
			state.mu.Unlock()
		}

		switch method {
		case "initialize":
			if !strings.Contains(r.Header.Get("Accept"), "application/json") {
				t.Fatalf("initialize accept = %q", r.Header.Get("Accept"))
			}
			state.mu.Lock()
			state.initCount++
			state.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set(headerSessionID, sessionID)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]any{
					"protocolVersion": protocolVersion,
					"capabilities": map[string]any{
						"tools": map[string]any{"listChanged": true},
						"resources": map[string]any{
							"subscribe":   true,
							"listChanged": true,
						},
						"prompts": map[string]any{"listChanged": true},
					},
					"serverInfo": map[string]any{
						"name":    "fake-http-mcp",
						"version": "1.0.0",
					},
				},
			})
		case "notifications/initialized":
			state.mu.Lock()
			state.notifyCount++
			state.mu.Unlock()
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			state.mu.Lock()
			toolVersion := state.toolSetVersion
			state.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]any{
					"tools": httpHelperTools(toolVersion),
				},
			})
		case "tools/call":
			params, _ := req["params"].(map[string]any)
			toolName, _ := params["name"].(string)
			args, _ := params["arguments"].(map[string]any)
			text, _ := args["text"].(string)
			if toolName == "reverse" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      req["id"],
					"result": map[string]any{
						"content": []map[string]any{
							{"type": "text", "text": reverseString(text)},
						},
					},
				})
				return
			}
			if text == "refresh-tools" {
				state.mu.Lock()
				state.toolSetVersion = 2
				state.toolListChanged = true
				state.mu.Unlock()
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
				return
			}
			if text == "boom" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      req["id"],
					"result": map[string]any{
						"content": []map[string]any{
							{"type": "text", "text": "echo failed"},
						},
						"isError": true,
					},
				})
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]any{
					"content": []map[string]any{
						{"type": "text", "text": "echo: " + text},
					},
				},
			})
		case "resources/list":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			state.mu.Lock()
			resourceName := state.resourceName
			state.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]any{
					"resources": []map[string]any{
						{
							"uri":         "file:///notes/readme.txt",
							"name":        resourceName,
							"description": "Test resource",
							"mimeType":    "text/plain",
						},
					},
				},
			})
		case "resources/read":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			state.mu.Lock()
			resourceText := state.resourceText
			state.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]any{
					"contents": []map[string]any{
						{
							"uri":      "file:///notes/readme.txt",
							"mimeType": "text/plain",
							"text":     resourceText,
						},
					},
				},
			})
		case "resources/templates/list":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]any{
					"resourceTemplates": []map[string]any{
						{
							"uriTemplate": "file:///{path}",
							"name":        "files",
							"description": "File template",
						},
					},
				},
			})
		case "resources/subscribe":
			params, _ := req["params"].(map[string]any)
			uri, _ := params["uri"].(string)
			state.mu.Lock()
			state.subscribedResources[uri] = true
			state.resourceName = "readme-v2.txt"
			state.resourceText = "hello refreshed"
			state.promptName = "review-v2"
			state.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result":  map[string]any{},
			})
		case "resources/unsubscribe":
			params, _ := req["params"].(map[string]any)
			uri, _ := params["uri"].(string)
			state.mu.Lock()
			delete(state.subscribedResources, uri)
			state.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result":  map[string]any{},
			})
		case "prompts/list":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			state.mu.Lock()
			promptName := state.promptName
			state.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]any{
					"prompts": []map[string]any{
						{
							"name":        promptName,
							"description": "Review a topic",
							"arguments": []map[string]any{
								{"name": "topic", "required": true},
							},
						},
					},
				},
			})
		case "prompts/get":
			params, _ := req["params"].(map[string]any)
			args, _ := params["arguments"].(map[string]any)
			topic, _ := args["topic"].(string)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]any{
					"description": "Prompt result",
					"messages": []map[string]any{
						{
							"role": "user",
							"content": map[string]any{
								"type": "text",
								"text": "Review " + topic,
							},
						},
					},
				},
			})
		default:
			t.Fatalf("unexpected method %q", method)
		}
	}))
	return server, state
}

func httpHelperTools(version int) []map[string]any {
	if version == 2 {
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
