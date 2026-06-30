package agentadapter

import (
	"testing"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/tui/chat"
)

func TestConvertUsage(t *testing.T) {
	u := agentcore.TokenUsage{
		PromptTokens:     100,
		CompletionTokens: 50,
		TotalTokens:      150,
	}
	cu := convertUsage(u)
	if cu.PromptTokens != 100 || cu.CompletionTokens != 50 || cu.TotalTokens != 150 {
		t.Fatalf("convertUsage = %+v", cu)
	}
}

func TestConvertUsage_Zero(t *testing.T) {
	cu := convertUsage(agentcore.TokenUsage{})
	if cu.PromptTokens != 0 || cu.CompletionTokens != 0 || cu.TotalTokens != 0 {
		t.Fatalf("expected zero, got %+v", cu)
	}
}

func newAgentWithBus() *agentcore.Agent {
	a := &agentcore.Agent{}
	a.SetEventBus(agentcore.NewEventBus())
	return a
}

type mockSubscriber struct {
	sub chat.EventSubscriber
}

func (s *mockSubscriber) Subscribe(sub chat.EventSubscriber) {
	s.sub = sub
}

func TestBindAgent(t *testing.T) {
	agent := newAgentWithBus()
	sub := &mockSubscriber{}

	// should not panic
	BindAgent(sub, agent)
}

func TestAdapterSubscriberOn(t *testing.T) {
	agent := newAgentWithBus()
	adapter := &subscriberAdapter{agent: agent}

	events := []chat.ChatEventType{
		chat.ChatEventAgentStart,
		chat.ChatEventAgentEnd,
		chat.ChatEventAgentError,
		chat.ChatEventTurnStart,
		chat.ChatEventTurnEnd,
		chat.ChatEventMessageDelta,
		chat.ChatEventToolCallStart,
		chat.ChatEventToolCallEnd,
		chat.ChatEventHandoffStart,
		chat.ChatEventHandoffEnd,
		chat.ChatEventCompactionStart,
		chat.ChatEventCompactionEnd,
		chat.ChatEventAutoRetry,
	}

	for _, ev := range events {
		called := false
		adapter.On(ev, func(ce chat.ChatEvent) {
			called = true
		})
		_ = called
	}
}
