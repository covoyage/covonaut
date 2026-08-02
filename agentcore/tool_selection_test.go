package agentcore

import (
	"context"
	"strings"
	"testing"
)

type toolSelectionProvider struct {
	tools []ToolDefinition
}

func (p *toolSelectionProvider) Complete(_ context.Context, req *ProviderRequest) (*ProviderResponse, error) {
	p.tools = append([]ToolDefinition(nil), req.Tools...)
	return &ProviderResponse{Content: "done"}, nil
}

func (p *toolSelectionProvider) Stream(context.Context, *ProviderRequest) (<-chan StreamDelta, error) {
	return nil, nil
}

func TestSelectToolDefinitionsLimitsVisibility(t *testing.T) {
	tools := []ToolDefinition{
		{Name: "calculator", Description: "calculate numbers"},
		{Name: "weather", Description: "lookup weather forecast"},
		{Name: "status", Description: "always available"},
	}
	got, err := selectToolDefinitions(context.Background(), &ToolSelectionConfig{
		MaxVisible:    1,
		AlwaysVisible: []string{"status"},
	}, []Message{{Role: RoleUser, Content: "please lookup the weather forecast"}}, tools)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "status" && got[1].Name != "status" {
		t.Fatalf("selected tools = %#v", got)
	}
	foundWeather := false
	for _, tool := range got {
		foundWeather = foundWeather || tool.Name == "weather"
	}
	if !foundWeather {
		t.Fatalf("selected tools = %#v, missing weather", got)
	}
}

func TestAgentAppliesToolSelectionToProviderRequest(t *testing.T) {
	provider := &toolSelectionProvider{}
	agent := New(StubConfig(provider,
		WithTools(
			&Tool{Name: "calculator", Description: "calculate numbers"},
			&Tool{Name: "weather", Description: "lookup weather forecast"},
		),
		WithToolSelection(&ToolSelectionConfig{MaxVisible: 1}),
	))
	if _, err := agent.Run(context.Background(), "lookup weather forecast"); err != nil {
		t.Fatal(err)
	}
	if len(provider.tools) != 1 || provider.tools[0].Name != "weather" {
		t.Fatalf("provider tools = %#v", provider.tools)
	}
}

func TestKeywordToolSelectorMatchesChineseTags(t *testing.T) {
	selector := KeywordToolSelector{}
	names, err := selector.SelectTools(context.Background(), ToolSelectionContext{
		Messages: []Message{{Role: RoleUser, Content: "查询明天北京天气"}},
		Tools: []ToolDefinition{
			{Name: "forecast", SearchTerms: []string{"天气", "气象"}},
			{Name: "calculator", SearchTerms: []string{"数学"}},
		},
		Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "forecast" {
		t.Fatalf("selected = %#v", names)
	}
}

func TestEmbeddingToolSelectorCachesToolVectors(t *testing.T) {
	toolEmbeddingCalls := 0
	selector := &EmbeddingToolSelector{
		MinScore: 0.5,
		Embed: func(_ context.Context, texts []string) ([][]float64, error) {
			vectors := make([][]float64, len(texts))
			if len(texts) > 1 {
				toolEmbeddingCalls++
			}
			for i, text := range texts {
				if strings.Contains(text, "weather") {
					vectors[i] = []float64{1, 0}
				} else {
					vectors[i] = []float64{0, 1}
				}
			}
			return vectors, nil
		},
	}
	selection := ToolSelectionContext{
		Messages: []Message{{Role: RoleUser, Content: "weather"}},
		Tools: []ToolDefinition{
			{Name: "weather", Description: "weather forecast"},
			{Name: "calculator", Description: "math"},
		},
		Limit: 1,
	}
	for i := 0; i < 2; i++ {
		names, err := selector.SelectTools(context.Background(), selection)
		if err != nil {
			t.Fatal(err)
		}
		if len(names) != 1 || names[0] != "weather" {
			t.Fatalf("selected = %#v", names)
		}
	}
	if toolEmbeddingCalls != 1 {
		t.Fatalf("tool embedding calls = %d, want 1", toolEmbeddingCalls)
	}
}

func TestEmbeddingToolSelectorFallsBackWhenNoToolMeetsThreshold(t *testing.T) {
	selector := &EmbeddingToolSelector{
		MinScore: 0.9,
		Embed: func(_ context.Context, texts []string) ([][]float64, error) {
			vectors := make([][]float64, len(texts))
			for i, text := range texts {
				if text == "query-weather" {
					vectors[i] = []float64{1, 0}
				} else {
					vectors[i] = []float64{0, 1}
				}
			}
			return vectors, nil
		},
		Fallback: ToolSelectorFunc(func(_ context.Context, _ ToolSelectionContext) ([]string, error) {
			return []string{"keyword-match"}, nil
		}),
	}
	names, err := selector.SelectTools(context.Background(), ToolSelectionContext{
		Messages: []Message{{Role: RoleUser, Content: "query-weather"}},
		Tools:    []ToolDefinition{{Name: "weather"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "keyword-match" {
		t.Fatalf("selected = %#v", names)
	}
}

func TestEmbeddingToolSelectorCacheEvictionKeepsCurrentWorkingSet(t *testing.T) {
	embeddedTexts := make(map[string]int)
	selector := &EmbeddingToolSelector{
		MaxCacheEntries: 2,
		Embed: func(_ context.Context, texts []string) ([][]float64, error) {
			vectors := make([][]float64, len(texts))
			for i, text := range texts {
				embeddedTexts[text]++
				vectors[i] = []float64{float64(len(text)), float64(i + 1)}
			}
			return vectors, nil
		},
	}
	first := []ToolDefinition{{Name: "alpha"}, {Name: "beta"}}
	if _, err := selector.toolVectors(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	current := []ToolDefinition{{Name: "alpha"}, {Name: "beta"}, {Name: "gamma"}}
	vectors, err := selector.toolVectors(context.Background(), current)
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 3 {
		t.Fatalf("vectors = %#v", vectors)
	}
	for i, vector := range vectors {
		if len(vector) != 2 {
			t.Fatalf("vector %d lost during eviction: %#v", i, vector)
		}
	}
	if _, err := selector.toolVectors(context.Background(), current); err != nil {
		t.Fatal(err)
	}
	if embeddedTexts[toolSelectionDocument(current[0])] != 1 || embeddedTexts[toolSelectionDocument(current[1])] != 1 {
		t.Fatalf("hot tools were re-embedded: %#v", embeddedTexts)
	}
}
