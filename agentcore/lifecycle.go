package agentcore

import (
	"context"
	"time"
)

// Lifecycle hooks allow intercepting every stage of the agent execution loop
// in a middleware pattern. Each hook can observe, modify, or short-circuit
// the corresponding phase.
//
// The execution flow with lifecycle hooks:
//
//	BeforeAgentRun → (for each inner-loop turn:
//	    BeforeTurn → BeforeModelCall → model call → AfterModelCall →
//	    (optional tools) BeforeToolExecution → tool calls → AfterToolExecution
//	    AfterTurn
//	) → AfterAgentRun
//
// BeforeMessagePersist / AfterMessagePersist wrap every conversation append
// (AddMessage) except ReplaceMessages used by compaction.

// AgentRunContext carries context through the agent lifecycle.
type AgentRunContext struct {
	Agent    *Agent
	Input    string
	Messages []Message
	Turn     int64
}

// ModelCallContext carries context for model call hooks.
type ModelCallContext struct {
	Request  *ProviderRequest
	Response *ProviderResponse // nil in Before, populated in After
	Err      error             // only in After
}

// ToolExecutionContext carries context for tool execution hooks.
type ToolExecutionContext struct {
	ToolCalls []ToolCall
	Results   []ToolResult // nil in Before, populated in After
}

// TurnInfo describes the inner-loop iteration that just finished.
type TurnInfo struct {
	HadToolCalls bool
}

// LifecycleHook intercepts a specific phase of agent execution.
// Returning a non-nil error short-circuits the phase.
type LifecycleHook interface {
	// BeforeAgentRun is called once when the agent starts.
	// Modify arc.Messages to alter the initial prompt.
	BeforeAgentRun(ctx context.Context, arc *AgentRunContext) error

	// AfterAgentRun is called once when the agent finishes (success or error).
	AfterAgentRun(ctx context.Context, arc *AgentRunContext, output string, err error)

	// BeforeModelCall is called before each LLM call.
	// Modify mcc.Request to alter the request.
	BeforeModelCall(ctx context.Context, arc *AgentRunContext, mcc *ModelCallContext) error

	// AfterModelCall is called after each LLM call.
	AfterModelCall(ctx context.Context, arc *AgentRunContext, mcc *ModelCallContext)

	// BeforeToolExecution is called before tool calls are dispatched.
	// Return error to skip tool execution entirely.
	BeforeToolExecution(ctx context.Context, arc *AgentRunContext, tec *ToolExecutionContext) error

	// AfterToolExecution is called after all tool calls in a turn complete.
	AfterToolExecution(ctx context.Context, arc *AgentRunContext, tec *ToolExecutionContext)

	// BeforeTurn runs once per inner-loop iteration after compaction / steering
	// injection and before TurnStart is emitted.
	BeforeTurn(ctx context.Context, arc *AgentRunContext) error

	// AfterTurn runs once after TurnEnd for that iteration (with or without tools).
	AfterTurn(ctx context.Context, arc *AgentRunContext, info TurnInfo)

	// BeforeMessagePersist runs immediately before a message is appended to state.
	// Mutate *msg to change what gets stored; return error to abort the agent run.
	BeforeMessagePersist(ctx context.Context, arc *AgentRunContext, msg *Message) error

	// AfterMessagePersist runs after the message was stored.
	AfterMessagePersist(ctx context.Context, arc *AgentRunContext, msg Message)
}

// BaseLifecycleHook provides no-op defaults so implementations can override
// only the hooks they care about.
type BaseLifecycleHook struct{}

func (BaseLifecycleHook) BeforeAgentRun(_ context.Context, _ *AgentRunContext) error             { return nil }
func (BaseLifecycleHook) AfterAgentRun(_ context.Context, _ *AgentRunContext, _ string, _ error) {}
func (BaseLifecycleHook) BeforeModelCall(_ context.Context, _ *AgentRunContext, _ *ModelCallContext) error {
	return nil
}
func (BaseLifecycleHook) AfterModelCall(_ context.Context, _ *AgentRunContext, _ *ModelCallContext) {}
func (BaseLifecycleHook) BeforeToolExecution(_ context.Context, _ *AgentRunContext, _ *ToolExecutionContext) error {
	return nil
}
func (BaseLifecycleHook) AfterToolExecution(_ context.Context, _ *AgentRunContext, _ *ToolExecutionContext) {
}

