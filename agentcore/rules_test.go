package agentcore

import (
	"context"
	"os"
	"strings"
	"testing"
)

type memReader map[string][]byte

func (m memReader) ReadFile(name string) ([]byte, error) {
	if data, ok := m[name]; ok {
		return data, nil
	}
	return nil, os.ErrNotExist
}

func TestRulesExtension_DefaultPath(t *testing.T) {
	reader := memReader{"AGENTS.md": []byte("# Rules\nBe concise.")}
	ext := NewRulesExtension(WithRulesReader(reader))
	if err := ext.Init(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ext.SystemPromptSuffix(), "Be concise.") {
		t.Fatalf("suffix=%q", ext.SystemPromptSuffix())
	}
}

func TestRulesExtension_CustomPaths(t *testing.T) {
	reader := memReader{
		".cursorrules": []byte("rule A"),
		"AGENTS.md":    []byte("rule B"),
	}
	ext := NewRulesExtension(WithRulesPaths(".cursorrules", "AGENTS.md"), WithRulesReader(reader))
	if err := ext.Init(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	s := ext.SystemPromptSuffix()
	if !strings.Contains(s, "rule A") || !strings.Contains(s, "rule B") {
		t.Fatalf("suffix=%q", s)
	}
	if !strings.HasPrefix(s, "rule A") {
		t.Fatalf("expected rule A first, got %q", s)
	}
}

func TestRulesExtension_MissingFilesSkipped(t *testing.T) {
	reader := memReader{"AGENTS.md": []byte("only this exists")}
	ext := NewRulesExtension(WithRulesPaths("missing.txt", "AGENTS.md", "also-missing.md"), WithRulesReader(reader))
	if err := ext.Init(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if ext.SystemPromptSuffix() != "only this exists" {
		t.Fatalf("suffix=%q", ext.SystemPromptSuffix())
	}
}

func TestRulesExtension_AllMissing(t *testing.T) {
	reader := memReader{}
	ext := NewRulesExtension(WithRulesPaths("a.md", "b.md"), WithRulesReader(reader))
	if err := ext.Init(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if ext.SystemPromptSuffix() != "" {
		t.Fatalf("expected empty suffix, got %q", ext.SystemPromptSuffix())
	}
}

func TestRulesExtension_EmptyFileSkipped(t *testing.T) {
	reader := memReader{"a.md": []byte("   \n\n  "), "b.md": []byte("real rule")}
	ext := NewRulesExtension(WithRulesPaths("a.md", "b.md"), WithRulesReader(reader))
	if err := ext.Init(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if ext.SystemPromptSuffix() != "real rule" {
		t.Fatalf("suffix=%q", ext.SystemPromptSuffix())
	}
}

func TestRulesExtension_IntegrationWithAgent(t *testing.T) {
	reader := memReader{"AGENTS.md": []byte("Always be helpful.")}
	ext := NewRulesExtension(WithRulesReader(reader))
	agent := New(NewConfig(WithSystemPrompt("base prompt"), WithExtensions(ext)))
	defer agent.Close()
	sp := agent.Config().SystemPrompt
	if !strings.Contains(sp, "base prompt") {
		t.Fatalf("base prompt lost: %q", sp)
	}
	if !strings.Contains(sp, "Always be helpful.") {
		t.Fatalf("rules not appended: %q", sp)
	}
}

func TestRulesExtension_IntegrationEmptyBasePrompt(t *testing.T) {
	reader := memReader{"AGENTS.md": []byte("Only rules.")}
	ext := NewRulesExtension(WithRulesReader(reader))
	agent := New(NewConfig(WithExtensions(ext)))
	defer agent.Close()
	sp := agent.Config().SystemPrompt
	if sp != "Only rules." {
		t.Fatalf("expected rules only, got %q", sp)
	}
}

func TestRulesExtension_ImplementsInterfaces(t *testing.T) {
	ext := NewRulesExtension()
	var _ Extension = ext
	var _ SystemPromptProvider = ext
}
