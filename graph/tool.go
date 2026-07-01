package graph

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/covoyage/covonaut/agentcore"
)

// ToolConfig provides metadata for wrapping a graph or runner as an agent tool.
type ToolConfig struct {
	Name        string
	Description string
	InputDesc   string
}

func (c ToolConfig) inputDesc() string {
	if c.InputDesc != "" {
		return c.InputDesc
	}
	return "The input to pass to the pipeline"
}

// AsTool wraps the CompiledGraph as an agentcore.Tool so an agent can invoke
// the deterministic DAG pipeline via a tool call.
func (cg *CompiledGraph) AsTool(cfg ToolConfig) *agentcore.Tool {
	return stepAsTool(cg, cfg)
}

// AsTool wraps the Runner as an agentcore.Tool.
func (r *Runner) AsTool(cfg ToolConfig) *agentcore.Tool {
	return stepAsTool(r, cfg)
}

// AsTool wraps the CompiledPregelGraph as an agentcore.Tool. The Pregel graph's
// state-based execution is bridged via RunString: the tool input is placed in
// PregelState["input"] and the result is read from PregelState["output"].
func (cpg *CompiledPregelGraph) AsTool(cfg ToolConfig) *agentcore.Tool {
	return &agentcore.Tool{
		Name:        cfg.Name,
		Description: cfg.Description,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"input": map[string]any{
					"type":        "string",
					"description": cfg.inputDesc(),
				},
			},
			"required": []any{"input"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Input string `json:"input"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments for pregel graph tool %q: %w", cfg.Name, err)
			}
			return cpg.RunString(ctx, params.Input)
		},
	}
}

func stepAsTool(s Step, cfg ToolConfig) *agentcore.Tool {
	return &agentcore.Tool{
		Name:        cfg.Name,
		Description: cfg.Description,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"input": map[string]any{
					"type":        "string",
					"description": cfg.inputDesc(),
				},
			},
			"required": []any{"input"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Input string `json:"input"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments for graph tool %q: %w", cfg.Name, err)
			}
			return s.Run(ctx, params.Input)
		},
	}
}
