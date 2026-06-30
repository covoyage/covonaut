package agentcore

import (
	"strings"
	"testing"
)

func TestExtractJSONObject_plain(t *testing.T) {
	raw := `  {"a":1}  `
	got := extractJSONObject(raw)
	if got != `{"a":1}` {
		t.Fatalf("got %q", got)
	}
}

func TestExtractJSONObject_fenced(t *testing.T) {
	raw := "```json\n{\"active_task\":\"x\"}\n```"
	got := extractJSONObject(raw)
	if !strings.Contains(got, "active_task") {
		t.Fatalf("got %q", got)
	}
}

func TestParseStructuredCompactionSummary(t *testing.T) {
	raw := `{"active_task":"build login","goal":"add OAuth","completed_actions":"1. wrote handler","active_state":"main branch","blocked":"rate limit","key_decisions":"use PKCE"}`
	s, err := parseStructuredCompactionSummary(raw)
	if err != nil {
		t.Fatal(err)
	}
	if s.ActiveTask != "build login" || s.Goal != "add OAuth" || s.ActiveState != "main branch" {
		t.Fatalf("%+v", s)
	}
}
