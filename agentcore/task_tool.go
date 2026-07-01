package agentcore

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// TaskOption describes a sub-agent available through TaskTool.
type TaskOption struct {
	Name        string
	Description string
	Tool        *Tool
}

// TaskTool creates a meta-tool that lets an LLM delegate work to any of the
// given sub-agents via a single tool call. The LLM selects a sub-agent by name
// and provides a task string; the tool routes the call to the matching
// sub-agent tool.
//
// Each sub-agent's task parameter is automatically wrapped as {"input": "..."}
// so that tools created via AgentAsTool work out of the box.
//
// Usage:
//
//	tt := TaskTool("delegate", []TaskOption{
//	    {Name: "coder", Description: "Writes Go code", Tool: AgentAsTool(coderCfg)},
//	    {Name: "reviewer", Description: "Reviews Go code", Tool: AgentAsTool(reviewCfg)},
//	})
//
//	agent := New(Config{Tools: []*Tool{tt, readFile, writeFile}})
func TaskTool(name string, options []TaskOption) *Tool {
	// Sort by name so enum and description ordering are consistent.
	sorted := make([]TaskOption, len(options))
	copy(sorted, options)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Name < sorted[j].Name
	})

	enumVals := make([]string, len(sorted))
	var sb strings.Builder
	for i, opt := range sorted {
		enumVals[i] = opt.Name
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(opt.Name)
		sb.WriteString(": ")
		sb.WriteString(opt.Description)
	}

	lookup := make(map[string]*Tool, len(sorted))
	for _, opt := range sorted {
		lookup[opt.Name] = opt.Tool
	}

	return &Tool{
		Name:        name,
		Description: "Delegate a task to a specialized sub-agent",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"agent": map[string]any{
					"type":        "string",
					"enum":        enumVals,
					"description": sb.String(),
				},
				"task": map[string]any{
					"type":        "string",
					"description": "The task to delegate to the sub-agent",
				},
			},
			"required": []any{"agent", "task"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Agent string `json:"agent"`
				Task  string `json:"task"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, WrapNodeError(
					fmt.Errorf("invalid args: %w", err),
					"task_tool:"+name,
				)
			}
			if params.Agent == "" {
				return nil, WrapNodeError(
					fmt.Errorf("'agent' is required"),
					"task_tool:"+name,
				)
			}
			if params.Task == "" {
				return nil, WrapNodeError(
					fmt.Errorf("'task' is required"),
					"task_tool:"+name,
				)
			}
			subTool, ok := lookup[params.Agent]
			if !ok {
				known := strings.Join(enumVals, ", ")
				return nil, WrapNodeError(
					fmt.Errorf("unknown agent %q (known: %s)", params.Agent, known),
					"task_tool:"+name,
				)
			}
			wrapped, err := json.Marshal(map[string]string{"input": params.Task})
			if err != nil {
				return nil, WrapNodeError(
					fmt.Errorf("marshal args: %w", err),
					"task_tool:"+name,
				)
			}
			result, err := subTool.Func(ctx, wrapped)
			if err != nil {
				return nil, WrapNodeError(err, "task_tool:"+name)
			}
			return result, nil
		},
	}
}

// TaskToolFromConfigs is a convenience wrapper that creates AgentAsTool
// instances from each Config and then bundles them into a single TaskTool.
func TaskToolFromConfigs(name string, configs []Config) *Tool {
	options := make([]TaskOption, len(configs))
	for i, cfg := range configs {
		cfg := cfg
		name := cfg.Name
		if name == "" {
			name = fmt.Sprintf("sub_agent_%d", i)
			cfg.Name = name
		}
		options[i] = TaskOption{
			Name: name,
			Description: fmt.Sprintf("Delegate to the %q sub-agent", name),
			Tool: AgentAsTool(cfg),
		}
	}
	return TaskTool(name, options)
}