func (BaseLifecycleHook) BeforeTurn(_ context.Context, _ *AgentRunContext) error      { return nil }
func (BaseLifecycleHook) AfterTurn(_ context.Context, _ *AgentRunContext, _ TurnInfo) {}
func (BaseLifecycleHook) BeforeMessagePersist(_ context.Context, _ *AgentRunContext, _ *Message) error {
	return nil
}
func (BaseLifecycleHook) AfterMessagePersist(_ context.Context, _ *AgentRunContext, _ Message) {}

// LifecycleChain composes multiple LifecycleHooks into one.
// Hooks are called in order; AfterXxx hooks are called in reverse order.
type LifecycleChain []LifecycleHook

func (lc LifecycleChain) BeforeAgentRun(ctx context.Context, arc *AgentRunContext) error {
	for _, h := range lc {
		if err := h.BeforeAgentRun(ctx, arc); err != nil {
			return err
		}
	}
	return nil
}

func (lc LifecycleChain) AfterAgentRun(ctx context.Context, arc *AgentRunContext, output string, err error) {
	for i := len(lc) - 1; i >= 0; i-- {
		lc[i].AfterAgentRun(ctx, arc, output, err)
	}
}

func (lc LifecycleChain) BeforeModelCall(ctx context.Context, arc *AgentRunContext, mcc *ModelCallContext) error {
	for _, h := range lc {
		if err := h.BeforeModelCall(ctx, arc, mcc); err != nil {
			return err
		}
	}
	return nil
}

func (lc LifecycleChain) AfterModelCall(ctx context.Context, arc *AgentRunContext, mcc *ModelCallContext) {
	for i := len(lc) - 1; i >= 0; i-- {
		lc[i].AfterModelCall(ctx, arc, mcc)
	}
}

func (lc LifecycleChain) BeforeToolExecution(ctx context.Context, arc *AgentRunContext, tec *ToolExecutionContext) error {
	for _, h := range lc {
		if err := h.BeforeToolExecution(ctx, arc, tec); err != nil {
			return err
		}
	}
	return nil
}

func (lc LifecycleChain) AfterToolExecution(ctx context.Context, arc *AgentRunContext, tec *ToolExecutionContext) {
	for i := len(lc) - 1; i >= 0; i-- {
		lc[i].AfterToolExecution(ctx, arc, tec)
	}
}

func (lc LifecycleChain) BeforeTurn(ctx context.Context, arc *AgentRunContext) error {
	for _, h := range lc {
		if err := h.BeforeTurn(ctx, arc); err != nil {
			return err
		}
	}
	return nil
}

func (lc LifecycleChain) AfterTurn(ctx context.Context, arc *AgentRunContext, info TurnInfo) {
	for i := len(lc) - 1; i >= 0; i-- {
		lc[i].AfterTurn(ctx, arc, info)
	}
}

func (lc LifecycleChain) BeforeMessagePersist(ctx context.Context, arc *AgentRunContext, msg *Message) error {
	for _, h := range lc {
		if err := h.BeforeMessagePersist(ctx, arc, msg); err != nil {
			return err
		}
	}
	return nil
}

func (lc LifecycleChain) AfterMessagePersist(ctx context.Context, arc *AgentRunContext, msg Message) {
	for i := len(lc) - 1; i >= 0; i-- {
		lc[i].AfterMessagePersist(ctx, arc, msg)
	}
}

// --- built-in lifecycle hooks ---

// GuardrailHook validates model output before tool execution.
// If the validator returns an error, the model response is persisted (so it is
// not lost), then an error system message is fed back to the model. The agent
// continues running, giving the model a chance to self-correct.
type GuardrailHook struct {
	BaseLifecycleHook
	Validate func(ctx context.Context, response *ProviderResponse) error
}

func (g *GuardrailHook) AfterModelCall(ctx context.Context, arc *AgentRunContext, mcc *ModelCallContext) {
	if mcc.Err != nil || mcc.Response == nil || g.Validate == nil {
		return
	}
	if err := g.Validate(ctx, mcc.Response); err != nil {
		mcc.Err = err
	}
}

// AuditHook logs every phase transition for compliance/debugging.
type AuditHook struct {
	BaseLifecycleHook
	OnEvent func(phase string, detail map[string]any)
}

