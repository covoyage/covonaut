package agentcore

import (
	"context"
	"testing"
	"time"
)

type mixedBlockStreamProvider struct{}

func (mixedBlockStreamProvider) Complete(context.Context, *ProviderRequest) (*ProviderResponse, error) {
	return nil, nil
}

func (mixedBlockStreamProvider) Stream(context.Context, *ProviderRequest) (<-chan StreamDelta, error) {
	ch := make(chan StreamDelta, 1)
	ch <- StreamDelta{
		Content: "answer",
		Blocks: []ContentBlock{
			{Kind: BlockKindText, Text: "answer"},
			{Kind: BlockKindThinking, Text: "reasoning"},
		},
	}
	close(ch)
	return ch, nil
}

func TestRunStreamingEmitsTextAndThinkingSeparately(t *testing.T) {
	agent := New(StubConfig(mixedBlockStreamProvider{}))
	eventCh := make(chan *MessageDeltaEvent, 2)
	agent.On(EventMessageDelta, func(event Event) {
		eventCh <- event.(*MessageDeltaEvent)
	})

	response, err := agent.runStreaming(context.Background(), &ProviderRequest{})
	if err != nil {
		t.Fatal(err)
	}
	events := make([]*MessageDeltaEvent, 0, 2)
	for len(events) < 2 {
		select {
		case event := <-eventCh:
			events = append(events, event)
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for message delta events; got %+v", events)
		}
	}
	if events[0].Delta != "answer" || events[0].Kind != BlockKindText {
		t.Fatalf("text event = %+v", events[0])
	}
	if events[1].Delta != "reasoning" || events[1].Kind != BlockKindThinking {
		t.Fatalf("thinking event = %+v", events[1])
	}
	if response.Content != "answer" || len(response.Blocks) != 2 {
		t.Fatalf("response = %+v", response)
	}
}

type mirroredThinkingStreamProvider struct{}

func (mirroredThinkingStreamProvider) Complete(context.Context, *ProviderRequest) (*ProviderResponse, error) {
	return nil, nil
}

func (mirroredThinkingStreamProvider) Stream(context.Context, *ProviderRequest) (<-chan StreamDelta, error) {
	ch := make(chan StreamDelta, 1)
	ch <- StreamDelta{
		Content: "Let me inspect the two relevant files.",
		Blocks: []ContentBlock{
			{Kind: BlockKindText, Text: "Let me inspect the two relevant files."},
			{Kind: BlockKindThinking, Text: "Let me inspect the two relevant files."},
		},
	}
	close(ch)
	return ch, nil
}

func TestRunStreamingDoesNotEmitMirroredThinkingTwice(t *testing.T) {
	agent := New(StubConfig(mirroredThinkingStreamProvider{}))
	eventCh := make(chan *MessageDeltaEvent, 2)
	agent.On(EventMessageDelta, func(event Event) {
		eventCh <- event.(*MessageDeltaEvent)
	})

	if _, err := agent.runStreaming(context.Background(), &ProviderRequest{}); err != nil {
		t.Fatal(err)
	}

	select {
	case event := <-eventCh:
		if event.Delta != "Let me inspect the two relevant files." {
			t.Fatalf("event = %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message delta event")
	}
	select {
	case event := <-eventCh:
		t.Fatalf("mirrored narration emitted twice; extra event = %+v", event)
	case <-time.After(20 * time.Millisecond):
	}
}
