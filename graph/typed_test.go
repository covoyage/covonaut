package graph

import (
	"context"
	"strconv"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
)

func TestTypedNodeRunsInStringGraph(t *testing.T) {
	typedRunnable := agentcore.NewInvokeRunnable(func(_ context.Context, input int) (map[string]int, error) {
		return map[string]int{"doubled": input * 2}, nil
	})
	graph := NewGraph()
	err := AddTypedNode(graph, TypedNode[int, map[string]int]{
		Name:     "double",
		Runnable: typedRunnable,
		Decode: func(input string) (int, error) {
			return strconv.Atoi(input)
		},
		Encode: JSONEncode[map[string]int],
	})
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := graph.Compile(CompileOptions{EntryNode: "double"})
	if err != nil {
		t.Fatal(err)
	}
	output, err := compiled.Run(context.Background(), "21")
	if err != nil {
		t.Fatal(err)
	}
	if output != `{"doubled":42}` {
		t.Fatalf("output = %q", output)
	}
}
