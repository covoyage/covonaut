package agentcore

import (
	"context"
	"fmt"
	"strings"

	"github.com/covoyage/covonaut/skill"
)

const skillExtensionName = "skills"
const skillMetadataNameKey = "skill_name"
const skillMetadataSourceKey = "skill_source"
const skillMetadataSourceModel = "model_selection"

type skillExtension struct {
	BaseLifecycleHook
	agent    *Agent
	skills   []skill.Skill
	selected []string
}

// NewSkillExtension exposes discovered skills to the model and expands
// selected or explicit skill invocations at request time.
func NewSkillExtension(skills []skill.Skill, selected []string) Extension {
	cp := make([]skill.Skill, len(skills))
	copy(cp, skills)
	return &skillExtension{
		skills:   cp,
		selected: CloneStringSlice(selected),
	}
}

func (e *skillExtension) Name() string { return skillExtensionName }
func (e *skillExtension) Init(_ context.Context, agent *Agent) error {
	e.agent = agent
	return nil
}
func (*skillExtension) Dispose() error                 { return nil }
func (e *skillExtension) LifecycleHook() LifecycleHook { return e }

func (e *skillExtension) SystemPromptSuffix() string {
	return skill.Index(e.skills)
}

// SetSelected updates the set of selected skills at runtime.
func (e *skillExtension) SetSelected(selected []string) {
	e.selected = selected
}

func (e *skillExtension) TransformContext(_ context.Context, msgs []Message) []Message {
	out := make([]Message, 0, len(msgs)+1)
	if selected, _ := skill.ResolveSelection(e.skills, e.selected); len(selected) > 0 {
		out = append(out, Message{
			Role:    RoleSystem,
			Content: skill.ActivePrompt(selected),
		})
	}
	for i, msg := range msgs {
		if msg.Role == RoleUser {
			if cmd, ok := skill.ParseCommand(msg.Content); ok {
				if item, found := skill.FindByName(e.skills, cmd.Name); found {
					if i == len(msgs)-1 {
						e.emitSkillLoaded(item, "explicit_command", cmd.Args)
					}
					msg.Content = skill.ExplicitInvocation(item, cmd.Args)
				} else {
					msg.Content = fmt.Sprintf("Requested skill %q was not found. Available skills: %s", cmd.Name, strings.Join(e.availableNames(), ", "))
				}
			}
		}
		out = append(out, msg)
	}
	return out
}

func (e *skillExtension) AfterModelCall(_ context.Context, arc *AgentRunContext, mcc *ModelCallContext) {
	if mcc == nil || mcc.Err != nil || mcc.Response == nil || len(mcc.Response.ToolCalls) > 0 {
		return
	}
	cmd, ok := skill.ParseCommand(mcc.Response.Content)
	if !ok {
		return
	}
	item, found := skill.FindByName(e.skills, cmd.Name)
	if !found || item.DisableModelInvocation {
		return
	}
	e.emitSkillLoaded(item, skillMetadataSourceModel, cmd.Args)

	if loadedSkillNames(arc.Messages)[item.Name] {
		arc.Agent.FollowUp(Message{
			Role:    RoleSystem,
			Content: fmt.Sprintf("Skill %q is already loaded. Continue using it to solve the current task and provide the final answer.", item.Name),
			Metadata: map[string]any{
				skillMetadataNameKey:   item.Name,
				skillMetadataSourceKey: skillMetadataSourceModel,
			},
		})
	} else {
		arc.Agent.FollowUp(Message{
			Role:    RoleSystem,
			Content: skill.ExplicitInvocation(item, cmd.Args),
			Metadata: map[string]any{
				skillMetadataNameKey:   item.Name,
				skillMetadataSourceKey: skillMetadataSourceModel,
			},
		})
	}

	mcc.Response.Content = ""
	mcc.Response.Blocks = nil
	mcc.Response.Structured = nil
	mcc.Response.SuppressPersist = true
}

func (e *skillExtension) availableNames() []string {
	names := make([]string, 0, len(e.skills))
	for _, item := range e.skills {
		names = append(names, item.Name)
	}
	return names
}

func loadedSkillNames(msgs []Message) map[string]bool {
	out := make(map[string]bool)
	for _, msg := range msgs {
		if msg.Metadata == nil {
			continue
		}
		name, _ := msg.Metadata[skillMetadataNameKey].(string)
		source, _ := msg.Metadata[skillMetadataSourceKey].(string)
		if name == "" || source != skillMetadataSourceModel {
			continue
		}
		out[name] = true
	}
	return out
}

func (e *skillExtension) emitSkillLoaded(item skill.Skill, source, args string) {
	if e.agent == nil {
		return
	}
	e.agent.EmitEvent(SkillLoadedEvent{
		baseEvent: newBase(EventSkillLoaded),
		SkillName: item.Name,
		Path:      item.FilePath,
		Source:    source,
		Arguments: strings.TrimSpace(args),
	})
}
