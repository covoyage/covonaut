package graph

import (
	"context"
	"strings"
	"testing"
)

func TestPregelInfoAndMermaid(t *testing.T) {
	graph := NewPregelGraph()
	for _, name := range []string{"start", "next", "orphan"} {
		if err := graph.AddNode(name, func(_ context.Context, state PregelState) (PregelState, error) { return state, nil }); err != nil {
			t.Fatal(err)
		}
	}
	if err := graph.SetConditionalEdgeWithTargets("start", []string{"next", PregelEnd}, func(context.Context, PregelState) []string { return []string{"next"} }); err != nil {
		t.Fatal(err)
	}
	compiled, err := graph.Compile("start")
	if err != nil {
		t.Fatal(err)
	}
	info := compiled.Info()
	if len(info.Diagnostics) != 1 || info.Diagnostics[0].Code != "unreachable_node" || info.Diagnostics[0].Node != "orphan" {
		t.Fatalf("info = %#v", info)
	}
	mermaid := compiled.Mermaid()
	if !strings.Contains(mermaid, "n_start -.-> n_next") || !strings.Contains(mermaid, "n_start -.-> pregel_end") {
		t.Fatalf("mermaid = %q", mermaid)
	}
}

func TestPregelRejectsUndeclaredConditionalTarget(t *testing.T) {
	graph := NewPregelGraph()
	for _, name := range []string{"start", "declared", "other"} {
		if err := graph.AddNode(name, func(_ context.Context, state PregelState) (PregelState, error) { return state, nil }); err != nil {
			t.Fatal(err)
		}
	}
	if err := graph.SetConditionalEdgeWithTargets("start", []string{"declared"}, func(context.Context, PregelState) []string { return []string{"other"} }); err != nil {
		t.Fatal(err)
	}
	compiled, err := graph.Compile("start")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := compiled.Run(context.Background(), PregelState{}); err == nil {
		t.Fatal("expected undeclared target error")
	}
}
