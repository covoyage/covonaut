package agentcore

import (
	"context"
	"fmt"
	"strings"

	"github.com/covoyage/covonaut/skill"
)

type ThreadConfigProvider interface {
	GetThreadConfig(ctx context.Context, threadID string) (*CallConfig, bool, error)
}

type LoadAgentOptions struct {
	ThreadID string
	CallCfg  *CallConfig
	ThreadCfgProvider ThreadConfigProvider
}

func LoadAgent(ctx context.Context, cfg Config, opts LoadAgentOptions) (*Agent, error) {
	effective := &CallConfig{
		Model:          cfg.Model,
		ResponseFormat: CloneResponseFormat(cfg.ResponseFormat),
		Thinking:       CloneThinkingConfig(cfg.Thinking),
		Skills:         CloneStringSlice(cfg.SelectedSkills),
	}

	if opts.ThreadID != "" && opts.ThreadCfgProvider != nil {
		threadCfg, hasCfg, err := opts.ThreadCfgProvider.GetThreadConfig(ctx, opts.ThreadID)
		if err != nil {
			return nil, err
		}
		if hasCfg {
			effective = MergeCallConfig(effective, threadCfg)
		}
	}

	effective = MergeCallConfig(effective, opts.CallCfg)

	if effective != nil {
		if effective.Model != "" {
			cfg.Model = effective.Model
		}
		cfg.ResponseFormat = CloneResponseFormat(effective.ResponseFormat)
		cfg.Thinking = CloneThinkingConfig(effective.Thinking)
		cfg.SelectedSkills = CloneStringSlice(effective.Skills)
	}

	if len(cfg.AvailableSkills) > 0 && len(cfg.SelectedSkills) > 0 {
		_, missing := skill.ResolveSelection(cfg.AvailableSkills, cfg.SelectedSkills)
		if len(missing) > 0 {
			return nil, fmt.Errorf("unknown skills: %s", strings.Join(missing, ", "))
		}
	}

	if opts.ThreadID != "" && cfg.Checkpoint != nil {
		cp := *cfg.Checkpoint
		cp.ThreadID = opts.ThreadID
		cfg.Checkpoint = &cp
	}

	agent := New(cfg)

	if opts.ThreadID == "" || cfg.Store == nil {
		return agent, nil
	}

	hasState, err := cfg.Store.Has(ctx, opts.ThreadID)
	if err != nil {
		return nil, err
	}
	if !hasState {
		return agent, nil
	}
	if err := agent.LoadState(ctx, opts.ThreadID); err != nil {
		return nil, err
	}
	return agent, nil
}
