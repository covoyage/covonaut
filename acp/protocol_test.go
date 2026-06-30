package acp

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestWireFormatCamelCase guards the ACP wire format against regressions to
// snake_case, which would break the official Agent Client Protocol (Zed).
func TestWireFormatCamelCase(t *testing.T) {
	t.Run("initialize result", func(t *testing.T) {
		b, _ := json.Marshal(InitializeResult{
			ProtocolVersion:   1,
			AgentCapabilities: AgentCapabilities{LoadSession: true},
		})
		s := string(b)
		mustContain(t, s, `"protocolVersion"`)
		mustContain(t, s, `"agentCapabilities"`)
		mustContain(t, s, `"loadSession"`)
		mustNotContain(t, s, "protocol_version")
		mustNotContain(t, s, "agent_info") // removed; not in ACP initialize result
	})

	t.Run("new session", func(t *testing.T) {
		b, _ := json.Marshal(NewSessionResult{SessionID: "s1"})
		mustContain(t, string(b), `"sessionId"`)
		mustNotContain(t, string(b), "session_id")
	})

	t.Run("session/update nested with sessionUpdate", func(t *testing.T) {
		b, _ := json.Marshal(SessionNotification{
			SessionID: "s1",
			Update: SessionUpdate{
				SessionUpdate: "agent_message_chunk",
				Content:       TextContentBlock{Type: "text", Text: "hi"},
			},
		})
		s := string(b)
		mustContain(t, s, `"sessionId"`)
		mustContain(t, s, `"update"`)
		mustContain(t, s, `"sessionUpdate"`)
		mustNotContain(t, s, "session_update")
	})

	t.Run("tool_call update fields", func(t *testing.T) {
		b, _ := json.Marshal(SessionNotification{
			SessionID: "s1",
			Update: SessionUpdate{
				SessionUpdate: "tool_call",
				ToolCallID:    "t1",
				Title:         "read foo.go",
				Kind:          "read",
				Status:        "in_progress",
			},
		})
		s := string(b)
		mustContain(t, s, `"toolCallId"`)
		mustNotContain(t, s, "tool_call_id")
		mustNotContain(t, s, "tool_call_start")
	})

	t.Run("prompt response", func(t *testing.T) {
		b, _ := json.Marshal(PromptResponse{StopReason: "end_turn"})
		mustContain(t, string(b), `"stopReason"`)
		mustNotContain(t, string(b), "stop_reason")
	})

	t.Run("permission request", func(t *testing.T) {
		b, _ := json.Marshal(RequestPermissionParams{
			SessionID: "s1",
			ToolCall:  PermissionToolCall{ToolCallID: "t1", Title: "bash"},
			Options:   DefaultPermissionOptions(),
		})
		s := string(b)
		mustContain(t, s, `"sessionId"`)
		mustContain(t, s, `"toolCall"`)
		mustContain(t, s, `"toolCallId"`)
		mustContain(t, s, `"optionId"`)
	})
}

func TestInitializeParamsCamelDecode(t *testing.T) {
	// Zed sends camelCase; ensure we decode it.
	var p InitializeParams
	if err := json.Unmarshal([]byte(`{"protocolVersion":1,"clientInfo":{"name":"zed","version":"1.0"}}`), &p); err != nil {
		t.Fatal(err)
	}
	if p.ProtocolVersion != 1 {
		t.Errorf("protocolVersion not decoded: %d", p.ProtocolVersion)
	}
	if p.ClientInfo == nil || p.ClientInfo.Name != "zed" {
		t.Errorf("clientInfo not decoded: %+v", p.ClientInfo)
	}
}

func TestExtractPromptContent(t *testing.T) {
	blocks := []json.RawMessage{
		json.RawMessage(`{"type":"text","text":"please review"}`),
		json.RawMessage(`{"type":"resource","resource":{"uri":"file:///a/main.go","text":"package main"}}`),
		json.RawMessage(`{"type":"resource_link","uri":"file:///a/util.go","name":"util.go"}`),
		json.RawMessage(`{"type":"image","data":"...","mimeType":"image/png"}`),
		json.RawMessage(`{"type":"audio","data":"..."}`),
		json.RawMessage(`{"type":"unknown_block"}`),
	}
	out := extractPromptContent(blocks)

	for _, want := range []string{
		"please review",
		"package main",      // embedded resource text inlined
		"file:///a/main.go", // with its uri
		"util.go",           // resource_link name
		"file:///a/util.go", // resource_link uri
		"[image attached]",
		"[audio attached]",
	} {
		mustContain(t, out, want)
	}
}

func TestExtractPromptContentTextOnly(t *testing.T) {
	out := extractPromptContent([]json.RawMessage{
		json.RawMessage(`{"type":"text","text":"hello"}`),
	})
	if out != "hello" {
		t.Errorf("got %q, want %q", out, "hello")
	}
}

func mustContain(t *testing.T, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Errorf("expected %q in %s", sub, s)
	}
}

func mustNotContain(t *testing.T, s, sub string) {
	t.Helper()
	if strings.Contains(s, sub) {
		t.Errorf("did not expect %q in %s", sub, s)
	}
}
