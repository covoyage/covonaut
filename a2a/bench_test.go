package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newBenchClient returns an http.Client tuned for benchmark use with
// connection reuse to avoid port exhaustion.
func newBenchClient() *http.Client {
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 100,
			IdleConnTimeout:     30 * time.Second,
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
		},
	}
}

// ---------------------------------------------------------------------------
// Benchmarks for A2A server and client operations
// ---------------------------------------------------------------------------

func BenchmarkServer_SendTask(b *testing.B) {
	handler := newMockHandler()
	server := NewServer(handler)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	body := mustJSON(JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tasks/send",
		Params: mustJSON(SendTaskRequest{
			ID:      "bench-task",
			Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("benchmark")}},
		}),
	})

	client := newBenchClient()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := client.Post(ts.URL+"/", "application/json", strings.NewReader(string(body)))
		if err != nil {
			b.Fatal(err)
		}
		resp.Body.Close()
	}
}

func BenchmarkServer_GetTask(b *testing.B) {
	handler := newMockHandler()
	server := NewServer(handler)

	// Pre-populate a task
	_, _ = handler.SendTask(context.Background(), SendTaskRequest{
		ID:      "bench-get",
		Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("pre")}},
	})

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	body := mustJSON(JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tasks/get",
		Params:  mustJSON(GetTaskRequest{ID: "bench-get"}),
	})

	client := newBenchClient()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := client.Post(ts.URL+"/", "application/json", strings.NewReader(string(body)))
		if err != nil {
			b.Fatal(err)
		}
		resp.Body.Close()
	}
}

func BenchmarkClient_SendTask(b *testing.B) {
	handler := newMockHandler()
	server := NewServer(handler)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	client := NewClient(ts.URL, WithHTTPClient(newBenchClient()))
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := client.SendTask(ctx, SendTaskRequest{
			ID:      fmt.Sprintf("bench-%d", i),
			Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("benchmark")}},
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkJSONMarshal_Task(b *testing.B) {
	task := &Task{
		ID:    "task-123",
		State: TaskStateCompleted,
		Messages: []Message{
			{Role: string(RoleUser), Parts: []Part{NewTextPart("Hello")}},
			{Role: string(RoleAgent), Parts: []Part{NewTextPart("World")}},
		},
		Artifacts: []Artifact{
			{Name: "output", Parts: []Part{NewTextPart("result")}},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := json.Marshal(task)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkJSONUnmarshal_Task(b *testing.B) {
	data := []byte(`{"id":"task-123","state":"completed","messages":[{"role":"user","parts":[{"type":"text","text":"Hello"}]},{"role":"agent","parts":[{"type":"text","text":"World"}]}],"artifacts":[{"name":"output","parts":[{"type":"text","text":"result"}]}]}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var task Task
		if err := json.Unmarshal(data, &task); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPartHelpers(b *testing.B) {
	b.Run("NewTextPart", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = NewTextPart("hello world")
		}
	})

	b.Run("NewDataPart", func(b *testing.B) {
		data := map[string]any{"key": "value", "num": 42}
		for i := 0; i < b.N; i++ {
			_ = NewDataPart(data)
		}
	})

	b.Run("NewFilePartBytes", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = NewFilePartBytes("file.txt", "text/plain", "base64data")
		}
	})
}

func BenchmarkSSEDecoder(b *testing.B) {
	sseData := []byte("id: 1\nevent: update\ndata: {\"id\":1,\"result\":{\"id\":\"t1\",\"state\":\"working\"}}\n\n")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		decoder := NewSSEDecoder(strings.NewReader(string(sseData)))
		_, err := decoder.Next()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPublishTaskUpdate(b *testing.B) {
	handler := newMockHandler()
	server := NewServer(handler)

	// Create multiple subscribers
	for i := 0; i < 10; i++ {
		ch := make(chan *TaskUpdateEvent, 16)
		ts := server.getTaskState("bench-pub")
		ts.mu.Lock()
		ts.subs = append(ts.subs, ch)
		ts.mu.Unlock()
	}

	ev := &TaskUpdateEvent{
		Result: &Task{ID: "bench-pub", State: TaskStateWorking},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		server.PublishTaskUpdate("bench-pub", ev)
	}
}

func BenchmarkServer_AgentCard(b *testing.B) {
	handler := newMockHandler()
	server := NewServer(handler)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	client := newBenchClient()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := client.Get(ts.URL + "/.well-known/agent.json")
		if err != nil {
			b.Fatal(err)
		}
		resp.Body.Close()
	}
}

func BenchmarkServer_CancelTask(b *testing.B) {
	handler := newMockHandler()
	server := NewServer(handler)

	// Pre-populate a task
	_, _ = handler.SendTask(context.Background(), SendTaskRequest{
		ID:      "bench-cancel",
		Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("pre")}},
	})

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	body := mustJSON(JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tasks/cancel",
		Params:  mustJSON(CancelTaskRequest{ID: "bench-cancel"}),
	})

	client := newBenchClient()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Re-create task each iteration since cancel is terminal
		_, _ = handler.SendTask(context.Background(), SendTaskRequest{
			ID:      "bench-cancel",
			Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("pre")}},
		})

		resp, err := client.Post(ts.URL+"/", "application/json", strings.NewReader(string(body)))
		if err != nil {
			b.Fatal(err)
		}
		resp.Body.Close()
	}
}

// BenchmarkAuthMiddleware measures the overhead of authentication.
func BenchmarkAuthMiddleware(b *testing.B) {
	handler := newMockHandler()
	server := NewServer(handler, WithAuth(AuthConfig{APIKey: "bench-key"}))
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	body := mustJSON(JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tasks/send",
		Params: mustJSON(SendTaskRequest{
			ID:      "bench-auth",
			Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("benchmark")}},
		}),
	})

	client := newBenchClient()
	b.Run("WithAuth", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			req, _ := http.NewRequest(http.MethodPost, ts.URL+"/", strings.NewReader(string(body)))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-API-Key", "bench-key")
			resp, err := client.Do(req)
			if err != nil {
				b.Fatal(err)
			}
			resp.Body.Close()
		}
	})

	// Baseline without auth
	serverNoAuth := NewServer(handler)
	tsNoAuth := httptest.NewServer(serverNoAuth.Handler())
	defer tsNoAuth.Close()

	b.Run("WithoutAuth", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			resp, err := client.Post(tsNoAuth.URL+"/", "application/json", strings.NewReader(string(body)))
			if err != nil {
				b.Fatal(err)
			}
			resp.Body.Close()
		}
	})
}

// BenchmarkConcurrentTasks measures concurrent task handling.
func BenchmarkConcurrentTasks(b *testing.B) {
	handler := newMockHandler()
	server := NewServer(handler)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	client := NewClient(ts.URL)
	ctx := context.Background()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			_, err := client.SendTask(ctx, SendTaskRequest{
				ID:      fmt.Sprintf("concurrent-%d-%d", time.Now().UnixNano(), i),
				Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart("concurrent")}},
			})
			if err != nil {
				b.Fatal(err)
			}
			i++
		}
	})
}
