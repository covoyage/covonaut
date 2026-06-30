package agentcore

import (
	"context"
	"encoding/json"
	"testing"
)

func TestRegistry_Definitions_DeterministicOrder(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&Tool{Name: "zebra", Description: "z", Func: noopFunc})
	reg.Register(&Tool{Name: "alpha", Description: "a", Func: noopFunc})
	reg.Register(&Tool{Name: "middle", Description: "m", Func: noopFunc})

	defs := reg.Definitions()
	if len(defs) != 3 {
		t.Fatalf("len=%d want 3", len(defs))
	}
	// Must be sorted alphabetically
	if defs[0].Name != "alpha" || defs[1].Name != "middle" || defs[2].Name != "zebra" {
		t.Fatalf("order not deterministic: %v", namesOf(defs))
	}
}

func TestRegistry_Names_DeterministicOrder(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&Tool{Name: "zebra", Func: noopFunc})
	reg.Register(&Tool{Name: "alpha", Func: noopFunc})

	names := reg.Names()
	if names[0] != "alpha" || names[1] != "zebra" {
		t.Fatalf("order not deterministic: %v", names)
	}
}

func TestRegistry_Definitions_StableAcrossCalls(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&Tool{Name: "c", Func: noopFunc})
	reg.Register(&Tool{Name: "a", Func: noopFunc})
	reg.Register(&Tool{Name: "b", Func: noopFunc})

	for i := 0; i < 5; i++ {
		defs := reg.Definitions()
		got := namesOf(defs)
		want := []string{"a", "b", "c"}
		if len(got) != len(want) {
			t.Fatalf("call %d: len=%d want %d", i, len(got), len(want))
		}
		for j := range got {
			if got[j] != want[j] {
				t.Fatalf("call %d: order mismatch at %d: got %q want %q", i, j, got[j], want[j])
			}
		}
	}
}

func noopFunc(_ context.Context, _ json.RawMessage) (any, error) { return "ok", nil }

func namesOf(defs []ToolDefinition) []string {
	names := make([]string, len(defs))
	for i, d := range defs {
		names[i] = d.Name
	}
	return names
}
