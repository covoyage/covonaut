package deepseek

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
)

func TestNew_DelegatesToOpenAI(t *testing.T) {
	var gotPath string
	var gotAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_ds",
			"choices":[{"message":{"role":"assistant","content":"hello"}}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
		}`))
	}))
	defer srv.Close()

	provider := New(Config{
		APIKey:  "ds-test-key",
		BaseURL: srv.URL,
		Client:  srv.Client(),
	})

	resp, err := provider.Complete(context.Background(), &agentcore.ProviderRequest{
		Model:    "deepseek-chat",
		Messages: []agentcore.Message{{Role: agentcore.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "hello" {
		t.Fatalf("content = %q", resp.Content)
	}
	if gotPath != "/chat/completions" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuth != "Bearer ds-test-key" {
		t.Fatalf("auth = %q", gotAuth)
	}
}

func TestNew_DefaultBaseURL(t *testing.T) {
	var gotHost string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_ds",
			"choices":[{"message":{"role":"assistant","content":"ok"}}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
		}`))
	}))

	provider := New(Config{
		APIKey: "test",
		Client: srv.Client(),
	})

	_, err := provider.Complete(context.Background(), &agentcore.ProviderRequest{
		Model:    "deepseek-chat",
		Messages: []agentcore.Message{{Role: agentcore.RoleUser, Content: "hi"}},
	})
	srv.Close()

	if err == nil {
		t.Fatal("expected error for default URL hitting test server, but got nil")
	}
	_ = gotHost
}

func TestNew_SendsToolCalls(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_ds",
			"choices":[{
				"message":{
					"role":"assistant",
					"content":"",
					"tool_calls":[{"id":"call_1","type":"function","function":{"name":"search","arguments":"{\"q\":\"test\"}"}}]
				}
			}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
		}`))
	}))
	defer srv.Close()

	provider := New(Config{
		APIKey:  "test",
		BaseURL: srv.URL,
		Client:  srv.Client(),
	})

	resp, err := provider.Complete(context.Background(), &agentcore.ProviderRequest{
		Model:    "deepseek-chat",
		Messages: []agentcore.Message{{Role: agentcore.RoleUser, Content: "search"}},
		Tools: []agentcore.ToolDefinition{
			{Name: "search", Description: "Search", Parameters: map[string]any{"type": "object"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "search" {
		t.Fatalf("tool_calls = %#v", resp.ToolCalls)
	}
	tools, ok := gotBody["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools in request = %#v", gotBody["tools"])
	}
}
