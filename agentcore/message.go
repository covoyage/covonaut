package agentcore

// Role represents the sender of a message in a conversation.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// MessageType distinguishes internal message variants from standard LLM messages.
// Standard messages have Type == "" and are sent directly to the provider.
// Custom types are converted via ConvertToLLM before reaching the provider.
type MessageType string

const (
	MessageTypeStandard          MessageType = ""
	MessageTypeCompactionSummary MessageType = "compaction_summary"
	MessageTypeBranchSummary     MessageType = "branch_summary"
	MessageTypeCustom            MessageType = "custom"
)

// CacheControlMarker represents an Anthropic cache_control breakpoint for prompt caching.
// It is placed on messages to mark them as cacheable, which can reduce token costs by ~75%.
type CacheControlMarker struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

// ToolCall represents a function call requested by the model.
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Message represents a single message in the conversation history.
type Message struct {
	// ID optional stable id for merge semantics: AddMessage replaces an existing
	// message with the same non-empty ID instead of appending (LangGraph add_messages style).
	ID         string         `json:"id,omitempty"`
	Role       Role           `json:"role"`
	Content    string         `json:"content,omitempty"`
	ToolCalls  []ToolCall     `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Name       string         `json:"name,omitempty"`
	Type       MessageType    `json:"type,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	// CacheControl is an optional Anthropic cache_control marker for prompt caching.
	// When set, providers that support caching (e.g. Anthropic) will add cache_control
	// breakpoints to the corresponding content blocks.
	CacheControl *CacheControlMarker `json:"cache_control,omitempty"`
	// Blocks optional multi-segment body (for example text, thinking, images).
	// DefaultConvertToLLM collapses text/thinking into Content and preserves any
	// richer blocks for providers that support multipart content.
	Blocks []ContentBlock `json:"blocks,omitempty"`
	// InvocationID optionally correlates this message with one provider call.
	InvocationID string `json:"invocation_id,omitempty"`
}

// IsStandard returns true if the message is a standard LLM message (no custom type).
func (m Message) IsStandard() bool {
	return m.Type == "" || m.Type == MessageTypeStandard
}

// Clone returns a deep copy of the message. All reference-type fields (ToolCalls,
// Blocks, Metadata) are independently copied so that mutations to the clone never
// affect the original.
func (m Message) Clone() Message {
	cp := m
	if len(m.ToolCalls) > 0 {
		cp.ToolCalls = make([]ToolCall, len(m.ToolCalls))
		copy(cp.ToolCalls, m.ToolCalls)
	}
	if len(m.Blocks) > 0 {
		cp.Blocks = make([]ContentBlock, len(m.Blocks))
		copy(cp.Blocks, m.Blocks)
	}
	if len(m.Metadata) > 0 {
		cp.Metadata = deepCopyMap(m.Metadata)
	}
	return cp
}

func deepCopyMap(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = deepCopyAny(v)
	}
	return dst
}

func deepCopyAny(v any) any {
	switch val := v.(type) {
	case map[string]any:
		return deepCopyMap(val)
	case []any:
		cp := make([]any, len(val))
		for i, elem := range val {
			cp[i] = deepCopyAny(elem)
		}
		return cp
	default:
		return v
	}
}

// ConvertToLLMFunc transforms internal messages into the format expected by the provider.
// It should filter out UI-only messages, convert custom types to standard roles, etc.
type ConvertToLLMFunc func(msgs []Message) []Message

// DefaultConvertToLLM keeps standard messages as-is and strips custom types
// down to their basic role + content.
func DefaultConvertToLLM(msgs []Message) []Message {
	out := make([]Message, 0, len(msgs))
	for _, msg := range msgs {
		if msg.IsStandard() {
			if len(msg.Blocks) > 0 {
				msg = MessageCollapseForLLM(msg, false)
			}
			out = append(out, msg)
			continue
		}
		switch msg.Type {
		case MessageTypeCompactionSummary, MessageTypeBranchSummary:
			out = append(out, Message{
				Role:       msg.Role,
				Content:    msg.Content,
				ToolCalls:  msg.ToolCalls,
				ToolCallID: msg.ToolCallID,
				Name:       msg.Name,
			})
		case MessageTypeCustom:
			// Skip custom-only messages by default
		default:
			out = append(out, Message{
				Role:    msg.Role,
				Content: msg.Content,
			})
		}
	}
	return out
}
