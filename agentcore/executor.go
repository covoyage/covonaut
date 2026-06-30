package agentcore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ExecutionMode controls whether tool calls run serially or in parallel.
type ExecutionMode string

const (
	ModeSerial   ExecutionMode = "serial"
	ModeParallel ExecutionMode = "parallel"
)

// DualToolOutput wraps a tool result with separate output for LLM and user.
type DualToolOutput struct {
	ForLLM  string `json:"for_llm"`
	ForUser string `json:"for_user"`
	Silent  bool   `json:"silent,omitempty"`
}

// NewToolResult creates a result visible to both LLM and user.
func NewToolResult(forLLM string) *DualToolOutput {
	return &DualToolOutput{ForLLM: forLLM}
}

// SilentResult creates a result visible only to the LLM (not shown to user).
func SilentResult(forLLM string) *DualToolOutput {
	return &DualToolOutput{ForLLM: forLLM, Silent: true}
}

// UserResult creates a result visible to both LLM and user.
func UserResult(content string) *DualToolOutput {
	return &DualToolOutput{ForLLM: content, ForUser: content}
}

// ExecutorConfig tunes how the executor dispatches tool calls.
type ExecutorConfig struct {
	Mode               ExecutionMode
	Concurrency        int64 // max parallel goroutines; 0 = unlimited
	Middleware         []Middleware
	Before             []BeforeHook       // global before hooks applied to every tool
	After              []AfterHook        // global after hooks applied to every tool
	ValidateArguments  bool               // enable JSON Schema validation of tool arguments
	UnknownToolHandler UnknownToolHandler // called when the model hallucinates a tool name
}

// ToolResult holds the outcome of a single tool call execution.
type ToolResult struct {
	ToolCallID string
	ToolName   string
	Result     string
	// ForLLM provides alternative content shown to the LLM.
	// When set, this replaces Result in the model context.
	ForLLM string
	// ForUser provides alternative content shown to the user.
	// When set, this replaces Result in the user display.
	ForUser string
	// Silent suppresses display output.
	Silent   bool
	Err      error
	Duration time.Duration
}

// IsDualOutput returns true when LLM and user outputs differ.
func (r *ToolResult) IsDualOutput() bool {
	return r.ForLLM != "" && r.ForLLM != r.Result
}

// EffectiveResult returns the content for LLM context.
func (r *ToolResult) EffectiveResult() string {
	if r.ForLLM != "" {
		return r.ForLLM
	}
	return r.Result
}

// ExecuteCallbacks provides optional real-time notifications during ExecuteAll.
type ExecuteCallbacks struct {
	OnStart func(tc ToolCall)
	OnEnd   func(result ToolResult)
}

// Executor dispatches tool calls against a Registry with hooks and middleware.
type Executor struct {
	registry *Registry
	config   ExecutorConfig
	chain    ExecuteFunc
}

func NewExecutor(registry *Registry, cfg ...ExecutorConfig) *Executor {
	var config ExecutorConfig
	if len(cfg) > 0 {
		config = cfg[0]
	}
	if config.Mode == "" {
		config.Mode = ModeSerial
	}

	e := &Executor{
		registry: registry,
		config:   config,
	}
	e.chain = e.buildChain()
	return e
}

func (e *Executor) buildChain() ExecuteFunc {
	var core ExecuteFunc = e.coreExecute
	for i := len(e.config.Middleware) - 1; i >= 0; i-- {
		core = e.config.Middleware[i](core)
	}
	return core
}

// coreExecute looks up the tool, optionally validates arguments, and invokes its Func.
func (e *Executor) coreExecute(ctx context.Context, tc ToolCall) (string, error) {
	tool, ok := e.registry.Get(tc.Name)
	if !ok {
		if e.config.UnknownToolHandler != nil {
			return e.config.UnknownToolHandler(ctx, tc)
		}
		return "", fmt.Errorf("tool not found: %s", tc.Name)
	}

	// Unconditional JSON validity check. When the model output is truncated by
	// max_tokens the tool-call arguments are cut mid-string, producing invalid
	// JSON. Running the tool with partial arguments risks semantic corruption
	// (e.g. a half-written file path). Reject early with a clear message so the
	// model regenerates the call. This runs regardless of ValidateArguments
	// because it is a correctness guard, not a schema conformance check.
	if tc.Arguments != "" && !json.Valid([]byte(tc.Arguments)) {
		return "", fmt.Errorf(
			"tool %s arguments are not valid JSON — the previous response may have been truncated by max_tokens; please regenerate the tool call with complete arguments",
			tc.Name,
		)
	}

	if e.config.ValidateArguments {
		if err := ValidateToolArguments(tool, tc.Arguments); err != nil {
			return "", fmt.Errorf("argument validation failed for %s: %w", tc.Name, err)
		}
	}

	result, err := tool.Func(ctx, json.RawMessage(tc.Arguments))
	if err != nil {
		return "", fmt.Errorf("tool %s execution failed: %w", tc.Name, err)
	}

	// Handle DualToolOutput for separate LLM/user content
	if dual, ok := result.(*DualToolOutput); ok {
		if dual.ForLLM != "" {
			return fmt.Sprintf("__dual__%s__|__%s__", dual.ForLLM, dual.ForUser), nil
		}
		return dual.ForUser, nil
	}

	if str, ok := result.(string); ok {
		return str, nil
	}

	data, err := json.Marshal(result)
	if err != nil {
		return fmt.Sprintf("%v", result), nil
	}
	return string(data), nil
}

