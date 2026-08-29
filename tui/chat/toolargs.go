package chat

import (
	"encoding/json"
	"strings"
)

// argPreviewKeys lists the JSON argument keys checked in priority order when
// distilling a tool call into a short inline preview.
var argPreviewKeys = []string{"command", "file_path", "path", "pattern", "query", "url", "name"}

// ToolArgPreview picks the most telling argument value from a tool call's
// JSON arguments (e.g. the shell command, the file path). The value is
// returned un-truncated so renderers can fit it to the terminal width; a
// hard cap of 2000 runes guards against pathological arguments. max <= 0
// means "hard cap only".
func ToolArgPreview(arguments string, max int) string {
	if arguments == "" {
		return ""
	}
	if max <= 0 {
		max = 2000
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(arguments), &fields); err != nil {
		return ""
	}
	for _, key := range argPreviewKeys {
		value, ok := fields[key].(string)
		if !ok || value == "" {
			continue
		}
		if i := strings.IndexAny(value, "\r\n"); i >= 0 {
			value = value[:i]
		}
		runes := []rune(value)
		if len(runes) > max {
			value = string(runes[:max]) + "…"
		}
		return value
	}
	return ""
}
