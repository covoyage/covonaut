package agentcore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func echoTool() *Tool {
	return &Tool{
		Name:        "echo",
		Description: "echoes input",
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			return string(args), nil
		},
	}
}

func failTool() *Tool {
	return &Tool{
		Name:        "fail",
		Description: "always fails",
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			return "", errors.New("tool error")
		},
	}
}

func structTool() *Tool {
	return &Tool{
		Name:        "struct",
		Description: "returns a struct",
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			return map[string]any{"result": "ok"}, nil
		},
	}
}

func TestExecutorExecute(t *testing.T) {
	reg := NewRegistry()
	reg.Register(echoTool())
	exe := NewExecutor(reg)

	result := exe.Execute(context.Background(), ToolCall{
		ID:        "call-1",
		Name:      "echo",
		Arguments: `{"msg":"hello"}`,
	}, &AgentState{})

	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if result.Result != `{"msg":"hello"}` {
		t.Fatalf("result = %q", result.Result)
	}
	if result.ToolCallID != "call-1" {
		t.Fatalf("call ID = %q", result.ToolCallID)
	}
	if result.ToolName != "echo" {
		t.Fatalf("tool name = %q", result.ToolName)
	}
}

func TestExecutorExecuteToolNotFound(t *testing.T) {
	reg := NewRegistry()
	exe := NewExecutor(reg)

	result := exe.Execute(context.Background(), ToolCall{
		Name: "nonexistent",
	}, &AgentState{})

	if result.Err == nil {
		t.Fatal("expected error for missing tool")
	}
}

func TestExecutorExecuteToolError(t *testing.T) {
	reg := NewRegistry()
	reg.Register(failTool())
	exe := NewExecutor(reg)

	result := exe.Execute(context.Background(), ToolCall{
		Name: "fail",
	}, &AgentState{})

	if result.Err == nil {
		t.Fatal("expected error")
	}
}

func TestExecutorExecuteStructResult(t *testing.T) {
	reg := NewRegistry()
	reg.Register(structTool())
	exe := NewExecutor(reg)

	result := exe.Execute(context.Background(), ToolCall{
		Name: "struct",
	}, &AgentState{})

	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if result.Result != `{"result":"ok"}` {
		t.Fatalf("result = %q", result.Result)
	}
}

func TestExecutorBeforeHookRejects(t *testing.T) {
	rejectHook := func(ctx context.Context, hc *HookContext) error {
		return errors.New("rejected by hook")
	}

	reg := NewRegistry()
	reg.Register(&Tool{
		Name:   "blocked",
		Before: []BeforeHook{rejectHook},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			return "should not reach", nil
		},
	})
	exe := NewExecutor(reg)

	result := exe.Execute(context.Background(), ToolCall{Name: "blocked"}, &AgentState{})
	if result.Err == nil {
		t.Fatal("expected rejection error")
	}
}

func TestExecutorGlobalBeforeHook(t *testing.T) {
	reg := NewRegistry()
	reg.Register(echoTool())
	exe := NewExecutor(reg, ExecutorConfig{
		Before: []BeforeHook{
			func(ctx context.Context, hc *HookContext) error {
				if hc.ToolName == "echo" {
					return errors.New("global rejects echo")
				}
				return nil
			},
		},
	})

	result := exe.Execute(context.Background(), ToolCall{Name: "echo"}, &AgentState{})
	if result.Err == nil {
		t.Fatal("expected global hook rejection")
	}
}

func TestExecutorAfterHook(t *testing.T) {
	reg := NewRegistry()
	reg.Register(echoTool())

	var afterCalled bool
	exe := NewExecutor(reg, ExecutorConfig{
		After: []AfterHook{
			func(ctx context.Context, hc *HookContext, result string, err error) {
				afterCalled = true
			},
		},
	})

	exe.Execute(context.Background(), ToolCall{Name: "echo", Arguments: `"hi"`}, &AgentState{})
	if !afterCalled {
		t.Fatal("after hook should have been called")
	}
}

func TestExecutorMiddleware(t *testing.T) {
	reg := NewRegistry()
	reg.Register(echoTool())

	var mwCalled bool
	mw := func(next ExecuteFunc) ExecuteFunc {
		return func(ctx context.Context, tc ToolCall) (string, error) {
			mwCalled = true
			return next(ctx, tc)
		}
	}

	exe := NewExecutor(reg, ExecutorConfig{
		Middleware: []Middleware{mw},
	})

	exe.Execute(context.Background(), ToolCall{Name: "echo", Arguments: `"hi"`}, &AgentState{})
	if !mwCalled {
		t.Fatal("middleware should have been called")
	}
}

