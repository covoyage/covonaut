package agentcore

import "encoding/json"

// EstimateTokens returns a rough token count using the chars/4 heuristic.
func EstimateTokens(text string) int64 {
	if len(text) == 0 {
		return 0
	}
	return int64(len(text)+3) / 4
}

// EstimateMessageTokens estimates token count for a single message,
// including role overhead, content, and tool call payloads.
func EstimateMessageTokens(msg Message) int64 {
	tokens := int64(4) // role + message framing overhead
	tokens += EstimateTokens(MessageTextBody(msg))
	tokens += EstimateTokens(msg.Name)
	for _, tc := range msg.ToolCalls {
		tokens += EstimateTokens(tc.Name)
		tokens += EstimateTokens(tc.Arguments)
		tokens += 4 // per-tool-call overhead
	}
	return tokens
}

// EstimateMessagesTokens estimates total token count for a slice of messages.
func EstimateMessagesTokens(msgs []Message) int64 {
	total := int64(3) // conversation framing overhead
	for _, msg := range msgs {
		total += EstimateMessageTokens(msg)
	}
	return total
}

// EstimateToolDefinitionsTokens estimates token overhead of tool definitions
// in the request (they count against the context window).
func EstimateToolDefinitionsTokens(defs []ToolDefinition) int64 {
	if len(defs) == 0 {
		return 0
	}
	total := int64(0)
	for _, def := range defs {
		total += EstimateTokens(def.Name)
		total += EstimateTokens(def.Description)
		if def.Parameters != nil {
			if data, err := json.Marshal(def.Parameters); err == nil {
				total += EstimateTokens(string(data))
			}
		}
		total += 10 // per-tool overhead (type, function wrapper, etc.)
	}
	return total
}
