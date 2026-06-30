package agentadapter

import (
	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/tui/chat"
)

func BindAgent(sub chat.Subscriber, agent *agentcore.Agent) {
	adapter := &subscriberAdapter{agent: agent}
	sub.Subscribe(adapter)
}

type subscriberAdapter struct {
	agent *agentcore.Agent
}

func (s *subscriberAdapter) On(eventType chat.ChatEventType, handler func(chat.ChatEvent)) {
	switch eventType {
	case chat.ChatEventAgentStart:
		s.agent.On(agentcore.EventAgentStart, func(e agentcore.Event) {
			ev, ok := e.(*agentcore.AgentStartEvent)
			if !ok {
				return
			}
			handler(chat.AgentStartChatEvent{AgentName: ev.AgentName, Input: ev.Input})
		})
	case chat.ChatEventAgentEnd:
		s.agent.On(agentcore.EventAgentEnd, func(e agentcore.Event) {
			ev, ok := e.(*agentcore.AgentEndEvent)
			if !ok {
				return
			}
			handler(chat.AgentEndChatEvent{AgentName: ev.AgentName, Output: ev.Output})
		})
	case chat.ChatEventAgentError:
		s.agent.On(agentcore.EventAgentError, func(e agentcore.Event) {
			ev, ok := e.(*agentcore.AgentErrorEvent)
			if !ok {
				return
			}
			handler(chat.AgentErrorChatEvent{Err: ev.Err})
		})
	case chat.ChatEventTurnStart:
		s.agent.On(agentcore.EventTurnStart, func(e agentcore.Event) {
			ev, ok := e.(*agentcore.TurnStartEvent)
			if !ok {
				return
			}
			handler(chat.TurnStartChatEvent{Turn: ev.Turn})
		})
	case chat.ChatEventTurnEnd:
		s.agent.On(agentcore.EventTurnEnd, func(e agentcore.Event) {
			ev, ok := e.(*agentcore.TurnEndEvent)
			if !ok {
				return
			}
			handler(chat.TurnEndChatEvent{Turn: ev.Turn, Usage: convertUsage(ev.Usage)})
		})
	case chat.ChatEventMessageDelta:
		s.agent.On(agentcore.EventMessageDelta, func(e agentcore.Event) {
			ev, ok := e.(*agentcore.MessageDeltaEvent)
			if !ok {
				return
			}
			handler(chat.MessageDeltaChatEvent{Delta: ev.Delta, Kind: string(ev.Kind)})
		})
	case chat.ChatEventToolCallStart:
		s.agent.On(agentcore.EventToolCallStart, func(e agentcore.Event) {
			ev, ok := e.(*agentcore.ToolCallStartEvent)
			if !ok {
				return
			}
			handler(chat.ToolCallStartChatEvent{ToolCall: chat.ToolCallInfo{
				ID: ev.ToolCall.ID, Name: ev.ToolCall.Name, Arguments: ev.ToolCall.Arguments,
			}})
		})
	case chat.ChatEventToolCallEnd:
		s.agent.On(agentcore.EventToolCallEnd, func(e agentcore.Event) {
			ev, ok := e.(*agentcore.ToolCallEndEvent)
			if !ok {
				return
			}
			handler(chat.ToolCallEndChatEvent{
				ToolCallID: ev.ToolCallID, ToolName: ev.ToolName,
				Result: ev.Result, Err: ev.Err, Duration: ev.Duration,
			})
		})
	case chat.ChatEventHandoffStart:
		s.agent.On(agentcore.EventHandoffStart, func(e agentcore.Event) {
			ev, ok := e.(*agentcore.HandoffStartEvent)
			if !ok {
				return
			}
			handler(chat.HandoffStartChatEvent{
				SourceAgent: ev.SourceAgent, TargetAgent: ev.TargetAgent,
				Mode: ev.Mode, Context: ev.Context,
			})
		})
	case chat.ChatEventHandoffEnd:
		s.agent.On(agentcore.EventHandoffEnd, func(e agentcore.Event) {
			ev, ok := e.(*agentcore.HandoffEndEvent)
			if !ok {
				return
			}
			handler(chat.HandoffEndChatEvent{
				TargetAgent: ev.TargetAgent, Output: ev.Output,
				Duration: ev.Duration, Err: ev.Err,
			})
		})
	case chat.ChatEventCompactionStart:
		s.agent.On(agentcore.EventCompactionStart, func(e agentcore.Event) {
			ev, ok := e.(*agentcore.CompactionStartEvent)
			if !ok {
				return
			}
			handler(chat.CompactionStartChatEvent{
				TokensBefore: ev.TokensBefore, ContextWindow: ev.ContextWindow,
			})
		})
	case chat.ChatEventCompactionEnd:
		s.agent.On(agentcore.EventCompactionEnd, func(e agentcore.Event) {
			ev, ok := e.(*agentcore.CompactionEndEvent)
			if !ok {
				return
			}
			handler(chat.CompactionEndChatEvent{
				TokensBefore: ev.TokensBefore, TokensAfter: ev.TokensAfter,
				MessagesCut: ev.MessagesCut, Duration: ev.Duration,
			})
		})
	case chat.ChatEventAutoRetry:
		s.agent.On(agentcore.EventAutoRetry, func(e agentcore.Event) {
			ev, ok := e.(*agentcore.AutoRetryEvent)
			if !ok {
				return
			}
			handler(chat.AutoRetryChatEvent{
				Attempt: ev.Attempt, MaxRetries: ev.MaxRetries,
				Delay: ev.Delay, Err: ev.Err,
			})
		})
	}
}

func convertUsage(u agentcore.TokenUsage) chat.TokenUsage {
	return chat.TokenUsage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
	}
}
