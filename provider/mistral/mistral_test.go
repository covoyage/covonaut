package mistral

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
)

func TestNew_DefaultBaseURL(t *testing.T) {
	provider := New(Config{APIKey: "test-key"})
	if provider == nil {
		t.Fatal("expected non-nil provider")
	}
}

func TestNew_CustomBaseURL(t *testing.T) {
	provider := New(Config{
		APIKey:  "test-key",
		BaseURL: "https://custom.mistral.ai/v1",
	})
	if provider == nil {
		t.Fatal("expected non-nil provider")
	}
}

func TestProvider_Complete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["model"] != "mistral-large" {
			t.Fatalf("model = %v", body["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices":[{"message":{"role":"assistant","content":"hello"}}],
			"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}
		}`))
	}))
	defer srv.Close()

	provider := New(Config{
		APIKey:  "test-key",
		BaseURL: srv.URL,
	})

	resp, err := provider.Complete(context.Background(), &agentcore.ProviderRequest{
		Model: "mistral-large",
		Messages: []agentcore.Message{
			{Role: agentcore.RoleUser, Content: "hi"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "hello" {
		t.Fatalf("content = %q", resp.Content)
	}
}

func TestDefaultBaseURL(t *testing.T) {
	if defaultBaseURL != "https://api.mistral.ai/v1" {
		t.Fatalf("unexpected default URL: %s", defaultBaseURL)
	}
}
