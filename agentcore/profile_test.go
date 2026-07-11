package agentcore

import (
	"strings"
	"testing"
)

func TestApplyModelProfile_NoProfile(t *testing.T) {
	ResetProfilesForTest()
	cfg := NewConfig(WithModel("unknown-model"), WithSystemPrompt("hello"), WithTools(&Tool{Name: "a"}))
	out := ApplyModelProfile(cfg)
	if out.SystemPrompt != "hello" {
		t.Fatalf("system prompt changed: %q", out.SystemPrompt)
	}
	if len(out.Tools) != 1 {
		t.Fatalf("tools changed: %d", len(out.Tools))
	}
}

func TestApplyModelProfile_SystemPromptSuffix(t *testing.T) {
	ResetProfilesForTest()
	RegisterProfile(ModelProfile{
		Name:               "test-model",
		SystemPromptSuffix: "Be concise.",
	})
	cfg := NewConfig(WithModel("test-model"), WithSystemPrompt("You are helpful."))
	out := ApplyModelProfile(cfg)
	want := "You are helpful.\n\nBe concise."
	if out.SystemPrompt != want {
		t.Fatalf("got %q want %q", out.SystemPrompt, want)
	}
}

func TestApplyModelProfile_SuffixOnEmptyPrompt(t *testing.T) {
	ResetProfilesForTest()
	RegisterProfile(ModelProfile{
		Name:               "test-model",
		SystemPromptSuffix: "Be concise.",
	})
	cfg := NewConfig(WithModel("test-model"))
	out := ApplyModelProfile(cfg)
	if out.SystemPrompt != "Be concise." {
		t.Fatalf("got %q", out.SystemPrompt)
	}
}

func TestApplyModelProfile_ExcludedTools(t *testing.T) {
	ResetProfilesForTest()
	RegisterProfile(ModelProfile{
		Name:          "test-model",
		ExcludedTools: []string{"bash", "delete"},
	})
	cfg := NewConfig(
		WithModel("test-model"),
		WithTools(&Tool{Name: "read"}, &Tool{Name: "bash"}, &Tool{Name: "edit"}, &Tool{Name: "delete"}),
	)
	out := ApplyModelProfile(cfg)
	if len(out.Tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(out.Tools))
	}
	names := []string{out.Tools[0].Name, out.Tools[1].Name}
	if names[0] != "read" || names[1] != "edit" {
		t.Fatalf("got %v want [read edit]", names)
	}
}

func TestApplyModelProfile_ExcludedToolsNoTools(t *testing.T) {
	ResetProfilesForTest()
	RegisterProfile(ModelProfile{
		Name:          "test-model",
		ExcludedTools: []string{"bash"},
	})
	cfg := NewConfig(WithModel("test-model"))
	out := ApplyModelProfile(cfg)
	if len(out.Tools) != 0 {
		t.Fatalf("got %d tools, want 0", len(out.Tools))
	}
}

func TestApplyModelProfile_TemperatureOverrideWhenUnset(t *testing.T) {
	ResetProfilesForTest()
	temp := 0.7
	RegisterProfile(ModelProfile{
		Name:        "test-model",
		Temperature: &temp,
	})
	cfg := NewConfig(WithModel("test-model"))
	out := ApplyModelProfile(cfg)
	if out.Temperature != 0.7 {
		t.Fatalf("got %v want 0.7", out.Temperature)
	}
}

func TestApplyModelProfile_TemperatureNotOverrideWhenSet(t *testing.T) {
	ResetProfilesForTest()
	temp := 0.7
	RegisterProfile(ModelProfile{
		Name:        "test-model",
		Temperature: &temp,
	})
	cfg := NewConfig(WithModel("test-model"), WithTemperature(0.3))
	out := ApplyModelProfile(cfg)
	if out.Temperature != 0.3 {
		t.Fatalf("got %v want 0.3 (user value preserved)", out.Temperature)
	}
}

func TestApplyModelProfile_MaxTurnsOverrideWhenUnset(t *testing.T) {
	ResetProfilesForTest()
	maxTurns := int64(5)
	RegisterProfile(ModelProfile{
		Name:     "test-model",
		MaxTurns: &maxTurns,
	})
	cfg := NewConfig(WithModel("test-model"))
	out := ApplyModelProfile(cfg)
	if out.MaxTurns != 5 {
		t.Fatalf("got %d want 5", out.MaxTurns)
	}
}

func TestApplyModelProfile_MaxTurnsNotOverrideWhenSet(t *testing.T) {
	ResetProfilesForTest()
	maxTurns := int64(5)
	RegisterProfile(ModelProfile{
		Name:     "test-model",
		MaxTurns: &maxTurns,
	})
	cfg := NewConfig(WithModel("test-model"), WithMaxTurns(10))
	out := ApplyModelProfile(cfg)
	if out.MaxTurns != 10 {
		t.Fatalf("got %d want 10 (user value preserved)", out.MaxTurns)
	}
}

func TestApplyModelProfile_EmptyModelSkipped(t *testing.T) {
	ResetProfilesForTest()
	RegisterProfile(ModelProfile{
		Name:               "test-model",
		SystemPromptSuffix: "should not apply",
	})
	cfg := NewConfig(WithSystemPrompt("base"))
	out := ApplyModelProfile(cfg)
	if out.SystemPrompt != "base" {
		t.Fatalf("profile applied with empty model: %q", out.SystemPrompt)
	}
}

func TestRegisterProfile_EmptyNameIgnored(t *testing.T) {
	ResetProfilesForTest()
	RegisterProfile(ModelProfile{Name: "", SystemPromptSuffix: "x"})
	if _, ok := LookupProfile(""); ok {
		t.Fatal("empty-name profile should not register")
	}
}

func TestRegisterProfile_Replaces(t *testing.T) {
	ResetProfilesForTest()
	RegisterProfile(ModelProfile{Name: "m", SystemPromptSuffix: "v1"})
	RegisterProfile(ModelProfile{Name: "m", SystemPromptSuffix: "v2"})
	p, ok := LookupProfile("m")
	if !ok || p.SystemPromptSuffix != "v2" {
		t.Fatalf("expected v2, got %q ok=%v", p.SystemPromptSuffix, ok)
	}
}

func TestLookupProfile_NotFound(t *testing.T) {
	ResetProfilesForTest()
	_, ok := LookupProfile("nope")
	if ok {
		t.Fatal("expected not found")
	}
}

func TestNewAppliesProfile(t *testing.T) {
	ResetProfilesForTest()
	RegisterProfile(ModelProfile{
		Name:               "profiled-model",
		SystemPromptSuffix: "extra guidance",
	})
	defer ResetProfilesForTest()

	agent := New(NewConfig(WithModel("profiled-model"), WithSystemPrompt("base")))
	defer agent.Close()
	if !strings.Contains(agent.Config().SystemPrompt, "extra guidance") {
		t.Fatalf("New did not apply profile: %q", agent.Config().SystemPrompt)
	}
	if !strings.Contains(agent.Config().SystemPrompt, "base") {
		t.Fatalf("New overwrote base prompt: %q", agent.Config().SystemPrompt)
	}
}