func TestExecutorUnknownToolHandler(t *testing.T) {
	reg := NewRegistry()
	handler := func(ctx context.Context, tc ToolCall) (string, error) {
		return "handled", nil
	}
	exe := NewExecutor(reg, ExecutorConfig{
		UnknownToolHandler: handler,
	})

	result := exe.Execute(context.Background(), ToolCall{Name: "unknown"}, &AgentState{})
	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if result.Result != "handled" {
		t.Fatalf("result = %q", result.Result)
	}
}

func TestExecutorValidateArguments(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&Tool{
		Name: "validated",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string"},
			},
			"required": []any{"name"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			return "ok", nil
		},
	})
	exe := NewExecutor(reg, ExecutorConfig{
		ValidateArguments: true,
	})

	// Missing required field
	result := exe.Execute(context.Background(), ToolCall{
		Name:      "validated",
		Arguments: `{}`,
	}, &AgentState{})
	if result.Err == nil {
		t.Fatal("expected validation error with missing required field")
	}
}

func TestExecutorExecuteAllSerial(t *testing.T) {
	reg := NewRegistry()
	reg.Register(echoTool())
	exe := NewExecutor(reg)

	calls := []ToolCall{
		{ID: "c1", Name: "echo", Arguments: `"a"`},
		{ID: "c2", Name: "echo", Arguments: `"b"`},
	}
	results := exe.ExecuteAll(context.Background(), calls, &AgentState{}, nil)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Result != `"a"` || results[1].Result != `"b"` {
		t.Fatalf("unexpected results: %v", results)
	}
}

func TestExecutorExecuteAllParallel(t *testing.T) {
	reg := NewRegistry()
	reg.Register(echoTool())
	exe := NewExecutor(reg, ExecutorConfig{Mode: ModeParallel})

	calls := []ToolCall{
		{ID: "c1", Name: "echo", Arguments: `"a"`},
		{ID: "c2", Name: "echo", Arguments: `"b"`},
	}
	results := exe.ExecuteAll(context.Background(), calls, &AgentState{}, nil)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
}

func TestExecutorExecuteAllCallbacks(t *testing.T) {
	reg := NewRegistry()
	reg.Register(echoTool())
	exe := NewExecutor(reg)

	var starts, ends int
	cb := &ExecuteCallbacks{
		OnStart: func(tc ToolCall) { starts++ },
		OnEnd:   func(result ToolResult) { ends++ },
	}

	calls := []ToolCall{
		{ID: "c1", Name: "echo"},
		{ID: "c2", Name: "echo"},
	}
	exe.ExecuteAll(context.Background(), calls, &AgentState{}, cb)
	if starts != 2 {
		t.Fatalf("expected 2 starts, got %d", starts)
	}
	if ends != 2 {
		t.Fatalf("expected 2 ends, got %d", ends)
	}
}

func TestExecutorDefaultModeSerial(t *testing.T) {
	reg := NewRegistry()
	exe := NewExecutor(reg)
	if exe.config.Mode != ModeSerial {
		t.Fatalf("expected default mode serial, got %s", exe.config.Mode)
	}
}

func TestExecutorArgumentRepair(t *testing.T) {
	reg := NewRegistry()
	reg.Register(echoTool())

	repairCalled := false
	repairFunc := func(rawArgs string, toolName string) string {
		repairCalled = true
		if toolName == "echo" {
			return `{"msg":"repaired"}`
		}
		return rawArgs
	}

	exe := NewExecutor(reg, ExecutorConfig{
		ArgumentRepairFunc: repairFunc,
	})

	// Invalid JSON should be repaired
	result := exe.Execute(context.Background(), ToolCall{
		Name:      "echo",
		Arguments: `{"msg": "broken"`,
	}, &AgentState{})

	if result.Err != nil {
		t.Fatalf("expected no error after repair, got: %v", result.Err)
	}
	if !repairCalled {
		t.Error("repair function was not called")
	}
}

func TestExecutorArgumentRepairFails(t *testing.T) {
	reg := NewRegistry()
	reg.Register(echoTool())

	repairFunc := func(rawArgs string, toolName string) string {
		return rawArgs // return unchanged (still invalid)
	}

	exe := NewExecutor(reg, ExecutorConfig{
		ArgumentRepairFunc: repairFunc,
	})

	result := exe.Execute(context.Background(), ToolCall{
		Name:      "echo",
		Arguments: `{"msg": broken`,
	}, &AgentState{})

	if result.Err == nil {
		t.Fatal("expected error when repair fails")
	}
}

