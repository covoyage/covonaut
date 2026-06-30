package agentcore

import (
	"context"
	"testing"
)

type captureStructuredProvider struct {
	lastRequest *ProviderRequest
}

func (p *captureStructuredProvider) Complete(ctx context.Context, req *ProviderRequest) (*ProviderResponse, error) {
	cp := *req
	p.lastRequest = &cp
	return &ProviderResponse{
		Content:    `{"answer":"ok"}`,
		Blocks:     []ContentBlock{{Kind: BlockKindText, Text: `{"answer":"ok"}`}},
		Structured: []byte(`{"answer":"ok"}`),
	}, nil
}

func (p *captureStructuredProvider) Stream(ctx context.Context, req *ProviderRequest) (<-chan StreamDelta, error) {
	ch := make(chan StreamDelta)
	close(ch)
	return ch, nil
}

type streamingBlocksProvider struct{}

func (streamingBlocksProvider) Complete(ctx context.Context, req *ProviderRequest) (*ProviderResponse, error) {
	return &ProviderResponse{Content: "unused"}, nil
}

func (streamingBlocksProvider) Stream(ctx context.Context, req *ProviderRequest) (<-chan StreamDelta, error) {
	ch := make(chan StreamDelta, 3)
	ch <- StreamDelta{
		Content: "hel",
		Blocks:  []ContentBlock{{Kind: BlockKindText, Text: "hel"}},
	}
	ch <- StreamDelta{
		Content: "lo",
		Blocks:  []ContentBlock{{Kind: BlockKindText, Text: "lo"}},
	}
	ch <- StreamDelta{
		Usage: &TokenUsage{CompletionTokens: 1, TotalTokens: 1},
		Done:  true,
	}
	close(ch)
	return ch, nil
}

func TestAgentRun_PassesResponseFormatToProvider(t *testing.T) {
	provider := &captureStructuredProvider{}
	format := NewJSONSchemaResponseFormat("answer", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"answer": map[string]any{"type": "string"},
		},
		"required": []string{"answer"},
	})

	agent := New(Config{
		ModelConfig: ModelConfig{
			Name:           "structured",
			Model:          "stub",
			Provider:       provider,
			ResponseFormat: format,
		},
	})

	out, err := agent.Run(context.Background(), "say hi")
	if err != nil {
		t.Fatal(err)
	}
	if out != `{"answer":"ok"}` {
		t.Fatalf("output = %q", out)
	}
	if provider.lastRequest == nil || provider.lastRequest.ResponseFormat == nil {
		t.Fatal("expected response format on provider request")
	}
	if provider.lastRequest.ResponseFormat.Type != ResponseFormatJSONSchema {
		t.Fatalf("type = %q", provider.lastRequest.ResponseFormat.Type)
	}
	if provider.lastRequest.ResponseFormat.JSONSchema == nil || provider.lastRequest.ResponseFormat.JSONSchema.Name != "answer" {
		t.Fatalf("schema = %#v", provider.lastRequest.ResponseFormat.JSONSchema)
	}
}

func TestAgentRun_PassesThinkingToProvider(t *testing.T) {
	provider := &captureStructuredProvider{}
	thinking := &ThinkingConfig{
		IncludeThoughts: true,
		Display:         ThinkingDisplaySummarized,
		Effort:          ThinkingEffortMedium,
		Budget:          2048,
	}

	agent := New(Config{
		ModelConfig: ModelConfig{
			Name:     "thinking",
			Model:    "stub",
			Provider: provider,
			Thinking: thinking,
		},
	})

	if _, err := agent.Run(context.Background(), "think"); err != nil {
		t.Fatal(err)
	}
	if provider.lastRequest == nil || provider.lastRequest.Thinking == nil {
		t.Fatal("expected thinking config on provider request")
	}
	if !provider.lastRequest.Thinking.IncludeThoughts {
		t.Fatal("expected IncludeThoughts to be true")
	}
	if provider.lastRequest.Thinking.Display != ThinkingDisplaySummarized {
		t.Fatalf("display = %q", provider.lastRequest.Thinking.Display)
	}
	if provider.lastRequest.Thinking.Effort != ThinkingEffortMedium {
		t.Fatalf("effort = %q", provider.lastRequest.Thinking.Effort)
	}
	if provider.lastRequest.Thinking.Budget != 2048 {
		t.Fatalf("budget = %d", provider.lastRequest.Thinking.Budget)
	}
}

