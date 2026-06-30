package vertex

import (
	"encoding/json"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
)

func TestNewGemini_Validation(t *testing.T) {
	_, err := NewGemini(Config{})
	if err == nil {
		t.Fatal("expected error without project")
	}
	_, err = NewGemini(Config{Project: "my-project"})
	if err == nil {
		t.Fatal("expected error without region")
	}
}

func TestNewAnthropic_Validation(t *testing.T) {
	_, err := NewAnthropic(Config{})
	if err == nil {
		t.Fatal("expected error without project")
	}
	_, err = NewAnthropic(Config{Project: "my-project"})
	if err == nil {
		t.Fatal("expected error without region")
	}
}

func TestNewAnthropic_WithAccessToken(t *testing.T) {
	provider, err := NewAnthropic(Config{
		Project:     "my-project",
		Region:      "us-central1",
		AccessToken: "test-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	if provider == nil {
		t.Fatal("expected non-nil provider")
	}
}

func TestNewGemini_WithAccessToken(t *testing.T) {
	provider, err := NewGemini(Config{
		Project:     "my-project",
		Region:      "us-central1",
		AccessToken: "test-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	if provider == nil {
		t.Fatal("expected non-nil provider")
	}
}

func TestEndpoint(t *testing.T) {
	p := &anthropicVertex{
		project: "my-project",
		region:  "us-central1",
	}
	ep := p.endpoint("claude-3-sonnet")
	expected := "https://us-central1-aiplatform.googleapis.com/v1/projects/my-project/locations/us-central1/publishers/anthropic/models/claude-3-sonnet:streamRawPredict"
	if ep != expected {
		t.Fatalf("endpoint = %q, want %q", ep, expected)
	}
}

func TestBuildRequest(t *testing.T) {
	p := &anthropicVertex{}
	body, err := p.buildRequest(&agentcore.ProviderRequest{
		Model: "claude-3-sonnet",
		Messages: []agentcore.Message{
			{Role: agentcore.RoleSystem, Content: "be helpful"},
			{Role: agentcore.RoleUser, Content: "hello"},
			{Role: agentcore.RoleAssistant, Content: "hi"},
		},
		MaxTokens:   2000,
		Temperature: 0.5,
		Thinking: &agentcore.ThinkingConfig{
			Effort: "medium",
		},
	}, false)
	if err != nil {
		t.Fatal(err)
	}

	var parsed vertexAnthropicRequest
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Model != "claude-3-sonnet" {
		t.Fatalf("model = %q", parsed.Model)
	}
	if parsed.MaxTokens != 2000 {
		t.Fatalf("max_tokens = %d", parsed.MaxTokens)
	}
	if parsed.System != "be helpful" {
		t.Fatalf("system = %q", parsed.System)
	}
	if len(parsed.Messages) != 2 {
		t.Fatalf("messages count = %d", len(parsed.Messages))
	}
	if parsed.Messages[0].Role != "user" || parsed.Messages[1].Role != "assistant" {
		t.Fatalf("roles = %+v", parsed.Messages)
	}
	if parsed.Stream != false {
		t.Fatalf("stream should be false")
	}
	if parsed.Thinking == nil || parsed.Thinking.Type != "enabled" {
		t.Fatalf("thinking = %+v", parsed.Thinking)
	}
}

func TestBuildRequest_Stream(t *testing.T) {
	p := &anthropicVertex{}
	body, err := p.buildRequest(&agentcore.ProviderRequest{
		Model: "claude-3-haiku",
		Messages: []agentcore.Message{
			{Role: agentcore.RoleUser, Content: "hi"},
		},
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	var parsed vertexAnthropicRequest
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Stream != true {
		t.Fatalf("stream should be true")
	}
}

func TestInterface(t *testing.T) {
	var _ agentcore.Provider = (*anthropicVertex)(nil)
}