func TestExecutorNoRepairFunc(t *testing.T) {
	reg := NewRegistry()
	reg.Register(echoTool())

	// No repair func configured — should reject invalid JSON
	exe := NewExecutor(reg)

	result := exe.Execute(context.Background(), ToolCall{
		Name:      "echo",
		Arguments: `{"msg": broken`,
	}, &AgentState{})

	if result.Err == nil {
		t.Fatal("expected error for invalid JSON without repair func")
	}
}

// fakeImageResult is a BlockCarrier standing in for a multimodal tool result.
type fakeImageResult struct {
	text   string
	blocks []ContentBlock
}

func (r *fakeImageResult) ToolBlocks() []ContentBlock { return r.blocks }
func (r *fakeImageResult) ToolText() string           { return r.text }

func TestExecuteBlockCarrierRoundTrip(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&Tool{
		Name: "snap",
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			return &fakeImageResult{
				text: "image attached",
				blocks: []ContentBlock{
					{Kind: BlockKindImage, URL: "data:image/png;base64,QUFB", MediaType: "image/png"},
				},
			}, nil
		},
	})
	exe := NewExecutor(reg, ExecutorConfig{Mode: ModeSerial})
	res := exe.Execute(context.Background(), ToolCall{ID: "c1", Name: "snap"}, &AgentState{})
	if res.Err != nil {
		t.Fatalf("execute: %v", res.Err)
	}
	if res.Result != "image attached" {
		t.Fatalf("text form: got %q", res.Result)
	}
	if len(res.Blocks) != 1 || res.Blocks[0].Kind != BlockKindImage {
		t.Fatalf("blocks not transported: %+v", res.Blocks)
	}
	if res.Blocks[0].URL != "data:image/png;base64,QUFB" {
		t.Fatalf("block payload corrupted: %+v", res.Blocks[0])
	}
}

func TestExecuteAllMixed(t *testing.T) {
	var mu sync.Mutex
	var inFlight int32
	maxConcurrent := int32(0)
	timeline := []string{}

	record := func(name string) {
		mu.Lock()
		timeline = append(timeline, name)
		mu.Unlock()
	}

	track := func() func() {
		mu.Lock()
		inFlight++
		if inFlight > maxConcurrent {
			maxConcurrent = inFlight
		}
		mu.Unlock()
		return func() {
			mu.Lock()
			inFlight--
			mu.Unlock()
		}
	}

	safe := func(ctx context.Context, args json.RawMessage) (any, error) {
		done := track()
		defer done()
		time.Sleep(30 * time.Millisecond)
		record("safe:" + string(args))
		return "ok", nil
	}
	unsafe := func(name string) ToolFunc {
		return func(ctx context.Context, args json.RawMessage) (any, error) {
			done := track()
			defer done()
			mu.Lock()
			current := inFlight
			mu.Unlock()
			if current != 1 {
				t.Errorf("%s ran with %d calls in flight; want exclusive execution", name, current)
			}
			record(name)
			return "ok", nil
		}
	}

	yes := true
	no := false
	reg := NewRegistry()
	reg.Register(&Tool{Name: "safe", Func: safe, ConcurrencySafe: &yes})
	reg.Register(&Tool{Name: "blocked", Func: unsafe("blocked"), ConcurrencySafe: &no})
	reg.Register(&Tool{Name: "undeclared", Func: unsafe("undeclared")})

	calls := []ToolCall{
		{ID: "1", Name: "safe", Arguments: `"a"`},
		{ID: "2", Name: "safe", Arguments: `"b"`},
		{ID: "3", Name: "blocked", Arguments: ""},
		{ID: "4", Name: "safe", Arguments: `"c"`},
		{ID: "5", Name: "undeclared", Arguments: ""},
		{ID: "6", Name: "safe", Arguments: `"d"`},
	}

	exe := NewExecutor(reg, ExecutorConfig{Mode: ModeMixed})
	results := exe.ExecuteAll(context.Background(), calls, &AgentState{}, nil)

	for i, r := range results {
		if r.Err != nil {
			t.Fatalf("call %d failed: %v", i, r.Err)
		}
	}
	if maxConcurrent < 2 {
		t.Errorf("declared-safe calls never ran concurrently (max=%d)", maxConcurrent)
	}
	// Results stay in model order.
	want := []string{"a", "b", "", "c", "", "d"}
	for i, arg := range want {
		if arg != "" && !strings.Contains(results[i].Result, "ok") {
			t.Errorf("result %d = %q, want ok", i, results[i].Result)
		}
	}
	_ = timeline
}

