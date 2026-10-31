package agentcore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func registrationTool(name string, calls *atomic.Int64) *Tool {
	return &Tool{Name: name, Parameters: map[string]any{"type": "object"}, Func: func(context.Context, json.RawMessage) (any, error) {
		calls.Add(1)
		return name, nil
	}}
}

func TestToolRegistrationRejectsReplacementRemovalAndSamePointer(t *testing.T) {
	for _, change := range []string{"replace", "remove", "same-pointer"} {
		t.Run(change, func(t *testing.T) {
			oldCalls, newCalls := &atomic.Int64{}, &atomic.Int64{}
			registry := NewRegistry()
			original := registrationTool("target", oldCalls)
			registry.Register(original)
			step := registry.captureStep()
			defer step.close()
			ctx := withToolStep(context.Background(), step)
			switch change {
			case "replace":
				registry.Register(registrationTool("target", newCalls))
			case "remove":
				registry.Unregister("target")
			case "same-pointer":
				registry.Unregister("target")
				registry.Register(original)
			}
			unknown := false
			executor := NewExecutor(registry, ExecutorConfig{UnknownToolHandler: func(context.Context, ToolCall) (string, error) {
				unknown = true
				return "must not run", nil
			}})
			result := executor.Execute(ctx, ToolCall{ID: "original-call", Name: "target", Arguments: "{}"}, NewState())
			if !errors.Is(result.Err, ErrStaleToolRegistration) || result.ToolCallID != "original-call" ||
				oldCalls.Load() != 0 || newCalls.Load() != 0 || unknown {
				t.Fatalf("changed registration was dispatched: %+v old=%d new=%d unknown=%v", result, oldCalls.Load(), newCalls.Load(), unknown)
			}
		})
	}
}

func TestToolRegistrationRejectsChangesDuringApprovalOrMiddleware(t *testing.T) {
	for _, stage := range []string{"tool-before", "global-before", "middleware"} {
		t.Run(stage, func(t *testing.T) {
			registry := NewRegistry()
			oldCalls, newCalls := &atomic.Int64{}, &atomic.Int64{}
			old := registrationTool("target", oldCalls)
			replacement := registrationTool("target", newCalls)
			change := func(context.Context, *HookContext) error { registry.Register(replacement); return nil }
			config := ExecutorConfig{}
			switch stage {
			case "tool-before":
				old.Before = []BeforeHook{change}
			case "global-before":
				config.Before = []BeforeHook{change}
			case "middleware":
				config.Middleware = []Middleware{func(next ExecuteFunc) ExecuteFunc {
					return func(ctx context.Context, call ToolCall) (string, error) {
						registry.Register(replacement)
						return next(ctx, call)
					}
				}}
			}
			registry.Register(old)
			result := NewExecutor(registry, config).Execute(context.Background(), ToolCall{Name: "target", Arguments: "{}"}, NewState())
			if !errors.Is(result.Err, ErrStaleToolRegistration) || oldCalls.Load() != 0 || newCalls.Load() != 0 {
				t.Fatalf("approval/replacement race executed a handler: %+v", result)
			}
		})
	}
}

func TestToolRegistrationRejectsMiddlewareNameRedirection(t *testing.T) {
	registry := NewRegistry()
	calls := &atomic.Int64{}
	registry.Register(registrationTool("approved", calls), registrationTool("other", calls))
	executor := NewExecutor(registry, ExecutorConfig{Middleware: []Middleware{func(next ExecuteFunc) ExecuteFunc {
		return func(ctx context.Context, call ToolCall) (string, error) {
			call.Name = "other"
			return next(ctx, call)
		}
	}}})
	result := executor.Execute(context.Background(), ToolCall{Name: "approved", Arguments: "{}"}, NewState())
	if !errors.Is(result.Err, ErrStaleToolRegistration) || calls.Load() != 0 {
		t.Fatal("middleware escaped the admitted registration", result.Err)
	}
}

func TestToolRegistrationBatchSharesIdentityAcrossSchedulingModes(t *testing.T) {
	for _, mode := range []ExecutionMode{ModeSerial, ModeParallel, ModeMixed} {
		t.Run(string(mode), func(t *testing.T) {
			registry := NewRegistry()
			calls := &atomic.Int64{}
			target := registrationTool("target", calls)
			safe := true
			target.ConcurrencySafe = &safe
			replacer := &Tool{Name: "replace", ConcurrencySafe: &safe, Func: func(context.Context, json.RawMessage) (any, error) {
				registry.Register(registrationTool("target", calls))
				return "replaced", nil
			}}
			registry.Register(replacer, target)
			results := NewExecutor(registry, ExecutorConfig{Mode: mode, Concurrency: 1}).ExecuteAll(context.Background(),
				[]ToolCall{{ID: "1", Name: "replace", Arguments: "{}"}, {ID: "2", Name: "target", Arguments: "{}"}}, NewState(), nil)
			if len(results) != 2 || results[0].Err != nil || !errors.Is(results[1].Err, ErrStaleToolRegistration) || calls.Load() != 0 {
				t.Fatalf("queued call rebound in %s mode: %+v", mode, results)
			}
		})
	}
}

