package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
)

func TestProviderComplete_UsesStructuredOutputInstruction(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"msg_123",
			"content":[{"type":"text","text":"{\"answer\":\"ok\"}"}],
			"stop_reason":"end_turn",
			"usage":{"input_tokens":4,"output_tokens":3}
		}`))
	}))
	defer srv.Close()

	provider := New(Config{
		APIKey:  "test-key",
		BaseURL: srv.URL,
		Client:  srv.Client(),
	})

	resp, err := provider.Complete(context.Background(), &agentcore.ProviderRequest{
		Model: "claude-sonnet",
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

	system, _ := gotBody["system"].(string)
	if !strings.Contains(system, "JSON Schema") {
		t.Fatalf("expected structured instruction in system prompt, got %q", system)
	}
}

func TestProviderComplete_SendsImageBlocks(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"msg_456",
			"content":[{"type":"text","text":"cat"}],
			"stop_reason":"end_turn",
			"usage":{"input_tokens":4,"output_tokens":1}
		}`))
	}))
	defer srv.Close()

	provider := New(Config{
		APIKey:  "test-key",
		BaseURL: srv.URL,
		Client:  srv.Client(),
	})

	_, err := provider.Complete(context.Background(), &agentcore.ProviderRequest{
		Model: "claude-sonnet",
		Messages: []agentcore.Message{
			{
				Role:    agentcore.RoleUser,
				Content: "describe this",
				Blocks: []agentcore.ContentBlock{{
					Kind:      agentcore.BlockKindImage,
					URL:       "data:image/png;base64,aGVsbG8=",
					MediaType: "image/png",
				}},
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
	blocks, ok := msg["content"].([]any)
	if !ok || len(blocks) != 2 {
		t.Fatalf("content = %#v", msg["content"])
	}
	imageBlock, ok := blocks[1].(map[string]any)
	if !ok {
		t.Fatalf("image block = %#v", blocks[1])
	}
	if imageBlock["type"] != "image" {
		t.Fatalf("type = %#v", imageBlock["type"])
	}
	source, ok := imageBlock["source"].(map[string]any)
	if !ok {
		t.Fatalf("source = %#v", imageBlock["source"])
	}
	if source["type"] != "base64" {
		t.Fatalf("source.type = %#v", source["type"])
	}
	if source["media_type"] != "image/png" {
		t.Fatalf("source.media_type = %#v", source["media_type"])
	}
	if source["data"] != "aGVsbG8=" {
		t.Fatalf("source.data = %#v", source["data"])
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
			"id":"msg_777",
			"content":[{"type":"text","text":"ok"}],
			"stop_reason":"end_turn",
			"usage":{"input_tokens":4,"output_tokens":1}
		}`))
	}))
	defer srv.Close()

	provider := New(Config{
		APIKey:  "test-key",
		BaseURL: srv.URL,
		Client:  srv.Client(),
	})

	_, err := provider.Complete(context.Background(), &agentcore.ProviderRequest{
		Model: "claude-sonnet",
		Messages: []agentcore.Message{
			{Role: agentcore.RoleUser, Content: "think first"},
		},
		Thinking: &agentcore.ThinkingConfig{
			IncludeThoughts: true,
			Display:         agentcore.ThinkingDisplaySummarized,
			Effort:          agentcore.ThinkingEffortHigh,
			Budget:          4096,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	cfg, ok := gotBody["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("thinking = %#v", gotBody["thinking"])
	}
	if cfg["type"] != "adaptive" {
		t.Fatalf("type = %#v", cfg["type"])
	}
	if cfg["display"] != "summarized" {
		t.Fatalf("display = %#v", cfg["display"])
	}
	if cfg["effort"] != "high" {
		t.Fatalf("effort = %#v", cfg["effort"])
	}
}

func TestProviderComplete_PreservesThinkingAndToolCallBlocks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"msg_789",
			"content":[
				{"type":"thinking","thinking":"let me reason","signature":"sig_1"},
				{"type":"text","text":"final answer"},
				{"type":"tool_use","id":"toolu_1","name":"lookup","input":{"q":"tokyo"}}
			],
			"stop_reason":"tool_use",
			"usage":{"input_tokens":4,"output_tokens":3}
		}`))
	}))
	defer srv.Close()

	provider := New(Config{
		APIKey:  "test-key",
		BaseURL: srv.URL,
		Client:  srv.Client(),
	})

	resp, err := provider.Complete(context.Background(), &agentcore.ProviderRequest{
		Model:    "claude-sonnet",
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
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"let me reason"}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig_1"}}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"final answer"}}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_1","name":"lookup","input":{}}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"q\":\"tokyo\"}"}}`,
			``,
			`event: message_delta`,
			`data: {"type":"message_delta","usage":{"input_tokens":4,"output_tokens":3}}`,
			``,
			`event: message_stop`,
			`data: {"type":"message_stop"}`,
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
		Client:  srv.Client(),
	})

	ch, err := provider.Stream(context.Background(), &agentcore.ProviderRequest{
		Model:    "claude-sonnet",
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
