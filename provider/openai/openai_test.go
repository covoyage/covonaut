package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
)

func TestProviderComplete_SendsStructuredOutputRequest(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_123",
			"choices":[{"message":{"role":"assistant","content":"{\"answer\":\"ok\"}"}}],
			"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}
		}`))
	}))
	defer srv.Close()

	provider := New(Config{
		APIKey:  "test-key",
		BaseURL: srv.URL,
		Client:  srv.Client(),
	})

	resp, err := provider.Complete(context.Background(), &agentcore.ProviderRequest{
		Model: "gpt-4o-mini",
		Messages: []agentcore.Message{
			{Role: agentcore.RoleUser, Content: "return json"},
		},
		ResponseFormat: &agentcore.ResponseFormat{
			Type: agentcore.ResponseFormatJSONSchema,
			JSONSchema: &agentcore.ResponseFormatJSONSchemaConfig{
				Name: "answer",
				Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"answer": map[string]any{"type": "string"},
					},
					"required": []string{"answer"},
				},
				Strict: true,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.Structured) != `{"answer":"ok"}` {
		t.Fatalf("structured = %s", string(resp.Structured))
	}
	if len(resp.Blocks) != 1 || resp.Blocks[0].Kind != agentcore.BlockKindText {
		t.Fatalf("blocks = %#v", resp.Blocks)
	}

	rf, ok := gotBody["response_format"].(map[string]any)
	if !ok {
		t.Fatalf("missing response_format: %#v", gotBody)
	}
	if rf["type"] != string(agentcore.ResponseFormatJSONSchema) {
		t.Fatalf("type = %#v", rf["type"])
	}
	jsonSchema, ok := rf["json_schema"].(map[string]any)
	if !ok {
		t.Fatalf("missing json_schema: %#v", rf)
	}
	if jsonSchema["name"] != "answer" {
		t.Fatalf("name = %#v", jsonSchema["name"])
	}
}

func TestProviderComplete_SendsImageBlocksAsMultipartContent(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_456",
			"choices":[{"message":{"role":"assistant","content":"looks good"}}],
			"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}
		}`))
	}))
	defer srv.Close()

	provider := New(Config{
		APIKey:  "test-key",
		BaseURL: srv.URL,
		Client:  srv.Client(),
	})

	_, err := provider.Complete(context.Background(), &agentcore.ProviderRequest{
		Model: "gpt-4o-mini",
		Messages: []agentcore.Message{
			{
				Role:    agentcore.RoleUser,
				Content: "describe this",
				Blocks: []agentcore.ContentBlock{
					{Kind: agentcore.BlockKindImage, URL: "https://example.com/cat.png", Detail: "high"},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	msgs, ok := gotBody["messages"].([]any)
	if !ok || len(msgs) != 1 {
		t.Fatalf("messages = %#v", gotBody["messages"])
	}
	msg, ok := msgs[0].(map[string]any)
	if !ok {
		t.Fatalf("message = %#v", msgs[0])
	}
	parts, ok := msg["content"].([]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("content = %#v", msg["content"])
	}
	imagePart, ok := parts[1].(map[string]any)
	if !ok {
		t.Fatalf("image part = %#v", parts[1])
	}
	if imagePart["type"] != "image_url" {
		t.Fatalf("type = %#v", imagePart["type"])
	}
	imageURL, ok := imagePart["image_url"].(map[string]any)
	if !ok {
		t.Fatalf("image_url = %#v", imagePart["image_url"])
	}
	if imageURL["url"] != "https://example.com/cat.png" {
		t.Fatalf("url = %#v", imageURL["url"])
	}
	if imageURL["detail"] != "high" {
		t.Fatalf("detail = %#v", imageURL["detail"])
	}
}

func TestProviderComplete_PreservesToolCallBlocks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_789",
			"choices":[{
				"message":{
					"role":"assistant",
					"content":"",
					"tool_calls":[
						{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"tokyo\"}"}}
					]
				}
			}],
			"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}
		}`))
	}))
	defer srv.Close()

	provider := New(Config{
		APIKey:  "test-key",
		BaseURL: srv.URL,
		Client:  srv.Client(),
	})

	resp, err := provider.Complete(context.Background(), &agentcore.ProviderRequest{
		Model:    "gpt-4o-mini",
		Messages: []agentcore.Message{{Role: agentcore.RoleUser, Content: "call a tool"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Blocks) != 1 {
		t.Fatalf("blocks len = %d", len(resp.Blocks))
	}
	if resp.Blocks[0].Kind != agentcore.BlockKindToolCall {
		t.Fatalf("block kind = %q", resp.Blocks[0].Kind)
	}
	if resp.Blocks[0].ToolCallID != "call_1" || resp.Blocks[0].Name != "lookup" {
		t.Fatalf("block = %#v", resp.Blocks[0])
	}
}

func TestProviderComplete_EndpointPathOverride(t *testing.T) {
	var gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_ep",
			"choices":[{"message":{"role":"assistant","content":"ok"}}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
		}`))
	}))
	defer srv.Close()

	provider := New(Config{
		APIKey:       "test-key",
		BaseURL:      srv.URL,
		EndpointPath: "/v1/chat",
		Client:       srv.Client(),
	})

	_, err := provider.Complete(context.Background(), &agentcore.ProviderRequest{
		Model:    "test-model",
		Messages: []agentcore.Message{{Role: agentcore.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/chat" {
		t.Fatalf("path = %q, want /v1/chat", gotPath)
	}
}

func TestProviderComplete_ExtraHeaders(t *testing.T) {
	var gotCustom string
	var gotAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCustom = r.Header.Get("X-Custom-Header")
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_hdr",
			"choices":[{"message":{"role":"assistant","content":"ok"}}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
		}`))
	}))
	defer srv.Close()

	provider := New(Config{
		APIKey:  "test-key",
		BaseURL: srv.URL,
		ExtraHeaders: map[string]string{
			"X-Custom-Header": "custom-value",
		},
		Client: srv.Client(),
	})

	_, err := provider.Complete(context.Background(), &agentcore.ProviderRequest{
		Model:    "test-model",
		Messages: []agentcore.Message{{Role: agentcore.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotCustom != "custom-value" {
		t.Fatalf("custom header = %q", gotCustom)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("auth = %q", gotAuth)
	}
}

func TestProviderComplete_PrepareMessages(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_pm",
			"choices":[{"message":{"role":"assistant","content":"ok"}}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
		}`))
	}))
	defer srv.Close()

	provider := New(Config{
		APIKey:  "test-key",
		BaseURL: srv.URL,
		PrepareMessages: func(msgs []agentcore.Message) []agentcore.Message {
			out := make([]agentcore.Message, len(msgs))
			copy(out, msgs)
			for i := range out {
				if out[i].Role == agentcore.RoleUser {
					out[i].Content = "[prefix] " + out[i].Content
				}
			}
			return out
		},
		Client: srv.Client(),
	})

	_, err := provider.Complete(context.Background(), &agentcore.ProviderRequest{
		Model:    "test-model",
		Messages: []agentcore.Message{{Role: agentcore.RoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	msgs, ok := gotBody["messages"].([]any)
	if !ok || len(msgs) != 1 {
		t.Fatalf("messages = %#v", gotBody["messages"])
	}
	msg, ok := msgs[0].(map[string]any)
	if !ok {
		t.Fatalf("message = %#v", msgs[0])
	}
	content, ok := msg["content"].(string)
	if !ok {
		t.Fatalf("content = %#v", msg["content"])
	}
	if content != "[prefix] hello" {
		t.Fatalf("content = %q, want [prefix] hello", content)
	}
}

func TestProviderComplete_BuildExtraBody(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_eb",
			"choices":[{"message":{"role":"assistant","content":"ok"}}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
		}`))
	}))
	defer srv.Close()

	provider := New(Config{
		APIKey:  "test-key",
		BaseURL: srv.URL,
		BuildExtraBody: func(req *agentcore.ProviderRequest) map[string]any {
			return map[string]any{
				"reasoning": map[string]any{"effort": "high"},
			}
		},
		Client: srv.Client(),
	})

	_, err := provider.Complete(context.Background(), &agentcore.ProviderRequest{
		Model:    "test-model",
		Messages: []agentcore.Message{{Role: agentcore.RoleUser, Content: "think"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	reasoning, ok := gotBody["reasoning"].(map[string]any)
	if !ok {
		t.Fatalf("missing reasoning: %#v", gotBody)
	}
	if reasoning["effort"] != "high" {
		t.Fatalf("effort = %#v", reasoning["effort"])
	}
}
