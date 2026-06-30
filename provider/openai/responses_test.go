package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
)

func TestResponsesAPI_Complete_SimpleText(t *testing.T) {
	var gotPath string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		defer r.Body.Close()
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_001",
			"object":"response",
			"status":"completed",
			"output":[
				{
					"type":"message",
					"id":"msg_001",
					"status":"completed",
					"role":"assistant",
					"content":[
						{"type":"output_text","text":"Hello from responses!"}
					]
				}
			],
			"usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}
		}`))
	}))
	defer srv.Close()

	provider := New(Config{
		APIKey:   "test-key",
		BaseURL:  srv.URL,
		Protocol: APIProtocolResponses,
		Client:   srv.Client(),
	})

	resp, err := provider.Complete(context.Background(), &agentcore.ProviderRequest{
		Model:    "gpt-4o",
		Messages: []agentcore.Message{{Role: agentcore.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "Hello from responses!" {
		t.Fatalf("content = %q", resp.Content)
	}
	if gotPath != "/responses" {
		t.Fatalf("path = %q, want /responses", gotPath)
	}
	if gotBody["model"] != "gpt-4o" {
		t.Fatalf("model = %#v", gotBody["model"])
	}
	if resp.Usage.TotalTokens != 8 {
		t.Fatalf("total_tokens = %d", resp.Usage.TotalTokens)
	}
}

func TestResponsesAPI_Complete_FunctionCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_002",
			"object":"response",
			"status":"completed",
			"output":[
				{
					"type":"function_call",
					"id":"fc_001",
					"call_id":"call_abc",
					"name":"get_weather",
					"arguments":"{\"city\":\"Tokyo\"}",
					"status":"completed"
				}
			],
			"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}
		}`))
	}))
	defer srv.Close()

	provider := New(Config{
		APIKey:   "test-key",
		BaseURL:  srv.URL,
		Protocol: APIProtocolResponses,
		Client:   srv.Client(),
	})

	resp, err := provider.Complete(context.Background(), &agentcore.ProviderRequest{
		Model:    "gpt-4o",
		Messages: []agentcore.Message{{Role: agentcore.RoleUser, Content: "weather"}},
		Tools: []agentcore.ToolDefinition{
			{Name: "get_weather", Description: "Get weather", Parameters: map[string]any{"type": "object"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("tool_calls = %#v", resp.ToolCalls)
	}
	if resp.ToolCalls[0].Name != "get_weather" || resp.ToolCalls[0].ID != "call_abc" {
		t.Fatalf("tool_call = %#v", resp.ToolCalls[0])
	}
	if resp.ToolCalls[0].Arguments != `{"city":"Tokyo"}` {
		t.Fatalf("arguments = %q", resp.ToolCalls[0].Arguments)
	}
}

func TestResponsesAPI_Complete_SystemInstructions(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_003",
			"object":"response",
			"status":"completed",
			"output":[
				{
					"type":"message",
					"id":"msg_003",
					"status":"completed",
					"role":"assistant",
					"content":[{"type":"output_text","text":"ok"}]
				}
			],
			"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}
		}`))
	}))
	defer srv.Close()

	provider := New(Config{
		APIKey:   "test-key",
		BaseURL:  srv.URL,
		Protocol: APIProtocolResponses,
		Client:   srv.Client(),
	})

	_, err := provider.Complete(context.Background(), &agentcore.ProviderRequest{
		Model: "gpt-4o",
		Messages: []agentcore.Message{
			{Role: agentcore.RoleSystem, Content: "You are helpful"},
			{Role: agentcore.RoleUser, Content: "hi"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotBody["instructions"] != "You are helpful" {
		t.Fatalf("instructions = %#v", gotBody["instructions"])
	}
}

func TestResponsesAPI_Complete_StructuredOutput(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_004",
			"object":"response",
			"status":"completed",
			"output":[
				{
					"type":"message",
					"id":"msg_004",
					"status":"completed",
					"role":"assistant",
					"content":[{"type":"output_text","text":"{\"answer\":42}"}]
				}
			],
			"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}
		}`))
	}))
	defer srv.Close()

	provider := New(Config{
		APIKey:   "test-key",
		BaseURL:  srv.URL,
		Protocol: APIProtocolResponses,
		Client:   srv.Client(),
	})

	resp, err := provider.Complete(context.Background(), &agentcore.ProviderRequest{
		Model:    "gpt-4o",
		Messages: []agentcore.Message{{Role: agentcore.RoleUser, Content: "answer"}},
		ResponseFormat: &agentcore.ResponseFormat{
			Type: agentcore.ResponseFormatJSONSchema,
			JSONSchema: &agentcore.ResponseFormatJSONSchemaConfig{
				Name:   "answer",
				Strict: true,
				Schema: map[string]any{"type": "object", "properties": map[string]any{"answer": map[string]any{"type": "number"}}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.Structured) != `{"answer":42}` {
		t.Fatalf("structured = %s", string(resp.Structured))
	}
	textCfg, ok := gotBody["text"].(map[string]any)
	if !ok {
		t.Fatalf("missing text config: %#v", gotBody)
	}
	fmt, ok := textCfg["format"].(map[string]any)
	if !ok {
		t.Fatalf("missing text.format: %#v", textCfg)
	}
	if fmt["type"] != "json_schema" {
		t.Fatalf("format type = %#v", fmt["type"])
	}
}

func TestResponsesAPI_Complete_ToolResult(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_005",
			"object":"response",
			"status":"completed",
			"output":[
				{
					"type":"message",
					"id":"msg_005",
					"status":"completed",
					"role":"assistant",
					"content":[{"type":"output_text","text":"The weather is sunny"}]
				}
			],
			"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}
		}`))
	}))
	defer srv.Close()

	provider := New(Config{
		APIKey:   "test-key",
		BaseURL:  srv.URL,
		Protocol: APIProtocolResponses,
		Client:   srv.Client(),
	})

	_, err := provider.Complete(context.Background(), &agentcore.ProviderRequest{
		Model: "gpt-4o",
		Messages: []agentcore.Message{
			{Role: agentcore.RoleUser, Content: "weather"},
			{Role: agentcore.RoleAssistant, ToolCalls: []agentcore.ToolCall{{ID: "call_1", Name: "get_weather", Arguments: `{"city":"Tokyo"}`}}},
			{Role: agentcore.RoleTool, ToolCallID: "call_1", Content: `{"temp":25}`},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	input, ok := gotBody["input"].([]any)
	if !ok || len(input) < 3 {
		t.Fatalf("input = %#v", gotBody["input"])
	}
	toolResult, ok := input[2].(map[string]any)
	if !ok {
		t.Fatalf("tool result = %#v", input[2])
	}
	if toolResult["type"] != "function_call_output" {
		t.Fatalf("type = %#v", toolResult["type"])
	}
	if toolResult["call_id"] != "call_1" {
		t.Fatalf("call_id = %#v", toolResult["call_id"])
	}
}

func TestResponsesAPI_Stream_TextDelta(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			"event: response.created\ndata: {\"id\":\"resp_s1\",\"type\":\"response.created\"}\n\n"+
				"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Hel\"}\n\n"+
				"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"lo\"}\n\n"+
				"event: response.completed\ndata: {\"type\":\"response.completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":2,\"total_tokens\":3}}\n\n",
		))
	}))
	defer srv.Close()

	provider := New(Config{
		APIKey:   "test-key",
		BaseURL:  srv.URL,
		Protocol: APIProtocolResponses,
		Client:   srv.Client(),
	})

	ch, err := provider.Stream(context.Background(), &agentcore.ProviderRequest{
		Model:    "gpt-4o",
		Messages: []agentcore.Message{{Role: agentcore.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	var content string
	var gotUsage bool
	for sd := range ch {
		content += sd.Content
		if sd.Usage != nil {
			gotUsage = true
			if sd.Usage.TotalTokens != 3 {
				t.Fatalf("total_tokens = %d", sd.Usage.TotalTokens)
			}
		}
	}
	if content != "Hello" {
		t.Fatalf("content = %q", content)
	}
	if !gotUsage {
		t.Fatal("missing usage in stream")
	}
}

func TestResponsesAPI_Stream_FunctionCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"id\":\"fc_1\",\"call_id\":\"call_xyz\",\"name\":\"search\",\"status\":\"in_progress\"}}\n\n"+
				"event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"{\\\"q\\\":\"}\n\n"+
				"event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"\\\"test\\\"}\"}\n\n"+
				"event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"id\":\"fc_1\",\"call_id\":\"call_xyz\",\"name\":\"search\",\"arguments\":\"{\\\"q\\\":\\\"test\\\"}\",\"status\":\"completed\"}}\n\n"+
				"event: response.completed\ndata: {\"type\":\"response.completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}\n\n",
		))
	}))
	defer srv.Close()

	provider := New(Config{
		APIKey:   "test-key",
		BaseURL:  srv.URL,
		Protocol: APIProtocolResponses,
		Client:   srv.Client(),
	})

	ch, err := provider.Stream(context.Background(), &agentcore.ProviderRequest{
		Model:    "gpt-4o",
		Messages: []agentcore.Message{{Role: agentcore.RoleUser, Content: "search"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	var toolCalls []agentcore.ToolCallDelta
	for sd := range ch {
		toolCalls = append(toolCalls, sd.ToolCalls...)
	}
	if len(toolCalls) != 1 {
		t.Fatalf("tool_calls = %#v", toolCalls)
	}
	if toolCalls[0].Name != "search" || toolCalls[0].ID != "call_xyz" {
		t.Fatalf("tool_call = %#v", toolCalls[0])
	}
	if !strings.Contains(toolCalls[0].Arguments, "test") {
		t.Fatalf("arguments = %q", toolCalls[0].Arguments)
	}
}

func TestToResponsesInput_SimpleString(t *testing.T) {
	result := ToResponsesInput([]agentcore.Message{
		{Role: agentcore.RoleUser, Content: "hello"},
	})
	str, ok := result.(string)
	if !ok || str != "hello" {
		t.Fatalf("result = %#v", result)
	}
}

func TestToResponsesInput_MultiTurn(t *testing.T) {
	result := ToResponsesInput([]agentcore.Message{
		{Role: agentcore.RoleSystem, Content: "be helpful"},
		{Role: agentcore.RoleUser, Content: "hi"},
		{Role: agentcore.RoleAssistant, ToolCalls: []agentcore.ToolCall{{ID: "c1", Name: "f", Arguments: "{}"}}},
		{Role: agentcore.RoleTool, ToolCallID: "c1", Content: "result"},
	})
	items, ok := result.([]any)
	if !ok || len(items) != 4 {
		t.Fatalf("items = %#v", result)
	}
	msg0, ok := items[0].(responsesInputMessage)
	if !ok || msg0.Role != "developer" {
		t.Fatalf("item[0] = %#v", items[0])
	}
	msg1, ok := items[1].(responsesInputMessage)
	if !ok || msg1.Role != "user" {
		t.Fatalf("item[1] = %#v", items[1])
	}
	fc, ok := items[2].(map[string]any)
	if !ok || fc["type"] != "function_call" {
		t.Fatalf("item[2] = %#v", items[2])
	}
	fo, ok := items[3].(responsesFunctionCallOutput)
	if !ok || fo.CallID != "c1" {
		t.Fatalf("item[3] = %#v", items[3])
	}
}

func TestToResponsesInput_ImageBlocks(t *testing.T) {
	result := ToResponsesInput([]agentcore.Message{
		{
			Role:    agentcore.RoleUser,
			Content: "describe",
			Blocks: []agentcore.ContentBlock{
				{Kind: agentcore.BlockKindImage, URL: "https://example.com/img.png", Detail: "high"},
			},
		},
	})
	items, ok := result.([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("items = %#v", result)
	}
	msg, ok := items[0].(responsesInputMessage)
	if !ok || msg.Role != "user" {
		t.Fatalf("msg = %#v", items[0])
	}
	parts, ok := msg.Content.([]responsesInputContent)
	if !ok || len(parts) != 2 {
		t.Fatalf("content = %#v", msg.Content)
	}
	if parts[1].Type != "input_image" || parts[1].ImageURL != "https://example.com/img.png" {
		t.Fatalf("image part = %#v", parts[1])
	}
}
