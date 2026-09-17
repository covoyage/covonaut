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
	ModeSerial ExecutionMode = "serial"
	// ModeParallel runs every call through the rolling parallel pool — the
	// widest scheduling. Only a tool that explicitly declared
	// ConcurrencySafe=false is held out to run alone as a serial barrier;
	// undeclared tools join the pool (fail-open).
	ModeParallel ExecutionMode = "parallel"
	// ModeMixed routes each call by its Tool.ConcurrencySafe declaration:
	// declared-safe calls run in a rolling parallel pool in model order,
	// everything else (explicitly unsafe or undeclared) runs alone behind a
	// serial barrier — fail closed on missing declarations.
	ModeMixed ExecutionMode = "mixed"
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

	// ArgumentRepairFunc is called when tool arguments contain invalid JSON.
	// It receives the raw arguments string and tool name, and should return
	// repaired JSON or the original string if no repair is possible.
	// When set, repair is attempted before rejecting invalid JSON.
	ArgumentRepairFunc func(rawArgs string, toolName string) string
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
	// Blocks carries multimodal content (e.g. images) attached to the tool
	// result. Providers that support multipart tool_result render these
	// alongside the textual Result; others fall back to text only.
	Blocks []ContentBlock
	// Silent suppresses display output.
	Silent   bool
	Err      error
	Duration time.Duration
}

