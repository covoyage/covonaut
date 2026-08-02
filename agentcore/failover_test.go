package agentcore

import (
	"context"
	"errors"
	"testing"
)

type failoverStubProvider struct {
	err      error
	response string
	calls    int
}

func (p *failoverStubProvider) Complete(context.Context, *ProviderRequest) (*ProviderResponse, error) {
	p.calls++
	if p.err != nil {
		return nil, p.err
	}
	return &ProviderResponse{Content: p.response}, nil
}

func (p *failoverStubProvider) Stream(context.Context, *ProviderRequest) (<-chan StreamDelta, error) {
	return nil, p.err
}

func TestAgentModelFailoverRemembersSuccessfulTarget(t *testing.T) {
	primary := &failoverStubProvider{err: errors.New("503 unavailable")}
	fallback := &failoverStubProvider{response: "fallback response"}
	agent := New(StubConfig(primary, WithModelFailover(&ModelFailoverConfig{
		Targets: []ModelTarget{{Name: "fallback", Model: "backup", Provider: fallback}},
	})))

	first, err := agent.Run(context.Background(), "one")
	if err != nil || first != "fallback response" {
		t.Fatalf("first run = %q, %v", first, err)
	}
	second, err := agent.Run(context.Background(), "two")
	if err != nil || second != "fallback response" {
		t.Fatalf("second run = %q, %v", second, err)
	}
	if primary.calls != 1 {
		t.Fatalf("primary calls = %d, want 1", primary.calls)
	}
	if fallback.calls != 2 {
		t.Fatalf("fallback calls = %d, want 2", fallback.calls)
	}
}

type partialFailureProvider struct{}

func (partialFailureProvider) Complete(context.Context, *ProviderRequest) (*ProviderResponse, error) {
	return nil, errors.New("503 unavailable")
}

func (partialFailureProvider) Stream(context.Context, *ProviderRequest) (<-chan StreamDelta, error) {
	stream := make(chan StreamDelta, 2)
	stream <- StreamDelta{Content: "partial"}
	stream <- StreamDelta{Err: errors.New("503 stream unavailable")}
	close(stream)
	return stream, nil
}

type successfulStreamProvider struct{}

func (successfulStreamProvider) Complete(context.Context, *ProviderRequest) (*ProviderResponse, error) {
	return &ProviderResponse{Content: "fallback"}, nil
}

func (successfulStreamProvider) Stream(context.Context, *ProviderRequest) (<-chan StreamDelta, error) {
	stream := make(chan StreamDelta, 1)
	stream <- StreamDelta{Content: "fallback", Done: true}
	close(stream)
	return stream, nil
}

func TestStreamingFailoverReceivesPartialResponse(t *testing.T) {
	var partial string
	var events []Event
	agent := New(StubConfig(partialFailureProvider{},
		WithStreaming(true),
		WithModelFailover(&ModelFailoverConfig{
			MaxAttempts: 1,
			SelectTarget: func(_ context.Context, failure ModelFailoverContext) (ModelTarget, error) {
				if failure.LastResponse != nil {
					partial = failure.LastResponse.Content
				}
				return ModelTarget{Name: "fallback", Provider: successfulStreamProvider{}}, nil
			},
		}),
	))
	agent.OnAll(func(event Event) {
		switch event.EventKind() {
		case EventMessageDelta, EventMessageReset, EventModelFailover:
			events = append(events, event)
		}
	})
	output, err := agent.Run(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	if partial != "partial" || output != "fallback" {
		t.Fatalf("partial = %q, output = %q", partial, output)
	}
	if len(events) != 4 {
		t.Fatalf("events = %#v", events)
	}
	firstDelta, ok := events[0].(*MessageDeltaEvent)
	if !ok || firstDelta.Delta != "partial" || firstDelta.AttemptID == "" {
		t.Fatalf("first event = %#v", events[0])
	}
	reset, ok := events[1].(*MessageResetEvent)
	if !ok || reset.AttemptID != firstDelta.AttemptID {
		t.Fatalf("reset = %#v, first delta = %#v", events[1], firstDelta)
	}
	if _, ok := events[2].(*ModelFailoverEvent); !ok {
		t.Fatalf("third event = %#v", events[2])
	}
	fallbackDelta, ok := events[3].(*MessageDeltaEvent)
	if !ok || fallbackDelta.Delta != "fallback" || fallbackDelta.AttemptID == firstDelta.AttemptID {
		t.Fatalf("fallback event = %#v", events[3])
	}
	runInfo, ok := EventRunInfo(firstDelta)
	if !ok || runInfo.Component != "model" || runInfo.ParentRunID == "" {
		t.Fatalf("delta run info = %#v, %v", runInfo, ok)
	}
}

func TestOrderedFailoverAdvancesPastLastTarget(t *testing.T) {
	config := &ModelFailoverConfig{Targets: []ModelTarget{
		{Name: "first"},
		{Name: "second"},
	}}
	target, err := config.target(context.Background(), ModelFailoverContext{
		Attempt:    1,
		LastTarget: ModelTarget{Name: "first"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if target.Name != "second" {
		t.Fatalf("target = %#v", target)
	}
}