func TestToolRegistrationNestedInvocationAndScopeExpiration(t *testing.T) {
	calls := &atomic.Int64{}
	var retained context.Context
	agent := New(Config{})
	agent.RegisterTools(registrationTool("hidden", calls))
	agent.RegisterTools(&Tool{Name: "outer", Func: func(ctx context.Context, _ json.RawMessage) (any, error) {
		retained = ctx
		result, err := agent.InvokeTool(ctx, "hidden", json.RawMessage("{}"))
		if err != nil {
			return nil, err
		}
		agent.RegisterTools(registrationTool("late", calls))
		if _, err := agent.InvokeTool(ctx, "late", json.RawMessage("{}")); !errors.Is(err, ErrStaleToolRegistration) {
			return nil, fmt.Errorf("late nested registration was admitted: %v", err)
		}
		return result, nil
	}})
	if _, err := agent.InvokeTool(context.Background(), "outer", json.RawMessage("{}")); err != nil || calls.Load() != 1 {
		t.Fatal("hidden nested invocation regressed", err)
	}
	if _, err := agent.InvokeTool(retained, "hidden", json.RawMessage("{}")); !errors.Is(err, ErrToolStepClosed) {
		t.Fatal("retained step context was silently refreshed", err)
	}
	if _, err := agent.InvokeTool(context.Background(), "late", json.RawMessage("{}")); err != nil || calls.Load() != 2 {
		t.Fatal("new host invocation did not capture fresh registrations", err)
	}
}

func TestToolRegistrationAlreadyDispatchedCallFinishesOriginal(t *testing.T) {
	entered, finish := make(chan struct{}), make(chan struct{})
	oldAfter, newAfter := &atomic.Int64{}, &atomic.Int64{}
	newCalls := &atomic.Int64{}
	registry := NewRegistry()
	registry.Register(&Tool{Name: "target", Func: func(context.Context, json.RawMessage) (any, error) {
		close(entered)
		<-finish
		return "old result", nil
	}, After: []AfterHook{func(context.Context, *HookContext, string, error) { oldAfter.Add(1) }}})
	executor := NewExecutor(registry)
	done := make(chan ToolResult, 1)
	go func() {
		done <- executor.Execute(context.Background(), ToolCall{Name: "target", Arguments: "{}"}, NewState())
	}()
	<-entered
	replacement := registrationTool("target", newCalls)
	replacement.After = []AfterHook{func(context.Context, *HookContext, string, error) { newAfter.Add(1) }}
	registry.Register(replacement)
	close(finish)
	result := <-done
	if result.Err != nil || result.Result != "old result" || oldAfter.Load() != 1 || newAfter.Load() != 0 || newCalls.Load() != 0 {
		t.Fatalf("in-flight result or after hook was rebound: %+v", result)
	}
}

type registrationProvider struct {
	complete func(context.Context, *ProviderRequest) (*ProviderResponse, error)
}

func (p registrationProvider) Complete(ctx context.Context, req *ProviderRequest) (*ProviderResponse, error) {
	return p.complete(ctx, req)
}
func (p registrationProvider) Stream(context.Context, *ProviderRequest) (<-chan StreamDelta, error) {
	return nil, errors.New("unexpected stream")
}

func TestToolRegistrationModelStepRefreshAndClosedContext(t *testing.T) {
	oldCalls, newCalls := &atomic.Int64{}, &atomic.Int64{}
	var agent *Agent
	var retained context.Context
	requests := 0
	provider := registrationProvider{complete: func(ctx context.Context, req *ProviderRequest) (*ProviderResponse, error) {
		retained = ctx
		requests++
		if len(req.Tools) != 1 || req.Tools[0].Name != "target" {
			t.Fatal("model schema does not match the captured registration")
		}
		switch requests {
		case 1:
			agent.RegisterTools(registrationTool("target", newCalls))
			return &ProviderResponse{ToolCalls: []ToolCall{{ID: "stale", Name: "target", Arguments: "{}"}}}, nil
		case 2:
			found := false
			for _, message := range req.Messages {
				found = found || message.ToolCallID == "stale" && strings.Contains(message.Content, ErrStaleToolRegistration.Error())
			}
			if !found {
				t.Fatal("stale registration did not reach model history as a real error")
			}
			return &ProviderResponse{ToolCalls: []ToolCall{{ID: "fresh", Name: "target", Arguments: "{}"}}}, nil
		default:
			return &ProviderResponse{Content: "done"}, nil
		}
	}}
	agent = New(Config{ModelConfig: ModelConfig{Model: "mock", Provider: provider}})
	agent.RegisterTools(registrationTool("target", oldCalls))
	if _, err := agent.Run(context.Background(), "test"); err != nil || requests != 3 || oldCalls.Load() != 0 || newCalls.Load() != 1 {
		t.Fatalf("model step identity was not refreshed correctly: requests=%d old=%d new=%d error=%v", requests, oldCalls.Load(), newCalls.Load(), err)
	}
	if _, err := agent.InvokeTool(retained, "target", json.RawMessage("{}")); !errors.Is(err, ErrToolStepClosed) {
		t.Fatal("finished model step remained callable", err)
	}
}

