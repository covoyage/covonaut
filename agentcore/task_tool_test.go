package agentcore

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestTaskTool_RoutesToCorrectSubAgent(t *testing.T) {
	subA := &Tool{
		Name:        "alpha",
		Description: "Agent that prepends A:",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"input": map[string]any{"type": "string"},
			},
			"required": []any{"input"},
		},
		Func: func(_ context.Context, args json.RawMessage) (any, error) {
			var p struct{ Input string }
			if err := json.Unmarshal(args, &p); err != nil {
				return nil, err
			}
			return "A:" + p.Input, nil
		},
	}
	subB := &Tool{
		Name:        "beta",
		Description: "Agent that prepends B:",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"input": map[string]any{"type": "string"},
			},
			"required": []any{"input"},
		},
		Func: func(_ context.Context, args json.RawMessage) (any, error) {
			var p struct{ Input string }
			if err := json.Unmarshal(args, &p); err != nil {
				return nil, err
			}
			return "B:" + p.Input, nil
		},
	}

	tt := TaskTool("delegate", []TaskOption{
		{Name: "alpha", Description: "The alpha agent", Tool: subA},
		{Name: "beta", Description: "The beta agent", Tool: subB},
	})

	if tt.Name != "delegate" {
		t.Fatalf("Name = %q, want delegate", tt.Name)
	}

	tests := []struct {
		name    string
		agent   string
		task    string
		want    string
		wantErr bool
	}{
		{name: "alpha agent", agent: "alpha", task: "do thing", want: "A:do thing"},
		{name: "beta agent", agent: "beta", task: "do other", want: "B:do other"},
		{name: "unknown agent", agent: "gamma", task: "anything", wantErr: true},
		{name: "empty agent", agent: "", task: "anything", wantErr: true},
		{name: "empty task", agent: "alpha", task: "", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			args, err := json.Marshal(map[string]string{
				"agent": tc.agent,
				"task":  tc.task,
			})
			if err != nil {
				t.Fatal(err)
			}
			got, err := tt.Func(context.Background(), args)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if _, ok := err.(*NodeError); !ok {
					t.Fatalf("error type = %T, want *NodeError", err)
				}
				if !strings.Contains(err.Error(), "task_tool:delegate") {
					t.Fatalf("error missing path: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTaskTool_Definition_ContainsSortedEnum(t *testing.T) {
	// Pass options in reverse alphabetical order; output must be sorted.
	tt := TaskTool("delegate", []TaskOption{
		{Name: "zebra", Description: "The zebra", Tool: &Tool{Name: "zebra", Func: noopFunc}},
		{Name: "beta", Description: "The beta", Tool: &Tool{Name: "beta", Func: noopFunc}},
		{Name: "alpha", Description: "The alpha", Tool: &Tool{Name: "alpha", Func: noopFunc}},
	})

	def := tt.Definition()
	props, ok := def.Parameters["properties"].(map[string]any)
	if !ok {
		t.Fatal("parameters missing properties")
	}
	agentSchema, ok := props["agent"].(map[string]any)
	if !ok {
		t.Fatal("properties missing agent")
	}
	enum, ok := agentSchema["enum"].([]string)
	if !ok {
		t.Fatal("agent missing enum")
	}
	want := []string{"alpha", "beta", "zebra"}
	if len(enum) != len(want) {
		t.Fatalf("enum = %v, want %v", enum, want)
	}
	for i := range want {
		if enum[i] != want[i] {
			t.Fatalf("enum[%d] = %q, want %q", i, enum[i], want[i])
		}
	}

	// Description must list agents in same sorted order.
	desc, ok := agentSchema["description"].(string)
	if !ok {
		t.Fatal("agent missing description")
	}
	lines := strings.Split(desc, "\n")
	if len(lines) != 3 {
		t.Fatalf("description = %q, want 3 lines", desc)
	}
	if !strings.HasPrefix(lines[0], "alpha:") || !strings.HasPrefix(lines[1], "beta:") || !strings.HasPrefix(lines[2], "zebra:") {
		t.Fatalf("description lines out of order:\n%s", desc)
	}
}

func TestTaskToolFromConfigs(t *testing.T) {
	cfgA := Config{
		ModelConfig: ModelConfig{Name: "reviewer"},
	}
	cfgB := Config{
		ModelConfig: ModelConfig{Name: "coder"},
	}

	tt := TaskToolFromConfigs("dev", []Config{cfgA, cfgB})

	def := tt.Definition()
	props := def.Parameters["properties"].(map[string]any)
	agentSchema := props["agent"].(map[string]any)
	enum := agentSchema["enum"].([]string)
	if len(enum) != 2 || enum[0] != "coder" || enum[1] != "reviewer" {
		t.Fatalf("enum = %v, want [coder reviewer]", enum)
	}
}
