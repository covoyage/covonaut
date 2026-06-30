package graph

import (
	"testing"

	"github.com/covoyage/covonaut/agentcore"
)

func TestPregelState_Clone_DeepCopy(t *testing.T) {
	original := PregelState{
		"input":   "hello",
		"counter": int64(42),
		"nested": map[string]any{
			"key":  "val",
			"nums": []any{int64(1), int64(2), int64(3)},
		},
		"messages": []agentcore.Message{
			{Role: agentcore.RoleUser, Content: "hi", ToolCalls: []agentcore.ToolCall{{ID: "tc1", Name: "tool"}}},
		},
	}

	cloned := original.Clone()

	// Verify values match
	if cloned["input"] != "hello" {
		t.Fatal("input mismatch")
	}
	if cloned["counter"] != int64(42) {
		t.Fatal("counter mismatch")
	}
	msgs, ok := cloned["messages"].([]agentcore.Message)
	if !ok || len(msgs) != 1 || msgs[0].Content != "hi" {
		t.Fatal("messages mismatch")
	}

	// Mutate cloned nested map — must not affect original
	nested := cloned["nested"].(map[string]any)
	nested["key"] = "mutated"

	origNested := original["nested"].(map[string]any)
	if origNested["key"] == "mutated" {
		t.Fatal("Clone() shallow-copied nested map — mutation leaked to original")
	}

	// Mutate cloned messages — must not affect original
	msgs[0].ToolCalls[0].Name = "mutated"
	origMsgs := original["messages"].([]agentcore.Message)
	if origMsgs[0].ToolCalls[0].Name == "mutated" {
		t.Fatal("Clone() shallow-copied Messages — mutation leaked to original")
	}
}

func TestPregelState_Clone_PreservesInt64(t *testing.T) {
	// JSON round-trip converts int64 to float64; our Clone must preserve the type.
	original := PregelState{
		"count": int64(42),
	}
	cloned := original.Clone()

	count, ok := cloned["count"].(int64)
	if !ok {
		t.Fatalf("count type = %T, want int64", cloned["count"])
	}
	if count != 42 {
		t.Fatalf("count = %d, want 42", count)
	}
}

func TestPregelState_GetMessages(t *testing.T) {
	msgs := []agentcore.Message{
		{Role: agentcore.RoleUser, Content: "hello"},
	}
	state := PregelState{"msgs": msgs}

	got := state.GetMessages("msgs")
	if len(got) != 1 || got[0].Content != "hello" {
		t.Fatalf("GetMessages = %+v, want 1 message", got)
	}

	// Missing key
	if got := state.GetMessages("nonexistent"); got != nil {
		t.Fatalf("GetMessages(nonexistent) = %+v, want nil", got)
	}

	// Non-message value
	state["bad"] = "not messages"
	if got := state.GetMessages("bad"); got != nil {
		t.Fatalf("GetMessages(bad) = %+v, want nil", got)
	}
}

func TestPregelState_Clone_TypedMapsAndSlices(t *testing.T) {
	original := PregelState{
		"strMap":  map[string]string{"a": "1", "b": "2"},
		"strSlice": []string{"x", "y", "z"},
		"intSlice": []int{1, 2, 3},
	}

	cloned := original.Clone()

	// Mutate cloned typed map
	clonedMap := cloned["strMap"].(map[string]string)
	clonedMap["a"] = "mutated"
	if original["strMap"].(map[string]string)["a"] == "mutated" {
		t.Fatal("Clone() shallow-copied map[string]string — mutation leaked")
	}

	// Mutate cloned string slice
	clonedSlice := cloned["strSlice"].([]string)
	clonedSlice[0] = "mutated"
	if original["strSlice"].([]string)[0] == "mutated" {
		t.Fatal("Clone() shallow-copied []string — mutation leaked")
	}

	// Mutate cloned int slice
	clonedInts := cloned["intSlice"].([]int)
	clonedInts[0] = 999
	if original["intSlice"].([]int)[0] == 999 {
		t.Fatal("Clone() shallow-copied []int — mutation leaked")
	}
}

func TestPregelState_Clone_NilValue(t *testing.T) {
	original := PregelState{"nil": nil}
	cloned := original.Clone()
	if cloned["nil"] != nil {
		t.Fatal("Clone() did not preserve nil value")
	}
}
