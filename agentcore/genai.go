package agentcore

import (
	"encoding/json"
	"unicode/utf8"
)

// maxGenAIAttributeBytes caps the size of gen_ai.prompt / gen_ai.completion
// span attributes. Observability backends (e.g. Langfuse) drop oversized
// spans, so guard against pathological prompt histories blowing the limit.
const maxGenAIAttributeBytes = 256 * 1024

// genAIPromptFromMessages serializes the request messages into a JSON string
// suitable for the gen_ai.prompt span attribute. Returns "" when there is
// nothing to report.
func genAIPromptFromMessages(messages []Message) string {
	if len(messages) == 0 {
		return ""
	}
	b, err := json.Marshal(messages)
	if err != nil {
		return ""
	}
	return truncateGenAIContent(string(b))
}

// truncateGenAIContent truncates s to maxGenAIAttributeBytes without
// splitting a UTF-8 rune, appending a marker when truncation happened.
func truncateGenAIContent(s string) string {
	if len(s) <= maxGenAIAttributeBytes {
		return s
	}
	cut := maxGenAIAttributeBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "\n...[truncated]"
}