func TestExecuteAllParallel(t *testing.T) {
	var mu sync.Mutex
	var inFlight int32
	maxConcurrent := int32(0)

	track := func() func() {
		mu.Lock()
		inFlight++
		if inFlight > maxConcurrent {
			maxConcurrent = inFlight
		}
		mu.Unlock()
		return func() {
			mu.Lock()
			inFlight--
			mu.Unlock()
		}
	}

	slow := func(ctx context.Context, args json.RawMessage) (any, error) {
		done := track()
		defer done()
		time.Sleep(30 * time.Millisecond)
		return "ok", nil
	}
	exclusive := func(name string) ToolFunc {
		return func(ctx context.Context, args json.RawMessage) (any, error) {
			done := track()
			defer done()
			mu.Lock()
			current := inFlight
			mu.Unlock()
			if current != 1 {
				t.Errorf("%s ran with %d calls in flight; want exclusive execution", name, current)
			}
			time.Sleep(10 * time.Millisecond)
			return "ok", nil
		}
	}

	yes := true
	no := false
	reg := NewRegistry()
	reg.Register(&Tool{Name: "declared", Func: slow, ConcurrencySafe: &yes})
	reg.Register(&Tool{Name: "opted_out", Func: exclusive("opted_out"), ConcurrencySafe: &no})
	reg.Register(&Tool{Name: "undeclared", Func: slow})

	calls := []ToolCall{
		{ID: "1", Name: "undeclared", Arguments: ""},
		{ID: "2", Name: "undeclared", Arguments: ""},
		{ID: "3", Name: "opted_out", Arguments: ""},
		{ID: "4", Name: "declared", Arguments: ""},
		{ID: "5", Name: "undeclared", Arguments: ""},
	}

	exe := NewExecutor(reg, ExecutorConfig{Mode: ModeParallel})
	results := exe.ExecuteAll(context.Background(), calls, &AgentState{}, nil)

	for i, r := range results {
		if r.Err != nil {
			t.Fatalf("call %d failed: %v", i, r.Err)
		}
	}
	// Undeclared tools join the pool (fail-open), so the first two calls must
	// have overlapped; the opted-out call ran alone as a barrier.
	if maxConcurrent < 2 {
		t.Errorf("parallel pool never overlapped calls (max=%d)", maxConcurrent)
	}
}

func TestExecuteAllRolling(t *testing.T) {
	var mu sync.Mutex
	slowFinished := false
	fastStartedWhileSlowRunning := 0

	reg := NewRegistry()
	reg.Register(&Tool{Name: "slow", Func: func(ctx context.Context, args json.RawMessage) (any, error) {
		time.Sleep(100 * time.Millisecond)
		mu.Lock()
		slowFinished = true
		mu.Unlock()
		return "ok", nil
	}})
	reg.Register(&Tool{Name: "fast", Func: func(ctx context.Context, args json.RawMessage) (any, error) {
		mu.Lock()
		if !slowFinished {
			fastStartedWhileSlowRunning++
		}
		mu.Unlock()
		return "ok", nil
	}})

	// Both tools are undeclared, so under ModeParallel both are pool-eligible.
	// A batch-granularity scheduler would wait for the slow call to settle
	// before starting the fast ones; a rolling scheduler starts them while it
	// is still in flight.
	calls := []ToolCall{
		{ID: "1", Name: "slow", Arguments: ""},
		{ID: "2", Name: "fast", Arguments: ""},
		{ID: "3", Name: "fast", Arguments: ""},
	}

	exe := NewExecutor(reg, ExecutorConfig{Mode: ModeParallel})
	results := exe.ExecuteAll(context.Background(), calls, &AgentState{}, nil)

	for i, r := range results {
		if r.Err != nil {
			t.Fatalf("call %d failed: %v", i, r.Err)
		}
		if !strings.Contains(r.Result, "ok") {
			t.Errorf("result %d = %q, want ok", i, r.Result)
		}
	}
	if fastStartedWhileSlowRunning < 2 {
		t.Errorf("rolling pool blocked later eligible calls behind a slow straggler (started early: %d)", fastStartedWhileSlowRunning)
	}
}