// Execute runs a single tool call: tool-before → global-before → middleware chain → global-after → tool-after.
func (e *Executor) Execute(ctx context.Context, tc ToolCall, state *AgentState) ToolResult {
	start := time.Now()

	hc := &HookContext{
		ToolName:  tc.Name,
		Arguments: json.RawMessage(tc.Arguments),
		State:     state,
	}

	tool, hasTool := e.registry.Get(tc.Name)

	// tool-level before hooks
	if hasTool {
		for _, hook := range tool.Before {
			if err := hook(ctx, hc); err != nil {
				return ToolResult{ToolCallID: tc.ID, ToolName: tc.Name, Err: err, Duration: time.Since(start)}
			}
		}
	}
	// global before hooks
	for _, hook := range e.config.Before {
		if err := hook(ctx, hc); err != nil {
			return ToolResult{ToolCallID: tc.ID, ToolName: tc.Name, Err: err, Duration: time.Since(start)}
		}
	}

	// middleware chain → core
	result, err := e.chain(ctx, tc)

	tr := ToolResult{
		ToolCallID: tc.ID,
		ToolName:   tc.Name,
		Result:     result,
		Err:        err,
		Duration:   time.Since(start),
	}

	// Extract dual output if present
	if strings.HasPrefix(result, "__dual__") {
		if parts := strings.SplitN(result[8:], "__|__", 2); len(parts) == 2 {
			tr.ForLLM = parts[0]
			tr.ForUser = parts[1]
			tr.Result = parts[0] // LLM sees ForLLM
		}
	}

	// tool-level after hooks
	if hasTool {
		for _, hook := range tool.After {
			hook(ctx, hc, result, err)
		}
	}
	// global after hooks
	for _, hook := range e.config.After {
		hook(ctx, hc, result, err)
	}

	return tr
}

// ExecuteAll runs multiple tool calls using the configured execution mode,
// firing optional callbacks in real time for each tool.
func (e *Executor) ExecuteAll(ctx context.Context, calls []ToolCall, state *AgentState, cb *ExecuteCallbacks) []ToolResult {
	if e.config.Mode == ModeParallel && len(calls) > 1 {
		return e.executeParallel(ctx, calls, state, cb)
	}
	return e.executeSerial(ctx, calls, state, cb)
}

func (e *Executor) executeSerial(ctx context.Context, calls []ToolCall, state *AgentState, cb *ExecuteCallbacks) []ToolResult {
	results := make([]ToolResult, len(calls))
	for i, tc := range calls {
		if cb != nil && cb.OnStart != nil {
			cb.OnStart(tc)
		}
		results[i] = e.Execute(ctx, tc, state)
		if cb != nil && cb.OnEnd != nil {
			cb.OnEnd(results[i])
		}
	}
	return results
}

func (e *Executor) executeParallel(ctx context.Context, calls []ToolCall, state *AgentState, cb *ExecuteCallbacks) []ToolResult {
	results := make([]ToolResult, len(calls))
	var wg sync.WaitGroup

	concurrency := e.config.Concurrency
	if concurrency <= 0 {
		concurrency = int64(len(calls))
	}
	sem := make(chan struct{}, concurrency)

	for i, tc := range calls {
		wg.Add(1)
		go func(idx int, call ToolCall) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					results[idx] = ToolResult{ToolName: call.Name, Result: fmt.Sprintf("panic: %v", r)}
				}
			}()
			sem <- struct{}{}
			defer func() { <-sem }()

			if cb != nil && cb.OnStart != nil {
				cb.OnStart(call)
			}
			results[idx] = e.Execute(ctx, call, state)
			if cb != nil && cb.OnEnd != nil {
				cb.OnEnd(results[idx])
			}
		}(i, tc)
	}

	wg.Wait()
	return results
}
