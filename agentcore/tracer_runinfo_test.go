package agentcore

import (
	"context"
	"encoding/json"
	"testing"
)

func TestStartComponentRunBuildsParentChildTree(t *testing.T) {
	rootCtx, rootSpan, root := StartComponentRun(context.Background(), nil, "agent", "root")
	defer rootSpan.End()
	childCtx, childSpan, child := StartComponentRun(rootCtx, nil, "tool", "search")
	defer childSpan.End()

	if root.RunID == "" || child.RunID == "" || child.ParentRunID != root.RunID {
		t.Fatalf("root = %#v, child = %#v", root, child)
	}
	if len(child.Path) != 2 || child.Path[0] != "root" || child.Path[1] != "search" {
		t.Fatalf("child path = %#v", child.Path)
	}
	fromContext, ok := RunInfoFromContext(childCtx)
	if !ok || fromContext.RunID != child.RunID {
		t.Fatalf("context run info = %#v, %v", fromContext, ok)
	}
}

type runInfoToolProvider struct {
	calls int
}

func (provider *runInfoToolProvider) Complete(context.Context, *ProviderRequest) (*ProviderResponse, error) {
	provider.calls++
	if provider.calls == 1 {
		return &ProviderResponse{ToolCalls: []ToolCall{{ID: "call-1", Name: "inspect", Arguments: `{}`}}}, nil
	}
	return &ProviderResponse{Content: "done"}, nil
}

func (*runInfoToolProvider) Stream(context.Context, *ProviderRequest) (<-chan StreamDelta, error) {
	return nil, nil
}

func TestAgentToolReceivesChildRunInfoWithoutTracer(t *testing.T) {
	var toolRun RunInfo
	provider := &runInfoToolProvider{}
	agent := New(StubConfig(provider, WithTools(&Tool{
		Name:        "inspect",
		Description: "inspect run metadata",
		Parameters:  map[string]any{"type": "object"},
		Func: func(ctx context.Context, _ json.RawMessage) (any, error) {
			toolRun, _ = RunInfoFromContext(ctx)
			return "ok", nil
		},
	})))
	if _, err := agent.Run(context.Background(), "inspect"); err != nil {
		t.Fatal(err)
	}
	if toolRun.Component != "tool" || toolRun.Name != "inspect" || toolRun.ParentRunID == "" {
		t.Fatalf("tool run info = %#v", toolRun)
	}
}

func TestResumeEventsHaveRootRunInfo(t *testing.T) {
	agent := New(StubConfig(&guardrailRejectProvider{}))
	agent.interrupted = &InterruptReason{Reason: "pause"}
	agent.state.SetStatus(StatusInterrupted)
	var info RunInfo
	agent.On(EventAgentStart, func(event Event) {
		info, _ = EventRunInfo(event)
	})
	if _, err := agent.Resume(context.Background()); err != nil {
		t.Fatal(err)
	}
	if info.RunID == "" || info.Component != "agent" || info.ParentRunID != "" {
		t.Fatalf("resume run info = %#v", info)
	}
}