func TestToolRegistrationSnapshotFreezesSchemaAndMetadata(t *testing.T) {
	calls := &atomic.Int64{}
	registry := NewRegistry()
	tool := registrationTool("target", calls)
	tool.Parameters["required"] = []string{"original"}
	tool.Parameters["enum"] = []int{1, 2}
	registry.Register(tool)
	step := registry.captureStep()
	defer step.close()
	definitions, err := step.definitions()
	if err != nil {
		t.Fatal(err)
	}
	definitions[0].Parameters["required"].([]string)[0] = "changed"
	definitions[0].Parameters["enum"].([]int)[0] = 9
	snapshot, ok, err := step.resolve("target")
	if err != nil || !ok || snapshot.Parameters["required"].([]string)[0] != "original" ||
		snapshot.Parameters["enum"].([]int)[0] != 1 || tool.Parameters["enum"].([]int)[0] != 1 {
		t.Fatal("provider schema edits mutated validation or registry metadata", err)
	}
	step.close()
	if _, err := step.definitions(); !errors.Is(err, ErrToolStepClosed) || step.tools != nil {
		t.Fatal("closed step retained its registration view")
	}
}

func TestToolRegistrationHiddenSelectionDoesNotBecomePermissionWhitelist(t *testing.T) {
	var agent *Agent
	calls := &atomic.Int64{}
	requests := 0
	provider := registrationProvider{complete: func(_ context.Context, req *ProviderRequest) (*ProviderResponse, error) {
		requests++
		if len(req.Tools) != 1 || req.Tools[0].Name != "outer" {
			t.Fatal("test did not hide the nested tool")
		}
		if requests == 1 {
			return &ProviderResponse{ToolCalls: []ToolCall{{ID: "outer", Name: "outer", Arguments: "{}"}}}, nil
		}
		return &ProviderResponse{Content: "done"}, nil
	}}
	cfg := Config{ModelConfig: ModelConfig{Provider: provider}}
	cfg.ToolSelection = &ToolSelectionConfig{Selector: ToolSelectorFunc(func(context.Context, ToolSelectionContext) ([]string, error) {
		return []string{"outer"}, nil
	})}
	agent = New(cfg)
	agent.RegisterTools(registrationTool("hidden", calls), &Tool{Name: "outer", Func: func(ctx context.Context, _ json.RawMessage) (any, error) {
		return agent.InvokeTool(ctx, "hidden", json.RawMessage("{}"))
	}})
	if _, err := agent.Run(context.Background(), "test"); err != nil || calls.Load() != 1 {
		t.Fatal("step visibility became a new invocation restriction", err)
	}
}

func TestToolRegistrationRejectedPreparationAndInvalidSchemaDoNotLeak(t *testing.T) {
	calls := &atomic.Int64{}
	var retained context.Context
	agent := New(Config{Lifecycle: registrationRejectHook{capture: func(ctx context.Context) { retained = ctx }}})
	agent.RegisterTools(registrationTool("target", calls))
	if _, err := agent.Run(context.Background(), "test"); err == nil {
		t.Fatal("rejected model preparation was accepted")
	}
	if _, err := agent.InvokeTool(retained, "target", json.RawMessage("{}")); !errors.Is(err, ErrToolStepClosed) || calls.Load() != 0 {
		t.Fatal("rejected step left a callable registration context", err)
	}
	tool := registrationTool("cyclic", calls)
	tool.Parameters["self"] = tool.Parameters
	copy := cloneRegisteredTool(tool)
	if _, err := json.Marshal(copy.Parameters); err == nil {
		t.Fatal("invalid cyclic metadata was silently replaced")
	}
}

type registrationRejectHook struct {
	BaseLifecycleHook
	capture func(context.Context)
}