func (a *AuditHook) BeforeAgentRun(_ context.Context, arc *AgentRunContext) error {
	if a.OnEvent != nil {
		a.OnEvent("agent_start", map[string]any{"input": arc.Input})
	}
	return nil
}

func (a *AuditHook) AfterAgentRun(_ context.Context, _ *AgentRunContext, output string, err error) {
	if a.OnEvent != nil {
		detail := map[string]any{"output": output}
		if err != nil {
			detail["error"] = err.Error()
		}
		a.OnEvent("agent_end", detail)
	}
}

func (a *AuditHook) BeforeModelCall(_ context.Context, arc *AgentRunContext, _ *ModelCallContext) error {
	if a.OnEvent != nil {
		a.OnEvent("model_call_start", map[string]any{"turn": arc.Turn})
	}
	return nil
}

func (a *AuditHook) AfterModelCall(_ context.Context, _ *AgentRunContext, mcc *ModelCallContext) {
	if a.OnEvent != nil {
		detail := map[string]any{}
		if mcc.Response != nil {
			detail["tool_calls"] = len(mcc.Response.ToolCalls)
		}
		if mcc.Err != nil {
			detail["error"] = mcc.Err.Error()
		}
		a.OnEvent("model_call_end", detail)
	}
}

func (a *AuditHook) BeforeToolExecution(_ context.Context, _ *AgentRunContext, tec *ToolExecutionContext) error {
	if a.OnEvent != nil {
		names := make([]string, len(tec.ToolCalls))
		for i, tc := range tec.ToolCalls {
			names[i] = tc.Name
		}
		a.OnEvent("tool_execution_start", map[string]any{"tools": names})
	}
	return nil
}

func (a *AuditHook) AfterToolExecution(_ context.Context, _ *AgentRunContext, tec *ToolExecutionContext) {
	if a.OnEvent != nil {
		var errCount int64
		for _, r := range tec.Results {
			if r.Err != nil {
				errCount++
			}
		}
		a.OnEvent("tool_execution_end", map[string]any{
			"total":  len(tec.Results),
			"errors": errCount,
		})
	}
}

func (a *AuditHook) BeforeTurn(_ context.Context, arc *AgentRunContext) error {
	if a.OnEvent != nil {
		a.OnEvent("turn_start", map[string]any{"turn": arc.Turn})
	}
	return nil
}

func (a *AuditHook) AfterTurn(_ context.Context, arc *AgentRunContext, info TurnInfo) {
	if a.OnEvent != nil {
		a.OnEvent("turn_end", map[string]any{"turn": arc.Turn, "had_tool_calls": info.HadToolCalls})
	}
}

func (a *AuditHook) BeforeMessagePersist(_ context.Context, _ *AgentRunContext, msg *Message) error {
	if a.OnEvent != nil {
		a.OnEvent("message_persist", map[string]any{"role": msg.Role, "type": msg.Type})
	}
	return nil
}

func (a *AuditHook) AfterMessagePersist(_ context.Context, _ *AgentRunContext, msg Message) {
	if a.OnEvent != nil {
		a.OnEvent("message_persisted", map[string]any{"role": msg.Role})
	}
}

// RateLimitHook enforces per-turn rate limits by injecting delays or errors.
// It resets the counter at the start of each Agent.Run() via BeforeAgentRun.
// When MaxTurnsPerMinute is set, it enforces a sliding window: if the number
// of turns within the last 60 seconds exceeds the limit, an error is returned.
type RateLimitHook struct {
	BaseLifecycleHook
	MaxTurnsPerMinute int64
	turnTimestamps    []time.Time
}

func (r *RateLimitHook) BeforeAgentRun(_ context.Context, _ *AgentRunContext) error {
	r.turnTimestamps = nil
	return nil
}

func (r *RateLimitHook) BeforeModelCall(_ context.Context, _ *AgentRunContext, _ *ModelCallContext) error {
	now := time.Now()
	r.turnTimestamps = append(r.turnTimestamps, now)

	if r.MaxTurnsPerMinute > 0 {
		windowStart := now.Add(-time.Minute)
		count := 0
		for _, t := range r.turnTimestamps {
			if t.After(windowStart) {
				count++
			}
		}
		if int64(count) > r.MaxTurnsPerMinute {
			return NewNodeError("rate limit exceeded", nil, "lifecycle", "rate_limit")
		}
	}
	return nil
}
