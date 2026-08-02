package graph

import (
	"context"
	"strings"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
)

func TestTypedGraphRunsFanOutFanInWithoutEncoding(t *testing.T) {
	graph := NewTypedGraph[int]()
	if err := graph.AddNode("start", agentcore.NewInvokeRunnable(func(_ context.Context, value int) (int, error) {
		return value + 1, nil
	})); err != nil {
		t.Fatal(err)
	}
	if err := graph.AddNode("double", agentcore.NewInvokeRunnable(func(_ context.Context, value int) (int, error) {
		return value * 2, nil
	})); err != nil {
		t.Fatal(err)
	}
	if err := graph.AddNode("triple", agentcore.NewInvokeRunnable(func(_ context.Context, value int) (int, error) {
		return value * 3, nil
	})); err != nil {
		t.Fatal(err)
	}
	if err := graph.AddNode("finish", agentcore.NewInvokeRunnable(func(_ context.Context, value int) (int, error) {
		return value + 10, nil
	})); err != nil {
		t.Fatal(err)
	}
	for _, edge := range [][2]string{{"start", "double"}, {"start", "triple"}, {"double", "finish"}, {"triple", "finish"}} {
		if err := graph.AddEdge(edge[0], edge[1]); err != nil {
			t.Fatal(err)
		}
	}
	compiled, err := graph.Compile(TypedCompileOptions[int]{
		EntryNode: "start",
		Merge: func(_ context.Context, values []int) (int, error) {
			total := 0
			for _, value := range values {
				total += value
			}
			return total, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	output, err := compiled.Run(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if output != 20 {
		t.Fatalf("output = %d, want 20", output)
	}
	info := compiled.Info()
	if len(info.Nodes) != 4 || info.Nodes[0].Kind != "typed" || info.Nodes[0].InputType != "int" {
		t.Fatalf("info = %#v", info)
	}
	if !strings.Contains(compiled.Mermaid(), "typed int") {
		t.Fatalf("mermaid = %q", compiled.Mermaid())
	}
}
