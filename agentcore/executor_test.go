package agentcore

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
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
