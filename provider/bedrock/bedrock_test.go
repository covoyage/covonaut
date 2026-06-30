package bedrock

import (
	"encoding/json"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
)

func TestNew_RequiresRegion(t *testing.T) {
	_, err := New(Config{})
	if err == nil || err.Error() != "bedrock: region is required" {
		t.Fatalf("expected region error, got %v", err)
	}
}

func TestNew_RequiresCredentials(t *testing.T) {
	_, err := New(Config{Region: "us-east-1"})
	if err == nil || err.Error() != "bedrock: AWS credentials required" {
		t.Fatalf("expected credentials error, got %v", err)
	}
}

func TestNew_Success(t *testing.T) {
	p, err := New(Config{
		Region:    "us-east-1",
		AccessKey: "AKID",
		SecretKey: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
}

func TestBuildInferenceConfig(t *testing.T) {
	ic := buildInferenceConfig(&agentcore.ProviderRequest{})
	if len(ic) != 0 {
		t.Fatalf("expected empty, got %v", ic)
	}

	ic = buildInferenceConfig(&agentcore.ProviderRequest{
		MaxTokens:   100,
		Temperature: 0.5,
	})
	if ic["maxTokens"].(int64) != 100 {
		t.Fatalf("maxTokens = %v (type: %T)", ic["maxTokens"], ic["maxTokens"])
	}
	if ic["temperature"].(float64) != 0.5 {
		t.Fatalf("temperature = %v (type: %T)", ic["temperature"], ic["temperature"])
	}
}

func TestConvertMessages(t *testing.T) {
	msgs, system := convertMessages([]agentcore.Message{
		{Role: agentcore.RoleSystem, Content: "you are helpful"},
		{Role: agentcore.RoleUser, Content: "hello"},
		{Role: agentcore.RoleAssistant, Content: "hi there"},
	})
	if len(system) != 1 || system[0]["text"] != "you are helpful" {
		t.Fatalf("system = %+v", system)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0]["role"] != "user" || msgs[0]["content"].([]map[string]any)[0]["text"] != "hello" {
		t.Fatalf("msg[0] = %+v", msgs[0])
	}
	if msgs[1]["role"] != "assistant" {
		t.Fatalf("msg[1] role = %v", msgs[1]["role"])
	}
}

func TestBuildBody(t *testing.T) {
	body, err := buildBody(&agentcore.ProviderRequest{
		Model: "claude-3",
		Messages: []agentcore.Message{
			{Role: agentcore.RoleUser, Content: "hello"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatal(err)
	}
	msgs, ok := parsed["messages"].([]any)
	if !ok || len(msgs) != 1 {
		t.Fatalf("messages = %+v", parsed["messages"])
	}
}

func TestExtractResponse(t *testing.T) {
	raw := json.RawMessage(`[
		{"text": "hello "},
		{"text": "world"}
	]`)
	text, blocks := extractResponse(raw)
	if text != "hello world" {
		t.Fatalf("text = %q", text)
	}
	if len(blocks) != 0 {
		t.Fatalf("expected 0 blocks, got %d", len(blocks))
	}
}

func TestExtractResponse_WithThinking(t *testing.T) {
	raw := json.RawMessage(`[
		{"reasoningContent": {"text": "thinking..."}},
		{"text": "answer"}
	]`)
	text, blocks := extractResponse(raw)
	if text != "answer" {
		t.Fatalf("text = %q", text)
	}
	if len(blocks) != 1 || blocks[0].Kind != agentcore.BlockKindThinking || blocks[0].Text != "thinking..." {
		t.Fatalf("blocks = %+v", blocks)
	}
}

func TestExtractResponse_Empty(t *testing.T) {
	text, blocks := extractResponse(nil)
	if text != "" || len(blocks) != 0 {
		t.Fatalf("expected empty, got text=%q blocks=%d", text, len(blocks))
	}
}

func TestExtractResponse_InvalidJSON(t *testing.T) {
	text, blocks := extractResponse(json.RawMessage(`not json`))
	if text != "" || len(blocks) != 0 {
		t.Fatalf("expected empty on invalid json, got text=%q", text)
	}
}



func TestSignerHelpers(t *testing.T) {
	if canonicalURI("/test") != "/test" {
		t.Fatal("canonicalURI failed")
	}
	if canonicalURI("") != "/" {
		t.Fatal("canonicalURI empty failed")
	}
	if canonicalQuery("a=1&b=2") != "a=1&b=2" {
		t.Fatal("canonicalQuery failed")
	}
	if canonicalQuery("") != "" {
		t.Fatal("canonicalQuery empty failed")
	}
	if sha256Hex([]byte("test")) != "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08" {
		t.Fatal("sha256Hex failed")
	}
	hmac := hmacSHA256([]byte("key"), []byte("data"))
	if len(hmac) != 32 {
		t.Fatal("hmacSHA256 length wrong")
	}
}

func TestInterface(t *testing.T) {
	var _ agentcore.Provider = (*bedrockProvider)(nil)
}
