package chat

import "time"

type EventSubscriber interface {
	On(eventType ChatEventType, handler func(ChatEvent))
}

type Subscriber interface {
	Subscribe(sub EventSubscriber)
}

type ChatEventType string

const (
	ChatEventAgentStart      ChatEventType = "agent_start"
	ChatEventAgentEnd        ChatEventType = "agent_end"
	ChatEventAgentError      ChatEventType = "agent_error"
	ChatEventTurnStart       ChatEventType = "turn_start"
	ChatEventTurnEnd         ChatEventType = "turn_end"
	ChatEventMessageDelta    ChatEventType = "message_delta"
	ChatEventToolCallStart   ChatEventType = "tool_call_start"
	ChatEventToolCallEnd     ChatEventType = "tool_call_end"
	ChatEventHandoffStart    ChatEventType = "handoff_start"
	ChatEventHandoffEnd      ChatEventType = "handoff_end"
	ChatEventCompactionStart ChatEventType = "compaction_start"
	ChatEventCompactionEnd   ChatEventType = "compaction_end"
	ChatEventAutoRetry       ChatEventType = "auto_retry"
)

type ChatEvent interface {
	ChatEventKind() ChatEventType
}

type AgentStartChatEvent struct {
	AgentName string
	Input     string
}

func (AgentStartChatEvent) ChatEventKind() ChatEventType { return ChatEventAgentStart }

type AgentEndChatEvent struct {
	AgentName string
	Output    string
}

func (AgentEndChatEvent) ChatEventKind() ChatEventType { return ChatEventAgentEnd }

type AgentErrorChatEvent struct {
	Err error
}

func (AgentErrorChatEvent) ChatEventKind() ChatEventType { return ChatEventAgentError }

type TurnStartChatEvent struct {
	Turn int64
}

func (TurnStartChatEvent) ChatEventKind() ChatEventType { return ChatEventTurnStart }

type TurnEndChatEvent struct {
	Turn  int64
	Usage TokenUsage
}

func (TurnEndChatEvent) ChatEventKind() ChatEventType { return ChatEventTurnEnd }

type MessageDeltaChatEvent struct {
	Delta string
	Kind  string // "text" or "thinking"
}

func (MessageDeltaChatEvent) ChatEventKind() ChatEventType { return ChatEventMessageDelta }

type ToolCallInfo struct {
	ID        string
	Name      string
	Arguments string
}

type ToolCallStartChatEvent struct {
	ToolCall ToolCallInfo
}

func (ToolCallStartChatEvent) ChatEventKind() ChatEventType { return ChatEventToolCallStart }

type ToolCallEndChatEvent struct {
	ToolCallID string
	ToolName   string
	Result     string
	Err        error
	Duration   time.Duration
}

func (ToolCallEndChatEvent) ChatEventKind() ChatEventType { return ChatEventToolCallEnd }

type HandoffStartChatEvent struct {
	SourceAgent string
	TargetAgent string
	Mode        string
	Context     string
}

func (HandoffStartChatEvent) ChatEventKind() ChatEventType { return ChatEventHandoffStart }

type HandoffEndChatEvent struct {
	TargetAgent string
	Output      string
	Duration    time.Duration
	Err         error
}

func (HandoffEndChatEvent) ChatEventKind() ChatEventType { return ChatEventHandoffEnd }

type CompactionStartChatEvent struct {
	TokensBefore  int64
	ContextWindow int64
}

func (CompactionStartChatEvent) ChatEventKind() ChatEventType { return ChatEventCompactionStart }

type CompactionEndChatEvent struct {
	TokensBefore int64
	TokensAfter  int64
	MessagesCut  int64
	Duration     time.Duration
}

func (CompactionEndChatEvent) ChatEventKind() ChatEventType { return ChatEventCompactionEnd }

type AutoRetryChatEvent struct {
	Attempt    int64
	MaxRetries int64
	Delay      time.Duration
	Err        error
}

func (AutoRetryChatEvent) ChatEventKind() ChatEventType { return ChatEventAutoRetry }

type TokenUsage struct {
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
}
