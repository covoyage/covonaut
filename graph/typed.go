package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/covoyage/covonaut/agentcore"
)

// TypedNode keeps a Runnable's input and output types checked by the compiler.
// Decode and Encode explicitly define its boundary with the existing string
// graph transport, avoiding reflection and preserving Step compatibility.
type TypedNode[I, O any] struct {
	Name     string
	Runnable agentcore.Runnable[I, O]
	Decode   func(string) (I, error)
	Encode   func(O) (string, error)
}

// AddTypedNode adapts and registers a typed Runnable in a Graph.
func AddTypedNode[I, O any](graph *Graph, node TypedNode[I, O]) error {
	if graph == nil {
		return fmt.Errorf("graph: graph is nil")
	}
	if node.Name == "" {
		return fmt.Errorf("graph: typed node name is required")
	}
	if node.Runnable == nil {
		return fmt.Errorf("graph: typed node %q runnable is nil", node.Name)
	}
	if node.Decode == nil || node.Encode == nil {
		return fmt.Errorf("graph: typed node %q requires Decode and Encode", node.Name)
	}
	return graph.AddNode(node.Name, &typedNodeStep[I, O]{node: node})
}

type typedNodeStep[I, O any] struct {
	node TypedNode[I, O]
}

func (*typedNodeStep[I, O]) graphNodeMetadata() (kind, inputType, outputType string, nested *GraphInfo) {
	return "typed", reflect.TypeOf((*I)(nil)).Elem().String(), reflect.TypeOf((*O)(nil)).Elem().String(), nil
}

func (step *typedNodeStep[I, O]) Run(ctx context.Context, input string) (string, error) {
	typedInput, err := step.node.Decode(input)
	if err != nil {
		return "", fmt.Errorf("typed node %q decode: %w", step.node.Name, err)
	}
	output, err := step.node.Runnable.Invoke(ctx, typedInput)
	if err != nil {
		return "", fmt.Errorf("typed node %q invoke: %w", step.node.Name, err)
	}
	encoded, err := step.node.Encode(output)
	if err != nil {
		return "", fmt.Errorf("typed node %q encode: %w", step.node.Name, err)
	}
	return encoded, nil
}

// JSONDecode is a convenient explicit boundary for structured node input.
func JSONDecode[T any](input string) (T, error) {
	var value T
	err := json.Unmarshal([]byte(input), &value)
	return value, err
}

// JSONEncode is a convenient explicit boundary for structured node output.
func JSONEncode[T any](output T) (string, error) {
	data, err := json.Marshal(output)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
