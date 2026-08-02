package agentcore

import (
	"reflect"
	"testing"
)

func TestNormalizeToolCallHistoryPatchesOnlyMissingResults(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: "start"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{
			{ID: "done", Name: "read"},
			{ID: "missing", Name: "write"},
		}},
		{Role: RoleTool, ToolCallID: "done", Name: "read", Content: "ok"},
		{Role: RoleUser, Content: "continue"},
	}

	got := NormalizeToolCallHistory(msgs)
	if len(got) != len(msgs)+1 {
		t.Fatalf("len = %d, want %d", len(got), len(msgs)+1)
	}
	if !reflect.DeepEqual(got[2], msgs[2]) {
		t.Fatalf("existing result changed: %#v", got[2])
	}
	patched := got[3]
	if patched.Role != RoleTool || patched.ToolCallID != "missing" || patched.Name != "write" {
		t.Fatalf("patched result = %#v", patched)
	}
	if !reflect.DeepEqual(got[4], msgs[3]) {
		t.Fatalf("next message moved incorrectly: %#v", got[4])
	}
	if len(msgs) != 4 {
		t.Fatalf("input mutated: %#v", msgs)
	}
}