func TestDecodeStructured(t *testing.T) {
	type payload struct {
		Answer string `json:"answer"`
	}

	got, err := DecodeStructured[payload](`{"answer":"ok"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got.Answer != "ok" {
		t.Fatalf("answer = %q", got.Answer)
	}
}

func TestRunStructuredInto(t *testing.T) {
	type payload struct {
		Answer string `json:"answer"`
	}

	provider := &captureStructuredProvider{}
	agent := New(Config{
		ModelConfig: ModelConfig{
			Name:           "structured",
			Model:          "stub",
			Provider:       provider,
			ResponseFormat: NewJSONObjectResponseFormat(),
		},
	})

	var got payload
	raw, err := RunStructuredInto(context.Background(), agent, "say hi", &got)
	if err != nil {
		t.Fatal(err)
	}
	if raw != `{"answer":"ok"}` {
		t.Fatalf("raw = %q", raw)
	}
	if got.Answer != "ok" {
		t.Fatalf("answer = %q", got.Answer)
	}
}

func TestContinueStructuredInto(t *testing.T) {
	type payload struct {
		Answer string `json:"answer"`
	}

	provider := &captureStructuredProvider{}
	agent := New(Config{
		ModelConfig: ModelConfig{
			Name:           "structured",
			Model:          "stub",
			Provider:       provider,
			ResponseFormat: NewJSONObjectResponseFormat(),
		},
	})

	agent.State().AddMessage(Message{Role: RoleUser, Content: "continue"})

	var got payload
	raw, err := ContinueStructuredInto(context.Background(), agent, &got)
	if err != nil {
		t.Fatal(err)
	}
	if raw != `{"answer":"ok"}` {
		t.Fatalf("raw = %q", raw)
	}
	if got.Answer != "ok" {
		t.Fatalf("answer = %q", got.Answer)
	}
}

func TestAgentRun_PersistsAssistantBlocks(t *testing.T) {
	provider := &captureStructuredProvider{}
	agent := New(Config{
		ModelConfig: ModelConfig{
			Name:           "structured",
			Model:          "stub",
			Provider:       provider,
			ResponseFormat: NewJSONObjectResponseFormat(),
		},
	})

	if _, err := agent.Run(context.Background(), "say hi"); err != nil {
		t.Fatal(err)
	}

	msgs := agent.State().Messages()
	if len(msgs) < 2 {
		t.Fatalf("messages len = %d", len(msgs))
	}
	last := msgs[len(msgs)-1]
	if last.Role != RoleAssistant {
		t.Fatalf("last role = %q", last.Role)
	}
	if len(last.Blocks) != 1 {
		t.Fatalf("blocks len = %d", len(last.Blocks))
	}
	if last.Blocks[0].Kind != BlockKindText || last.Blocks[0].Text != `{"answer":"ok"}` {
		t.Fatalf("block = %#v", last.Blocks[0])
	}
}

func TestAgentRunStreaming_AggregatesAssistantBlocks(t *testing.T) {
	agent := New(Config{
		ModelConfig: ModelConfig{
			Name:      "streaming-blocks",
			Model:     "stub",
			Provider:  streamingBlocksProvider{},
			Streaming: true,
		},
	})

	out, err := agent.Run(context.Background(), "stream")
	if err != nil {
		t.Fatal(err)
	}
	if out != "hello" {
		t.Fatalf("output = %q", out)
	}

	msgs := agent.State().Messages()
	last := msgs[len(msgs)-1]
	if len(last.Blocks) != 1 {
		t.Fatalf("blocks len = %d", len(last.Blocks))
	}
	if last.Blocks[0].Kind != BlockKindText || last.Blocks[0].Text != "hello" {
		t.Fatalf("block = %#v", last.Blocks[0])
	}
}
