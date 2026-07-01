package providercompat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/provider/chatcompat"
)

// drainStream drains a stream delta channel and returns the concatenated content.
func drainStream(ctx context.Context, ch <-chan agentcore.StreamDelta) (string, error) {
	var content strings.Builder
	for d := range ch {
		content.WriteString(d.Content)
	}
	return content.String(), nil
}

// DeepSeek chat model works like any standard Chat Completions provider.
func TestDeepSeek_ChatCompletes(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"ds",
			"choices":[{"message":{"role":"assistant","content":"hello"}}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
		}`))
	}))
	defer srv.Close()

	p := chatcompat.New(chatcompat.Config{
		APIKey:  "test",
		BaseURL: srv.URL,
		Client:  srv.Client(),
	})
	resp, err := p.Complete(context.Background(), &agentcore.ProviderRequest{
		Model:    "deepseek-chat",
		Messages: []agentcore.Message{{Role: agentcore.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "hello" {
		t.Fatalf("content = %q", resp.Content)
	}
}

// DeepSeek reasoner uses reasoning_content → BlockKindThinking.
func TestDeepSeek_ReasonerReturnsThinkingBlock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"ds_r",
			"choices":[{
				"message":{
					"role":"assistant",
					"content":"final answer",
					"reasoning_content":"let me think step by step"
				}
			}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
		}`))
	}))
	defer srv.Close()

	p := chatcompat.New(chatcompat.Config{
		APIKey:  "test",
		BaseURL: srv.URL,
		Client:  srv.Client(),
	})
	resp, err := p.Complete(context.Background(), &agentcore.ProviderRequest{
		Model:    "deepseek-reasoner",
		Messages: []agentcore.Message{{Role: agentcore.RoleUser, Content: "think"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var hasThinking bool
	for _, bl := range resp.Blocks {
		if bl.Kind == agentcore.BlockKindThinking && strings.Contains(bl.Text, "step by step") {
			hasThinking = true
			break
		}
	}
	if !hasThinking {
		t.Fatalf("no thinking block in response: %+v", resp.Blocks)
	}
}

// DeepSeek reasoner: DisableSystemPrompt strips system messages.
func TestDeepSeek_ReasonerDisablesSystemPrompt(t *testing.T) {
	var gotMessages []any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		gotMessages, _ = body["messages"].([]any)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"ds_r",
			"choices":[{"message":{"role":"assistant","content":"ok"}}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
		}`))
	}))
	defer srv.Close()

	p := chatcompat.New(chatcompat.Config{
		APIKey:              "test",
		BaseURL:             srv.URL,
		Client:              srv.Client(),
		DisableSystemPrompt: true,
	})
	_, err := p.Complete(context.Background(), &agentcore.ProviderRequest{
		Model: "deepseek-reasoner",
		Messages: []agentcore.Message{
			{Role: agentcore.RoleSystem, Content: "you are helpful"},
			{Role: agentcore.RoleUser, Content: "hi"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range gotMessages {
		mm := m.(map[string]any)
		if mm["role"] == "system" {
			t.Fatal("system message should have been stripped")
		}
	}
	if len(gotMessages) != 1 {
		t.Fatalf("expected 1 message (user), got %d", len(gotMessages))
	}
}

// Qwen (Tongyi Qianwen) is Chat Completions-compatible.
func TestQwen_ChatCompletes(t *testing.T) {
	var gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"qwen",
			"choices":[{"message":{"role":"assistant","content":"你好"}}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
		}`))
	}))
	defer srv.Close()

	p := chatcompat.New(chatcompat.Config{
		APIKey:  "sk-test",
		BaseURL: srv.URL,
		Client:  srv.Client(),
	})
	resp, err := p.Complete(context.Background(), &agentcore.ProviderRequest{
		Model:    "qwen-max",
		Messages: []agentcore.Message{{Role: agentcore.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "你好" {
		t.Fatalf("content = %q", resp.Content)
	}
	if gotPath != "/chat/completions" {
		t.Fatalf("path = %q", gotPath)
	}
}

// DeepSeek streaming works and emits thinking blocks.
func TestDeepSeek_StreamReturnsThinkingBlock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"let me think\"}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\" answer\"}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := chatcompat.New(chatcompat.Config{
		APIKey:  "test",
		BaseURL: srv.URL,
		Client:  srv.Client(),
	})
	ch, err := p.Stream(context.Background(), &agentcore.ProviderRequest{
		Model:    "deepseek-reasoner",
		Messages: []agentcore.Message{{Role: agentcore.RoleUser, Content: "think"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	content, err := drainStream(context.Background(), ch)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "answer") {
		t.Fatalf("content = %q, want \"answer\"", content)
	}
}

// Qwen streaming works.
func TestQwen_Stream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"你好\"}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := chatcompat.New(chatcompat.Config{
		APIKey:  "sk-test",
		BaseURL: srv.URL,
		Client:  srv.Client(),
	})
	ch, err := p.Stream(context.Background(), &agentcore.ProviderRequest{
		Model:    "qwen-max",
		Messages: []agentcore.Message{{Role: agentcore.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	content, err := drainStream(context.Background(), ch)
	if err != nil {
		t.Fatal(err)
	}
	if content != "你好" {
		t.Fatalf("content = %q, want \"你好\"", content)
	}
}

// Moonshot (Kimi) is Chat Completions-compatible.
func TestMoonshot_ChatCompletes(t *testing.T) {
	var gotAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"moonshot",
			"choices":[{"message":{"role":"assistant","content":"你好"}}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
		}`))
	}))
	defer srv.Close()

	p := chatcompat.New(chatcompat.Config{
		APIKey:  "sk-moonshot",
		BaseURL: srv.URL,
		Client:  srv.Client(),
	})
	resp, err := p.Complete(context.Background(), &agentcore.ProviderRequest{
		Model:    "moonshot-v1-8k",
		Messages: []agentcore.Message{{Role: agentcore.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "你好" {
		t.Fatalf("content = %q", resp.Content)
	}
	if gotAuth != "Bearer sk-moonshot" {
		t.Fatalf("auth = %q", gotAuth)
	}
}

// Qwen (DashScope) error format: {"code":"...","message":"..."}
func TestQwen_ErrorFormat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"InvalidParameter","message":"invalid model","request_id":"req_123"}`))
	}))
	defer srv.Close()

	p := chatcompat.New(chatcompat.Config{
		APIKey:  "test",
		BaseURL: srv.URL,
		Client:  srv.Client(),
	})
	_, err := p.Complete(context.Background(), &agentcore.ProviderRequest{
		Model:    "qwen-max",
		Messages: []agentcore.Message{{Role: agentcore.RoleUser, Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	// Error should contain the body, not "empty choices".
	if strings.Contains(err.Error(), "empty choices") {
		t.Fatalf("unexpected parse error, expected api error: %v", err)
	}
	if !strings.Contains(err.Error(), "InvalidParameter") {
		t.Fatalf("error should contain vendor error code: %v", err)
	}
}