func (hook registrationRejectHook) BeforeModelCall(ctx context.Context, _ *AgentRunContext, _ *ModelCallContext) error {
	hook.capture(ctx)
	return errors.New("preparation rejected")
}

func TestToolRegistrationChangesWhileApprovalWaits(t *testing.T) {
	entered, approve := make(chan struct{}), make(chan struct{})
	calls := &atomic.Int64{}
	registry := NewRegistry()
	registry.Register(registrationTool("target", calls))
	executor := NewExecutor(registry, ExecutorConfig{Before: []BeforeHook{func(context.Context, *HookContext) error {
		close(entered)
		<-approve
		return nil
	}}})
	done := make(chan ToolResult, 1)
	go func() {
		done <- executor.Execute(context.Background(), ToolCall{Name: "target", Arguments: "{}"}, NewState())
	}()
	<-entered
	registry.Register(registrationTool("target", calls))
	close(approve)
	result := <-done
	if !errors.Is(result.Err, ErrStaleToolRegistration) || calls.Load() != 0 {
		t.Fatal("approval for an old registration authorized its replacement", result.Err)
	}
}

func TestToolRegistrationProviderRetryRetainsOriginalStep(t *testing.T) {
	var agent *Agent
	requests := 0
	oldCalls, newCalls := &atomic.Int64{}, &atomic.Int64{}
	old, replacement := registrationTool("target", oldCalls), registrationTool("target", newCalls)
	old.Description, replacement.Description = "old definition", "new definition"
	provider := registrationProvider{complete: func(_ context.Context, req *ProviderRequest) (*ProviderResponse, error) {
		requests++
		expected := "old definition"
		if requests >= 3 {
			expected = "new definition"
		}
		if len(req.Tools) != 1 || req.Tools[0].Description != expected {
			t.Fatal("retry silently rebound its tool definition")
		}
		if requests == 1 {
			agent.RegisterTools(replacement)
			return nil, errors.New("429 rate limit exceeded")
		}
		if requests <= 3 {
			return &ProviderResponse{ToolCalls: []ToolCall{{ID: fmt.Sprint(requests), Name: "target", Arguments: "{}"}}}, nil
		}
		return &ProviderResponse{Content: "done"}, nil
	}}
	cfg := Config{ModelConfig: ModelConfig{Provider: provider}}
	cfg.RetryConfig = &RetryConfig{MaxRetries: 1, BaseDelayMs: 1, MaxDelayMs: 1}
	agent = New(cfg)
	agent.RegisterTools(old)
	if _, err := agent.Run(context.Background(), "test"); err != nil || requests != 4 || oldCalls.Load() != 0 || newCalls.Load() != 1 {
		t.Fatalf("retry adopted a new registration: requests=%d old=%d new=%d error=%v", requests, oldCalls.Load(), newCalls.Load(), err)
	}
}

func TestToolRegistrationCancellationExpiresRetainedContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var retained context.Context
	calls := &atomic.Int64{}
	provider := registrationProvider{complete: func(ctx context.Context, _ *ProviderRequest) (*ProviderResponse, error) {
		retained = ctx
		cancel()
		return nil, ctx.Err()
	}}
	agent := New(Config{ModelConfig: ModelConfig{Provider: provider}})
	agent.RegisterTools(registrationTool("target", calls))
	_, _ = agent.Run(ctx, "test")
	if retained == nil {
		t.Fatal("test did not enter the provider")
	}
	if _, err := agent.InvokeTool(context.WithoutCancel(retained), "target", json.RawMessage("{}")); !errors.Is(err, ErrToolStepClosed) || calls.Load() != 0 {
		t.Fatal("cancellation left a refreshable registration scope", err)
	}
}

func TestToolRegistrationIndependentRegistriesAndConcurrentReplacement(t *testing.T) {
	first, second := New(Config{}), New(Config{})
	calls := &atomic.Int64{}
	first.RegisterTools(registrationTool("same", calls))
	second.RegisterTools(registrationTool("same", calls))
	step := first.registry.captureStep()
	ctx := withToolStep(context.Background(), step)
	step.close()
	if _, err := second.InvokeTool(ctx, "same", json.RawMessage("{}")); err != nil {
		t.Fatal("another registry inherited an unrelated expired scope", err)
	}
	first.RegisterTools(registrationTool("same", calls))
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				first.RegisterTools(registrationTool("same", calls))
				_, err := first.InvokeTool(context.Background(), "same", json.RawMessage("{}"))
				if err != nil && !errors.Is(err, ErrStaleToolRegistration) {
					t.Errorf("unexpected concurrent dispatch failure: %v", err)
				}
			}
		}()
	}
	wg.Wait()
}
