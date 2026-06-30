package gemini

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
)

func TestProviderComplete_SendsStructuredOutputConfig(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates":[{"content":{"parts":[{"text":"{\"answer\":\"ok\"}"}]}}],
			"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":3,"totalTokenCount":7}
		}`))
	}))
	defer srv.Close()

	provider := New(Config{
		APIKey:  "test-key",
		BaseURL: srv.URL,
		Model:   "gemini-2.5-flash",
		Client:  srv.Client(),
	})

	resp, err := provider.Complete(context.Background(), &agentcore.ProviderRequest{
		Model: "gemini-2.5-flash",
		Messages: []agentcore.Message{
			{Role: agentcore.RoleUser, Content: "return json"},
		},
		ResponseFormat: agentcore.NewJSONSchemaResponseFormat("answer", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"answer": map[string]any{"type": "string"},
			},
			"required": []string{"answer"},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.Structured) != `{"answer":"ok"}` {
		t.Fatalf("structured = %s", string(resp.Structured))
	}

	gc, ok := gotBody["generationConfig"].(map[string]any)
	if !ok {
		t.Fatalf("missing generationConfig: %#v", gotBody)
	}
	if gc["responseMimeType"] != "application/json" {
		t.Fatalf("responseMimeType = %#v", gc["responseMimeType"])
	}
	if _, ok := gc["responseSchema"].(map[string]any); !ok {
		t.Fatalf("missing responseSchema: %#v", gc)
	}
}

func TestProviderComplete_SendsImageBlocksAsInlineData(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates":[{"content":{"parts":[{"text":"cat"}]}}],
			"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":1,"totalTokenCount":5}
		}`))
	}))
	defer srv.Close()

	provider := New(Config{
		APIKey:  "test-key",
		BaseURL: srv.URL,
		Model:   "gemini-2.5-flash",
		Client:  srv.Client(),
	})

	_, err := provider.Complete(context.Background(), &agentcore.ProviderRequest{
		Model: "gemini-2.5-flash",
		Messages: []agentcore.Message{
			{
				Role:    agentcore.RoleUser,
				Content: "describe this",
				Blocks: []agentcore.ContentBlock{
					{
						Kind:      agentcore.BlockKindImage,
						URL:       "data:image/png;base64,aGVsbG8=",
						MediaType: "image/png",
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	contents, ok := gotBody["contents"].([]any)
	if !ok || len(contents) != 1 {
		t.Fatalf("contents = %#v", gotBody["contents"])
	}
	content, ok := contents[0].(map[string]any)
	if !ok {
		t.Fatalf("content = %#v", contents[0])
	}
	parts, ok := content["parts"].([]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("parts = %#v", content["parts"])
	}
	inlinePart, ok := parts[1].(map[string]any)
	if !ok {
		t.Fatalf("inline part = %#v", parts[1])
	}
	inlineData, ok := inlinePart["inlineData"].(map[string]any)
	if !ok {
		t.Fatalf("inlineData = %#v", inlinePart)
	}
	if inlineData["mimeType"] != "image/png" {
		t.Fatalf("mimeType = %#v", inlineData["mimeType"])
	}
	if inlineData["data"] != "aGVsbG8=" {
		t.Fatalf("data = %#v", inlineData["data"])
	}
}

func TestProviderComplete_SendsThinkingConfig(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates":[{"content":{"parts":[{"text":"ok"}]}}],
			"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":1,"totalTokenCount":5}
		}`))
	}))
	defer srv.Close()

	provider := New(Config{
		APIKey:  "test-key",
		BaseURL: srv.URL,
		Model:   "gemini-2.5-flash",
		Client:  srv.Client(),
	})

	_, err := provider.Complete(context.Background(), &agentcore.ProviderRequest{
		Model: "gemini-2.5-flash",
		Messages: []agentcore.Message{
			{Role: agentcore.RoleUser, Content: "think first"},
		},
		Thinking: &agentcore.ThinkingConfig{
			IncludeThoughts: true,
			Display:         agentcore.ThinkingDisplayOmitted,
			Budget:          -1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	gc, ok := gotBody["generationConfig"].(map[string]any)
	if !ok {
		t.Fatalf("generationConfig = %#v", gotBody["generationConfig"])
	}
	tc, ok := gc["thinkingConfig"].(map[string]any)
	if !ok {
		t.Fatalf("thinkingConfig = %#v", gc["thinkingConfig"])
	}
	if tc["includeThoughts"] != false {
		t.Fatalf("includeThoughts = %#v", tc["includeThoughts"])
	}
	if tc["thinkingBudget"] != float64(-1) {
		t.Fatalf("thinkingBudget = %#v", tc["thinkingBudget"])
	}
}

func TestProviderComplete_PreservesThinkingAndToolCallBlocks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates":[{
				"content":{"parts":[
					{"text":"draft plan","thought":true,"thoughtSignature":"sig_1"},
					{"text":"final answer"},
					{"functionCall":{"name":"lookup","id":"call_1","args":{"q":"tokyo"}}}
				]}
			}],
			"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":3,"totalTokenCount":7}
		}`))
	}))
	defer srv.Close()

	provider := New(Config{
		APIKey:  "test-key",
		BaseURL: srv.URL,
		Model:   "gemini-2.5-flash",
		Client:  srv.Client(),
	})

	resp, err := provider.Complete(context.Background(), &agentcore.ProviderRequest{
		Model:    "gemini-2.5-flash",
		Messages: []agentcore.Message{{Role: agentcore.RoleUser, Content: "do it"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "final answer" {
		t.Fatalf("content = %q", resp.Content)
	}
	if len(resp.Blocks) != 3 {
		t.Fatalf("blocks len = %d", len(resp.Blocks))
	}
	if resp.Blocks[0].Kind != agentcore.BlockKindThinking || resp.Blocks[0].Signature != "sig_1" {
		t.Fatalf("thinking block = %#v", resp.Blocks[0])
	}
	if resp.Blocks[1].Kind != agentcore.BlockKindText || resp.Blocks[1].Text != "final answer" {
		t.Fatalf("text block = %#v", resp.Blocks[1])
	}
	if resp.Blocks[2].Kind != agentcore.BlockKindToolCall || resp.Blocks[2].Name != "lookup" {
		t.Fatalf("tool block = %#v", resp.Blocks[2])
	}
}

func TestProviderStream_EmitsThinkingBlocks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"candidates":[{"content":{"parts":[{"text":"draft plan","thought":true,"thoughtSignature":"sig_1"},{"text":"final answer"},{"functionCall":{"name":"lookup","id":"call_1","args":{"q":"tokyo"}}}]}}]}`,
			``,
			`data: {"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":3,"totalTokenCount":7}}`,
			``,
		}, "\n")))
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer srv.Close()

	provider := New(Config{
		APIKey:  "test-key",
		BaseURL: srv.URL,
		Model:   "gemini-2.5-flash",
		Client:  srv.Client(),
	})

	ch, err := provider.Stream(context.Background(), &agentcore.ProviderRequest{
		Model:    "gemini-2.5-flash",
		Messages: []agentcore.Message{{Role: agentcore.RoleUser, Content: "do it"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	var content strings.Builder
	var blocks []agentcore.ContentBlock
	var args string
	for delta := range ch {
		content.WriteString(delta.Content)
		blocks = agentcore.MergeContentBlocks(blocks, delta.Blocks...)
		for _, tc := range delta.ToolCalls {
			args += tc.Arguments
		}
	}

	if content.String() != "final answer" {
		t.Fatalf("content = %q", content.String())
	}
	if len(blocks) != 3 {
		t.Fatalf("blocks len = %d", len(blocks))
	}
	if blocks[0].Kind != agentcore.BlockKindThinking || blocks[0].Signature != "sig_1" {
		t.Fatalf("thinking block = %#v", blocks[0])
	}
	if blocks[1].Kind != agentcore.BlockKindText || blocks[1].Text != "final answer" {
		t.Fatalf("text block = %#v", blocks[1])
	}
	if blocks[2].Kind != agentcore.BlockKindToolCall || blocks[2].Arguments != `{"q":"tokyo"}` {
		t.Fatalf("tool block = %#v", blocks[2])
	}
	if args != `{"q":"tokyo"}` {
		t.Fatalf("tool args = %q", args)
	}
}
