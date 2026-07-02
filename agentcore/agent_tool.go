package agentcore

import (
	"context"
	"encoding/json"
	"fmt"
)

// AgentAsTool wraps an Agent config as a Tool that can be registered with
// a parent agent. The sub-agent runs a full conversation loop when invoked,
// producing a result string. Events from the sub-agent are forwarded to the
// parent's EventBus for unified observability.
//
// Parameters schema:
//
//	{"type": "object", "properties": {"input": {"type": "string", "description": "The task to delegate"}}, "required": ["input"]}
func AgentAsTool(cfg Config) *Tool {
	name := cfg.Name
	if name == "" {
		name = "sub_agent"
	}
	description := fmt.Sprintf("Delegate a task to the %q agent", name)

	return &Tool{
		Name:        name,
		Description: description,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"input": map[string]any{
					"type":        "string",
					"description": "The task or question to delegate to the sub-agent",
				},
			},
			"required": []any{"input"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Input string `json:"input"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments for agent tool %s: %w", name, err)
			}
			if params.Input == "" {
				return nil, fmt.Errorf("agent tool %s requires non-empty input", name)
			}
			agent := New(cfg)
			result, err := agent.Run(ctx, params.Input)
			if err != nil {
				return nil, WrapNodeError(err, "agent_tool:"+name)
			}
			return result, nil
		},
	}
}

// AgentAsToolWithEventBus is like AgentAsTool but forwards the sub-agent's
// events to the given EventBus for centralized observability.
func AgentAsToolWithEventBus(cfg Config, parentBus *EventBus) *Tool {
	tool := AgentAsTool(cfg)
	name := cfg.Name
	if name == "" {
		name = "sub_agent"
	}

	tool.Func = func(ctx context.Context, args json.RawMessage) (any, error) {
		var params struct {
			Input string `json:"input"`
		}
		if err := json.Unmarshal(args, &params); err != nil {
			return nil, fmt.Errorf("invalid arguments for agent tool %s: %w", name, err)
		}
		if params.Input == "" {
			return nil, fmt.Errorf("agent tool %s requires non-empty input", name)
		}

		agent := New(cfg)
		if parentBus != nil {
			agent.SetEventBus(parentBus)
		}
		result, err := agent.Run(ctx, params.Input)
		if err != nil {
			return nil, WrapNodeError(err, "agent_tool:"+name)
		}
		return result, nil
	}
	return tool
}
