package graph

import (
	"context"
	"strings"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
)

type introspectStep struct{}

func (introspectStep) Run(context.Context, string) (string, error) { return "ok", nil }

func TestCompiledGraphInfoAndMermaid(t *testing.T) {
	graph := NewGraph()
	for _, name := range []string{"start", "work", "orphan"} {
		if err := graph.AddNode(name, introspectStep{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := graph.AddEdge("start", "work"); err != nil {
		t.Fatal(err)
	}
	compiled, err := graph.Compile(CompileOptions{EntryNode: "start"})
	if err != nil {
		t.Fatal(err)
	}
	info := compiled.Info()
	if len(info.Nodes) != 3 || len(info.Diagnostics) != 1 || info.Diagnostics[0].Node != "orphan" {
		t.Fatalf("info = %#v", info)
	}
	mermaid := compiled.Mermaid()
	if !strings.Contains(mermaid, "n_start --> n_work") || strings.Contains(mermaid, "n_work --> n_orphan") {
		t.Fatalf("mermaid = %q", mermaid)
	}
}

type runInfoStep struct {
	info agentcore.RunInfo
}

func (step *runInfoStep) Run(ctx context.Context, input string) (string, error) {
	step.info, _ = agentcore.RunInfoFromContext(ctx)
	return input, nil
}

func TestCompiledGraphPropagatesNodeRunInfo(t *testing.T) {
	step := &runInfoStep{}
	graph := NewGraph()
	if err := graph.AddNode("start", step); err != nil {
		t.Fatal(err)
	}
	compiled, err := graph.Compile(CompileOptions{EntryNode: "start"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := compiled.Run(context.Background(), "input"); err != nil {
		t.Fatal(err)
	}
	if step.info.Component != "graph_node" || step.info.ParentRunID == "" {
		t.Fatalf("node run info = %#v", step.info)
	}
}

func TestGraphInfoIncludesTypedAndConditionalMetadata(t *testing.T) {
	graph := NewGraph()
	if err := AddTypedNode(graph, TypedNode[int, int]{
		Name:     "typed",
		Runnable: agentcore.NewInvokeRunnable(func(_ context.Context, value int) (int, error) { return value, nil }),
		Decode:   func(string) (int, error) { return 1, nil },
		Encode:   func(int) (string, error) { return "route", nil },
	}); err != nil {
		t.Fatal(err)
	}
	if err := graph.AddNode("left", introspectStep{}); err != nil {
		t.Fatal(err)
	}
	if err := graph.AddNode("right", introspectStep{}); err != nil {
		t.Fatal(err)
	}
	if err := graph.AddConditionalEdge("typed", func(context.Context, string) string { return "left" }, []string{"left", "right"}); err != nil {
		t.Fatal(err)
	}
	compiled, err := graph.Compile(CompileOptions{EntryNode: "typed"})
	if err != nil {
		t.Fatal(err)
	}
	info := compiled.Info()
	var typed GraphNodeInfo
	for _, node := range info.Nodes {
		if node.Name == "typed" {
			typed = node
		}
	}
	if typed.Kind != "typed" || typed.InputType != "int" || typed.OutputType != "int" || len(typed.ConditionalTargets) != 2 {
		t.Fatalf("typed info = %#v", typed)
	}
	if !strings.Contains(compiled.Mermaid(), "n_typed -.-> n_left") {
		t.Fatalf("mermaid = %q", compiled.Mermaid())
	}
}
