package agentcore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// HandoffMode determines how control is transferred to a target agent.
type HandoffMode string

const (
	// HandoffDelegate runs the target agent as a sub-task and returns its output
	// as a tool result back to the calling agent. The calling agent continues.
	HandoffDelegate HandoffMode = "delegate"

	// HandoffTransfer fully transfers the conversation to the target agent.
	// The calling agent stops and the target agent takes over.
	HandoffTransfer HandoffMode = "transfer"
)

// HandoffConfig describes a sub-agent that the current agent can hand off to.
type HandoffConfig struct {
	Name        string
	Description string // shown to the LLM so it can decide when to hand off
	Mode        HandoffMode
	AgentConfig Config
}

// PendingHandoff is set on state when a transfer-mode handoff tool is called.
type PendingHandoff struct {
	TargetName   string
	TargetConfig Config
	Context      string
}

// registerHandoffs creates a synthetic tool for each configured handoff target.
func (a *Agent) registerHandoffs() {
	for _, h := range a.config.Handoffs {
		a.registry.Register(a.createHandoffTool(h))
	}
}

func (a *Agent) createHandoffTool(h HandoffConfig) *Tool {
	return &Tool{
		Name:        "transfer_to_" + h.Name,
		Description: fmt.Sprintf("Hand off to %s. %s", h.Name, h.Description),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"message": map[string]any{
					"type":        "string",
					"description": "Context or instructions for the target agent",
				},
			},
			"required":             []string{"message"},
			"additionalProperties": false,
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var p struct {
				Message string `json:"message"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return nil, err
			}

			switch h.Mode {
			case HandoffDelegate:
				return a.executeDelegate(ctx, h, p.Message)
			case HandoffTransfer:
				a.state.SetPendingHandoff(&PendingHandoff{
					TargetName:   h.Name,
					TargetConfig: h.AgentConfig,
					Context:      p.Message,
				})
				return map[string]string{"status": "transferring to " + h.Name}, nil
			default:
				return nil, fmt.Errorf("unknown handoff mode: %s", h.Mode)
			}
		},
	}
}

// executeDelegate creates a sub-agent, runs it, and returns its output as a tool result.
func (a *Agent) executeDelegate(ctx context.Context, h HandoffConfig, input string) (any, error) {
	start := time.Now()
	a.emit(&HandoffStartEvent{
		baseEvent:   newBase(EventHandoffStart),
		SourceAgent: a.config.Name,
		TargetAgent: h.Name,
		Mode:        string(HandoffDelegate),
		Context:     input,
	})

	sub := New(h.AgentConfig)
	sub.SetEventBus(a.eventBus) // forward events to parent
	defer sub.Close()

	output, err := sub.Run(ctx, input)

	a.emit(&HandoffEndEvent{
		baseEvent:   newBase(EventHandoffEnd),
		TargetAgent: h.Name,
		Output:      output,
		Duration:    time.Since(start),
		Err:         err,
	})

	if err != nil {
		return nil, WrapNodeError(err, "delegate:"+h.Name)
	}
	return map[string]string{"result": output}, nil
}

// handleTransfer creates a target agent, inherits the conversation and runtime
// state from the source agent, and transfers control.
func (a *Agent) handleTransfer(ctx context.Context, handoff *PendingHandoff) (string, error) {
	start := time.Now()
	a.emit(&HandoffStartEvent{
		baseEvent:   newBase(EventHandoffStart),
		SourceAgent: a.config.Name,
		TargetAgent: handoff.TargetName,
		Mode:        string(HandoffTransfer),
		Context:     handoff.Context,
	})

	target := New(handoff.TargetConfig)
	target.SetEventBus(a.eventBus) // forward events
	defer target.Close()

	// Inherit runtime state from the source agent.
	a.inheritRuntime(target)

	// Inherit conversation: replace source system prompt with target's, keep the rest.
	if handoff.TargetConfig.SystemPrompt != "" {
		if err := target.persistMessage(ctx, Message{Role: RoleSystem, Content: handoff.TargetConfig.SystemPrompt}); err != nil {
			return "", err
		}
	}
	for _, msg := range a.state.Messages() {
		if msg.Role == RoleSystem {
			continue
		}
		if err := target.persistMessage(ctx, msg); err != nil {
			return "", err
		}
	}

	output, err := target.Continue(ctx)

	a.emit(&HandoffEndEvent{
		baseEvent:   newBase(EventHandoffEnd),
		TargetAgent: handoff.TargetName,
		Output:      output,
		Duration:    time.Since(start),
		Err:         err,
	})

	a.state.SetStatus(StatusFinished)
	return output, err
}

// inheritRuntime copies the source agent's tools, extensions, and config-level
// runtime state onto the target agent.
func (a *Agent) inheritRuntime(target *Agent) {
	// Copy tools from source to target (excluding handoff tools).
	for _, t := range a.registry.Tools() {
		if isHandoffTool(t.Name) {
			continue
		}
		target.registry.Register(t)
	}

	// Re-register source extensions on target.
	if len(a.config.Extensions) > 0 {
		_ = target.extensions.Register(context.Background(), target, a.config.Extensions...)
	}

	// Merge config-level runtime state.
	target.configMu.Lock()
	defer target.configMu.Unlock()

	if len(a.config.Middleware) > 0 {
		target.config.Middleware = append(target.config.Middleware, a.config.Middleware...)
	}
	if len(a.config.GlobalBefore) > 0 {
		target.config.GlobalBefore = append(target.config.GlobalBefore, a.config.GlobalBefore...)
	}
	if len(a.config.GlobalAfter) > 0 {
		target.config.GlobalAfter = append(target.config.GlobalAfter, a.config.GlobalAfter...)
	}
	if a.config.Lifecycle != nil {
		target.config.Lifecycle = appendLifecycleHook(target.config.Lifecycle, a.config.Lifecycle)
	}
	if a.config.TransformContext != nil {
		prev := target.config.TransformContext
		target.config.TransformContext = func(ctx context.Context, msgs []Message) []Message {
			if prev != nil {
				msgs = prev(ctx, msgs)
			}
			return a.config.TransformContext(ctx, msgs)
		}
	}
}

func isHandoffTool(name string) bool {
	return len(name) > 13 && name[:13] == "transfer_to_"
}
