package agentcore

import (
	"context"
	"strings"
	"testing"

	"github.com/covoyage/covonaut/skill"
)

type modelSelectSkillProvider struct {
	requests []*ProviderRequest
}

func (p *modelSelectSkillProvider) Complete(ctx context.Context, req *ProviderRequest) (*ProviderResponse, error) {
	cp := *req
	cp.Messages = append([]Message(nil), req.Messages...)
	p.requests = append(p.requests, &cp)
	if len(p.requests) == 1 {
		return &ProviderResponse{Content: "/skill:planner gather requirements"}, nil
	}
	return &ProviderResponse{Content: "final answer"}, nil
}

func (p *modelSelectSkillProvider) Stream(ctx context.Context, req *ProviderRequest) (<-chan StreamDelta, error) {
	ch := make(chan StreamDelta)
	close(ch)
	return ch, nil
}

func TestMergeCallConfig_OverridesSkills(t *testing.T) {
	base := &CallConfig{
		Model:  "base",
		Skills: []string{"planner"},
	}
	override := &CallConfig{
		Model:  "override",
		Skills: []string{"debugger", "writer"},
	}
	merged := MergeCallConfig(base, override)
	if merged.Model != "override" {
		t.Fatalf("model = %q", merged.Model)
	}
	if len(merged.Skills) != 2 || merged.Skills[0] != "debugger" || merged.Skills[1] != "writer" {
		t.Fatalf("skills = %#v", merged.Skills)
	}
	override.Skills[0] = "changed"
	if merged.Skills[0] != "debugger" {
		t.Fatalf("expected cloned skills slice, got %#v", merged.Skills)
	}
}

func TestAgentRun_ExpandsSkillsInPromptAndExplicitInvocation(t *testing.T) {
	provider := &captureStructuredProvider{}
	agent := New(Config{
		ModelConfig: ModelConfig{
			Name:     "skills",
			Model:    "stub",
			Provider: provider,
		},
		SkillConfig: SkillConfig{
			AvailableSkills: []skill.Skill{
				{
					Name:        "planner",
					Description: "Plans work",
					FilePath:    "/skills/planner/SKILL.md",
					BaseDir:     "/skills/planner",
					Body:        "Plan carefully.",
				},
				{
					Name:                   "debugger",
					Description:            "Debugs failures",
					FilePath:               "/skills/debugger/SKILL.md",
					BaseDir:                "/skills/debugger",
					Body:                   "Inspect logs first.",
					DisableModelInvocation: true,
				},
			},
			SelectedSkills: []string{"planner"},
		},
	})
	var loaded []SkillLoadedEvent
	agent.On(EventSkillLoaded, func(e Event) {
		ev, ok := e.(SkillLoadedEvent)
		if ok {
			loaded = append(loaded, ev)
		}
		if ev, ok := e.(*SkillLoadedEvent); ok {
			loaded = append(loaded, *ev)
		}
	})

	if _, err := agent.Run(context.Background(), "/skill:debugger trace the outage"); err != nil {
		t.Fatal(err)
	}
	if provider.lastRequest == nil {
		t.Fatal("expected captured request")
	}
	var joined []string
	for _, msg := range provider.lastRequest.Messages {
		joined = append(joined, msg.Content)
	}
	body := strings.Join(joined, "\n---\n")
	if !strings.Contains(body, "<available_skills>") {
		t.Fatalf("missing skill index in request: %s", body)
	}
	if !strings.Contains(body, "<active_skills>") || !strings.Contains(body, "Plan carefully.") {
		t.Fatalf("missing active skill prompt: %s", body)
	}
	if !strings.Contains(body, `<skill name="debugger"`) || !strings.Contains(body, "User: trace the outage") {
		t.Fatalf("missing explicit skill expansion: %s", body)
	}
	if len(loaded) != 1 || loaded[0].SkillName != "debugger" || loaded[0].Source != "explicit_command" {
		t.Fatalf("loaded events = %#v", loaded)
	}
}

func TestAgentRun_ModelSelectedSkillTriggersSecondTurn(t *testing.T) {
	provider := &modelSelectSkillProvider{}
	agent := New(Config{
		ModelConfig: ModelConfig{
			Name:     "skills",
			Model:    "stub",
			Provider: provider,
		},
		SkillConfig: SkillConfig{
			AvailableSkills: []skill.Skill{
				{
					Name:        "planner",
					Description: "Plans work",
					FilePath:    "/skills/planner/SKILL.md",
					BaseDir:     "/skills/planner",
					Body:        "Plan carefully.",
				},
			},
		},
	})
	var loaded []SkillLoadedEvent
	agent.On(EventSkillLoaded, func(e Event) {
		ev, ok := e.(SkillLoadedEvent)
		if ok {
			loaded = append(loaded, ev)
		}
		if ev, ok := e.(*SkillLoadedEvent); ok {
			loaded = append(loaded, *ev)
		}
	})

	out, err := agent.Run(context.Background(), "help me plan")
	if err != nil {
		t.Fatal(err)
	}
	if out != "final answer" {
		t.Fatalf("output = %q", out)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("request count = %d", len(provider.requests))
	}
	var joined []string
	for _, msg := range provider.requests[1].Messages {
		joined = append(joined, msg.Content)
	}
	secondTurn := strings.Join(joined, "\n---\n")
	if !strings.Contains(secondTurn, "Plan carefully.") || !strings.Contains(secondTurn, "User: gather requirements") {
		t.Fatalf("second turn missing loaded skill: %s", secondTurn)
	}

	for _, msg := range agent.State().Messages() {
		if msg.Role == RoleAssistant && strings.Contains(msg.Content, "/skill:planner") {
			t.Fatalf("intermediate skill command should not persist: %#v", msg)
		}
	}
	if len(loaded) != 1 || loaded[0].SkillName != "planner" || loaded[0].Source != skillMetadataSourceModel {
		t.Fatalf("loaded events = %#v", loaded)
	}
}