// BlockCarrier lets a tool return multimodal content (e.g. images) alongside
// its textual form. Implementations returned from a tool Func have their
// blocks transported to the model as multipart tool_result content when the
// provider supports it.
type BlockCarrier interface {
	ToolBlocks() []ContentBlock
	ToolText() string
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
	// (e.g. a half-written file path). When an ArgumentRepairFunc is configured,
	// attempt repair before rejecting so the model doesn't need to regenerate.
	// This runs regardless of ValidateArguments because it is a correctness
	// guard, not a schema conformance check.
	if tc.Arguments != "" && !json.Valid([]byte(tc.Arguments)) {
		if e.config.ArgumentRepairFunc != nil {
			repaired := e.config.ArgumentRepairFunc(tc.Arguments, tc.Name)
			if repaired != tc.Arguments && json.Valid([]byte(repaired)) {
				tc.Arguments = repaired
			} else {
				return "", fmt.Errorf(
					"tool %s arguments are not valid JSON and could not be repaired — the previous response may have been truncated by max_tokens; please regenerate the tool call with complete arguments",
					tc.Name,
				)
			}
		} else {
			return "", fmt.Errorf(
				"tool %s arguments are not valid JSON — the previous response may have been truncated by max_tokens; please regenerate the tool call with complete arguments",
				tc.Name,
			)
		}
	}

	// Coerce argument types before validation. LLMs frequently emit numbers
	// and booleans as JSON strings (e.g. {"count": "5"} instead of {"count": 5}).
	// This step silently fixes those mismatches so the tool receives the
	// correct types without requiring the model to regenerate the call.
	if tool.Parameters != nil && tc.Arguments != "" {
		tc.Arguments = CoerceToolArguments(tool, tc.Arguments)
	}

	if e.config.ValidateArguments {
		if err := ValidateToolArguments(tool, tc.Arguments); err != nil {
			return "", fmt.Errorf("argument validation failed for %s: %w", tc.Name, err)
		}
	}

	result, err := tool.Func(ctx, json.RawMessage(tc.Arguments))
	if err != nil {
		// Interrupt signals pass through without wrapping so the result
		// string and the interrupt error are both preserved.
		if IsInterrupt(err) {
			var resultStr string
			if str, ok := result.(string); ok {
				resultStr = str
			} else if result != nil {
				data, _ := json.Marshal(result)
				resultStr = string(data)
			}
			return resultStr, err
		}
		return "", fmt.Errorf("tool %s execution failed: %w", tc.Name, err)
	}

	// Handle DualToolOutput for separate LLM/user content
	if dual, ok := result.(*DualToolOutput); ok {
		if dual.ForLLM != "" {
			return fmt.Sprintf("__dual__%s__|__%s__", dual.ForLLM, dual.ForUser), nil
		}
		return dual.ForUser, nil
	}

	// Handle BlockCarrier results: transport multimodal blocks through the
	// string-typed middleware chain via sentinel encoding, decoded back into
	// ToolResult.Blocks by Execute.
	if carrier, ok := result.(BlockCarrier); ok {
		if blocks := carrier.ToolBlocks(); len(blocks) > 0 {
			if data, err := json.Marshal(blocks); err == nil {
				return fmt.Sprintf("__blocks__%s__|__%s", data, carrier.ToolText()), nil
			}
		}
		return carrier.ToolText(), nil
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

	// Decode block-carrier sentinel into structured Blocks. The marker is
	// split at its last separator so textual result content may freely
	// contain the token; a malformed payload degrades to plain text.
	if strings.HasPrefix(result, "__blocks__") {
		body := strings.TrimPrefix(result, "__blocks__")
		if idx := strings.LastIndex(body, "__|__"); idx >= 0 {
			var blocks []ContentBlock
			if err := json.Unmarshal([]byte(body[:idx]), &blocks); err == nil && len(blocks) > 0 {
				tr.Blocks = blocks
				tr.Result = body[idx+len("__|__"):]
			}
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
	switch {
	case e.config.Mode == ModeParallel && len(calls) > 1:
		return e.executeRolling(ctx, calls, state, cb, e.poolEligible)
	case e.config.Mode == ModeMixed && len(calls) > 1:
		return e.executeRolling(ctx, calls, state, cb, e.parallelSafe)
	default:
		return e.executeSerial(ctx, calls, state, cb)
	}
}

// poolEligible reports whether a call may join the parallel pool in
// ModeParallel. Only an explicit ConcurrencySafe=false declaration excludes a
// tool; undeclared tools join the pool (fail-open) so that "parallel" stays
// the widest setting. The fail-closed treatment of missing declarations is
// exclusive to ModeMixed via parallelSafe.
func (e *Executor) poolEligible(tc ToolCall) bool {
	tool, ok := e.registry.Get(tc.Name)
	if !ok || tool.ConcurrencySafe == nil {
		return true
	}
	return *tool.ConcurrencySafe
}

// parallelSafe reports whether a call may join the rolling pool in ModeMixed.
// Fail closed: unknown tools, undeclared tools, and any value other than an
// explicit true are treated as exclusive.
func (e *Executor) parallelSafe(tc ToolCall) bool {
	tool, ok := e.registry.Get(tc.Name)
	if !ok || tool.ConcurrencySafe == nil {
		return false
	}
	return *tool.ConcurrencySafe
}

func (e *Executor) executeSerial(ctx context.Context, calls []ToolCall, state *AgentState, cb *ExecuteCallbacks) []ToolResult {
	results := make([]ToolResult, len(calls))
	for i, tc := range calls {
		if cb != nil && cb.OnStart != nil {
			cb.OnStart(tc)
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					results[i] = ToolResult{ToolName: tc.Name, Result: fmt.Sprintf("panic: %v", r)}
				}
			}()
			results[i] = e.Execute(ctx, tc, state)
		}()
		if cb != nil && cb.OnEnd != nil {
			cb.OnEnd(results[i])
		}
	}
	return results
}

// executeRolling runs calls through a single rolling pool. Calls are started
// strictly in model order: an eligible call acquires a free slot (waiting for
// one if the pool is at capacity) and executes on its own goroutine, so a
// slow straggler never blocks later eligible calls from filling the pool; an
// ineligible call drains the pool and runs alone inline as a serial barrier.
// Results keep their original positions, so the model sees model-ordered
// results exactly as in serial execution. The eligibility predicate selects
// the scheduling policy: poolEligible for ModeParallel (fail-open) or
// parallelSafe for ModeMixed (fail-closed).
func (e *Executor) executeRolling(ctx context.Context, calls []ToolCall, state *AgentState, cb *ExecuteCallbacks, eligible func(ToolCall) bool) []ToolResult {
	results := make([]ToolResult, len(calls))

	concurrency := e.config.Concurrency
	if concurrency <= 0 {
		concurrency = int64(len(calls))
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	inFlight := 0
	poolIdle := sync.NewCond(&mu)

	runOne := func(idx int, tc ToolCall) {
		defer func() {
			if r := recover(); r != nil {
				results[idx] = ToolResult{ToolName: tc.Name, Result: fmt.Sprintf("panic: %v", r)}
			}
		}()
		if cb != nil && cb.OnStart != nil {
			cb.OnStart(tc)
		}
		results[idx] = e.Execute(ctx, tc, state)
		if cb != nil && cb.OnEnd != nil {
			cb.OnEnd(results[idx])
		}
	}

	startPooled := func(idx int, tc ToolCall) {
		wg.Add(1)
		mu.Lock()
		inFlight++
		mu.Unlock()
		go func() {
			defer wg.Done()
			// LIFO defers: the panic recover settles the result slot first,
			// then the in-flight count drops and waiters wake up.
			defer func() {
				mu.Lock()
				inFlight--
				poolIdle.Broadcast()
				mu.Unlock()
			}()
			runOne(idx, tc)
		}()
	}

	for i := 0; i < len(calls); {
		if eligible(calls[i]) {
			mu.Lock()
			for inFlight >= int(concurrency) {
				poolIdle.Wait()
			}
			mu.Unlock()
			startPooled(i, calls[i])
			i++
			continue
		}
		// Serial barrier: drain the pool, then run alone inline.
		mu.Lock()
		for inFlight > 0 {
			poolIdle.Wait()
		}
		mu.Unlock()
		runOne(i, calls[i])
		i++
	}
	wg.Wait()
	return results
}
