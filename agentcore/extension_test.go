package agentcore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type executionHookExtension struct {
	initErr    error
	middleware bool
	before     bool
	after      bool
}

func (*executionHookExtension) Name() string { return "execution-hooks" }

func (e *executionHookExtension) Init(context.Context, *Agent) error { return e.initErr }

func (*executionHookExtension) Dispose() error { return nil }

func (e *executionHookExtension) Middleware() []Middleware {
	return []Middleware{func(next ExecuteFunc) ExecuteFunc {
		return func(ctx context.Context, tc ToolCall) (string, error) {
			e.middleware = true
			return next(ctx, tc)
		}
	}}
}

func (e *executionHookExtension) BeforeHooks() []BeforeHook {
	return []BeforeHook{func(context.Context, *HookContext) error {
		e.before = true
		return nil
	}}
}

func (e *executionHookExtension) AfterHooks() []AfterHook {
	return []AfterHook{func(context.Context, *HookContext, string, error) {
		e.after = true
	}}
}

func TestExtensionExecutionHooksReachExecutor(t *testing.T) {
	ext := &executionHookExtension{}
	tool := &Tool{
		Name: "echo",
		Func: func(context.Context, json.RawMessage) (any, error) {
			return "ok", nil
		},
	}
	agent := New(NewConfig(WithTools(tool), WithExtensions(ext)))
	defer agent.Close()

	if _, err := agent.InvokeTool(context.Background(), "echo", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("InvokeTool: %v", err)
	}
	if !ext.middleware || !ext.before || !ext.after {
		t.Fatalf("extension hooks not applied: middleware=%v before=%v after=%v", ext.middleware, ext.before, ext.after)
	}
}

func TestExtensionInitErrorIsSurfaced(t *testing.T) {
	want := errors.New("init failed")
	agent := New(NewConfig(WithExtensions(&executionHookExtension{initErr: want})))
	defer agent.Close()

	if _, err := agent.Run(context.Background(), "hello"); err == nil || !strings.Contains(err.Error(), want.Error()) {
		t.Fatalf("Run error = %v, want %q", err, want)
	}
	if got, err := NewWithError(NewConfig(WithExtensions(&executionHookExtension{initErr: want}))); got != nil || err == nil {
		t.Fatalf("NewWithError = (%v, %v), want (nil, error)", got, err)
	}
}
