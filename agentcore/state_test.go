package agentcore

import (
	"testing"
)

func TestAgentState_AddMessage_mergeByID(t *testing.T) {
	s := NewState()
	s.AddMessage(Message{ID: "1", Role: RoleUser, Content: "a"})
	s.AddMessage(Message{ID: "2", Role: RoleAssistant, Content: "b"})
	s.AddMessage(Message{ID: "1", Role: RoleUser, Content: "a2"})
	msgs := s.Messages()
	if len(msgs) != 2 {
		t.Fatalf("len=%d want 2: %+v", len(msgs), msgs)
	}
	if msgs[0].Content != "a2" {
		t.Fatalf("merged content: %q", msgs[0].Content)
	}
	if msgs[1].Content != "b" {
		t.Fatalf("second msg: %+v", msgs[1])
	}
}

func TestAgentState_AddMessage_emptyIDAlwaysAppends(t *testing.T) {
	s := NewState()
	s.AddMessage(Message{Role: RoleUser, Content: "x"})
	s.AddMessage(Message{Role: RoleUser, Content: "y"})
	if len(s.Messages()) != 2 {
		t.Fatal()
	}
}

func TestAgentState_Messages_DeepCopy(t *testing.T) {
	s := NewState()
	s.AddMessage(Message{
		Role:    RoleAssistant,
		Content: "hello",
		ToolCalls: []ToolCall{
			{ID: "tc1", Name: "foo", Arguments: `{"a":1}`},
		},
		Blocks: []ContentBlock{
			{Kind: BlockKindText, Text: "hi"},
		},
		Metadata: map[string]any{
			"key": "value",
		},
	})

	msgs := s.Messages()
	if len(msgs) != 1 {
		t.Fatalf("len=%d want 1", len(msgs))
	}

	// Mutate the returned slice's fields — must NOT affect original state.
	msgs[0].ToolCalls[0].Name = "mutated"
	msgs[0].Blocks[0].Text = "mutated"
	msgs[0].Metadata["key"] = "mutated"

	original := s.Messages()[0]
	if original.ToolCalls[0].Name == "mutated" {
		t.Fatal("Messages() shallow-copied ToolCalls — mutation leaked to original state")
	}
	if original.Blocks[0].Text == "mutated" {
		t.Fatal("Messages() shallow-copied Blocks — mutation leaked to original state")
	}
	if original.Metadata["key"] == "mutated" {
		t.Fatal("Messages() shallow-copied Metadata — mutation leaked to original state")
	}
}

func TestAgentState_Snapshot_DeepCopy(t *testing.T) {
	s := NewState()
	s.AddMessage(Message{
		Role:    RoleAssistant,
		Content: "hello",
		ToolCalls: []ToolCall{
			{ID: "tc1", Name: "foo", Arguments: `{}`},
		},
		Metadata: map[string]any{"k": "v"},
	})

	snap := s.Snapshot()

	// Mutate snapshot — must NOT affect original.
	snap.Messages[0].ToolCalls[0].Name = "mutated"
	snap.Messages[0].Metadata["k"] = "mutated"

	original := s.Messages()[0]
	if original.ToolCalls[0].Name == "mutated" {
		t.Fatal("Snapshot() shallow-copied ToolCalls — mutation leaked to original state")
	}
	if original.Metadata["k"] == "mutated" {
		t.Fatal("Snapshot() shallow-copied Metadata — mutation leaked to original state")
	}
}

func TestMessage_Clone(t *testing.T) {
	original := Message{
		ID:      "m1",
		Role:    RoleAssistant,
		Content: "hi",
		ToolCalls: []ToolCall{
			{ID: "tc1", Name: "tool", Arguments: `{}`},
		},
		Blocks: []ContentBlock{
			{Kind: BlockKindText, Text: "block"},
		},
		Metadata: map[string]any{"key": "val"},
	}

	clone := original.Clone()

	// Verify values match
	if clone.ID != original.ID || clone.Role != original.Role || clone.Content != original.Content {
		t.Fatal("Clone did not copy value fields")
	}
	if len(clone.ToolCalls) != 1 || clone.ToolCalls[0].ID != "tc1" {
		t.Fatal("Clone did not copy ToolCalls correctly")
	}
	if len(clone.Blocks) != 1 || clone.Blocks[0].Text != "block" {
		t.Fatal("Clone did not copy Blocks correctly")
	}
	if clone.Metadata["key"] != "val" {
		t.Fatal("Clone did not copy Metadata correctly")
	}

	// Mutate clone — must not affect original
	clone.ToolCalls[0].Name = "changed"
	clone.Blocks[0].Text = "changed"
	clone.Metadata["key"] = "changed"

	if original.ToolCalls[0].Name == "changed" {
		t.Fatal("Clone() shallow-copied ToolCalls")
	}
	if original.Blocks[0].Text == "changed" {
		t.Fatal("Clone() shallow-copied Blocks")
	}
	if original.Metadata["key"] == "changed" {
		t.Fatal("Clone() shallow-copied Metadata")
	}
}

func TestMessageClone_DeepCopyMetadata(t *testing.T) {
	original := Message{
		Role:    RoleSystem,
		Content: "test",
		Metadata: map[string]any{
			"slice":  []any{"a", "b"},
			"nested": map[string]any{"inner": []any{"x"}},
			"str":    "hello",
		},
	}

	clone := original.Clone()

	clone.Metadata["slice"].([]any)[0] = "mutated"
	clone.Metadata["nested"].(map[string]any)["inner"].([]any)[0] = "mutated"
	clone.Metadata["str"] = "changed"

	if original.Metadata["slice"].([]any)[0] == "mutated" {
		t.Fatal("Clone() shallow-copied Metadata slice value")
	}
	if original.Metadata["nested"].(map[string]any)["inner"].([]any)[0] == "mutated" {
		t.Fatal("Clone() shallow-copied nested Metadata map value")
	}
	if original.Metadata["str"] == "changed" {
		t.Fatal("Clone() shallow-copied Metadata string value")
	}
}

