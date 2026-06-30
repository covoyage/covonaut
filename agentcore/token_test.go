package agentcore

import (
	"testing"
)

func TestEstimateTokens(t *testing.T) {
	if got := EstimateTokens(""); got != 0 {
		t.Fatalf("empty got %d", got)
	}
	if got := EstimateTokens("hello"); got != 2 {
		t.Fatalf("'hello' got %d", got)
	}
	// 1000 chars / 4 = 250 tokens
	s := string(make([]byte, 1000))
	if got := EstimateTokens(s); got != 250 {
		t.Fatalf("1000 chars got %d", got)
	}
}

func TestEstimateMessageTokens(t *testing.T) {
	msg := Message{
		Role:    RoleUser,
		Content: "hello world",
	}
	tokens := EstimateMessageTokens(msg)
	if tokens <= 0 {
		t.Fatalf("expected positive token count, got %d", tokens)
	}
	// 4 (overhead) + 3 ('hello world' = 11/4 = 2.75 -> 3) = 7
	if tokens != 7 {
		t.Fatalf("expected 7 tokens, got %d", tokens)
	}
}

func TestEstimateMessageTokensWithToolCalls(t *testing.T) {
	msg := Message{
		Role: RoleAssistant,
		ToolCalls: []ToolCall{
			{Name: "get_weather", Arguments: `{"city":"london"}`},
		},
	}
	tokens := EstimateMessageTokens(msg)
	if tokens <= 0 {
		t.Fatalf("expected positive token count, got %d", tokens)
	}
}

func TestEstimateMessagesTokens(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: "hi"},
		{Role: RoleAssistant, Content: "hello there"},
	}
	tokens := EstimateMessagesTokens(msgs)
	if tokens <= 0 {
		t.Fatalf("expected positive token count, got %d", tokens)
	}
}

func TestEstimateToolDefinitionsTokens(t *testing.T) {
	tokens := EstimateToolDefinitionsTokens(nil)
	if tokens != 0 {
		t.Fatalf("expected 0 for nil, got %d", tokens)
	}

	tokens = EstimateToolDefinitionsTokens([]ToolDefinition{})
	if tokens != 0 {
		t.Fatalf("expected 0 for empty, got %d", tokens)
	}

	defs := []ToolDefinition{
		{
			Name:        "get_weather",
			Description: "Get weather for a city",
			Parameters: map[string]any{"type": "object"},
		},
	}
	tokens = EstimateToolDefinitionsTokens(defs)
	if tokens <= 0 {
		t.Fatalf("expected positive token count, got %d", tokens)
	}
}
