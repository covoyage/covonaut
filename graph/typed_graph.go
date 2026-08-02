package graph

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/covoyage/covonaut/agentcore"
)

// TypedMerge combines predecessor or terminal values in a TypedGraph.
type TypedMerge[T any] func(ctx context.Context, values []T) (T, error)

// TypedGraph is an end-to-end typed DAG. Every node accepts and returns T, so
// values never cross a string, JSON, or reflection boundary during execution.
type TypedGraph[T any] struct {
	nodes map[string]agentcore.Runnable[T, T]
	edges map[string][]string
}

func NewTypedGraph[T any]() *TypedGraph[T] {
	return &TypedGraph[T]{
		nodes: make(map[string]agentcore.Runnable[T, T]),
		edges: make(map[string][]string),
	}
}

func (graph *TypedGraph[T]) AddNode(name string, runnable agentcore.Runnable[T, T]) error {
	if name == "" {
		return fmt.Errorf("typed graph: node name is required")
	}
	if runnable == nil {
		return fmt.Errorf("typed graph: node %q runnable is nil", name)
	}
	if _, exists := graph.nodes[name]; exists {
		return fmt.Errorf("typed graph: duplicate node %q", name)
	}
	graph.nodes[name] = runnable
	return nil
}

func (graph *TypedGraph[T]) AddEdge(from, to string) error {
	if _, ok := graph.nodes[from]; !ok {
		return fmt.Errorf("typed graph: unknown source node %q", from)
	}
	if _, ok := graph.nodes[to]; !ok {
		return fmt.Errorf("typed graph: unknown target node %q", to)
	}
	graph.edges[from] = append(graph.edges[from], to)
	return nil
}

type TypedCompileOptions[T any] struct {
	EntryNode string
	MaxSteps  int64
	Merge     TypedMerge[T]
	Tracer    agentcore.Tracer
}

type CompiledTypedGraph[T any] struct {
	nodes    map[string]agentcore.Runnable[T, T]
	edges    map[string][]string
	revEdges map[string][]string
	sorted   [][]string
	entry    string
	maxSteps int64
	merge    TypedMerge[T]
	tracer   agentcore.Tracer
}

func (graph *TypedGraph[T]) Compile(options TypedCompileOptions[T]) (*CompiledTypedGraph[T], error) {
	if options.EntryNode == "" {
		return nil, fmt.Errorf("typed graph: entry node is required")
	}
	if _, ok := graph.nodes[options.EntryNode]; !ok {
		return nil, fmt.Errorf("typed graph: entry node %q not found", options.EntryNode)
	}
	names := make(map[string]struct{}, len(graph.nodes))
	inDegree := make(map[string]int64, len(graph.nodes))
	revEdges := make(map[string][]string, len(graph.nodes))
	for name := range graph.nodes {
		names[name] = struct{}{}
		inDegree[name] = 0
	}
	for from, targets := range graph.edges {
		for _, target := range targets {
			inDegree[target]++
			revEdges[target] = append(revEdges[target], from)
		}
	}
	sorted, err := topoSort(names, graph.edges, inDegree)
	if err != nil {
		return nil, fmt.Errorf("typed %w", err)
	}
	maxSteps := options.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 100
	}
	return &CompiledTypedGraph[T]{
		nodes:    graph.nodes,
		edges:    graph.edges,
		revEdges: revEdges,
		sorted:   sorted,
		entry:    options.EntryNode,
		maxSteps: maxSteps,
		merge:    options.Merge,
		tracer:   options.Tracer,
	}, nil
}

func (graph *CompiledTypedGraph[T]) Run(ctx context.Context, input T) (T, error) {
	ctx, span, _ := agentcore.StartComponentRun(ctx, graph.tracer, "typed_graph", graph.entry)
	defer span.End()
	outputs := make(map[string]T)
	reachable := map[string]bool{graph.entry: true}
	var steps int64

	for _, layer := range graph.sorted {
		names := make([]string, 0, len(layer))
		for _, name := range layer {
			if reachable[name] {
				names = append(names, name)
			}
		}
		if len(names) == 0 {
			continue
		}
		results := make(map[string]T, len(names))
		errorsByNode := make(map[string]error, len(names))
		var mutex sync.Mutex
		var wait sync.WaitGroup
		for _, name := range names {
			steps++
			if steps > graph.maxSteps {
				var zero T
				return zero, agentcore.WrapNodeError(agentcore.ErrExceedMaxSteps, "typed_graph")
			}
			nodeInput, err := graph.nodeInput(ctx, name, input, outputs)
			if err != nil {
				var zero T
				return zero, err
			}
			wait.Add(1)
			go func(nodeName string, value T) {
				defer wait.Done()
				nodeCtx, nodeSpan, _ := agentcore.StartComponentRun(ctx, graph.tracer, "typed_graph_node", nodeName)
				defer nodeSpan.End()
				output, runErr := graph.nodes[nodeName].Invoke(nodeCtx, value)
				if runErr != nil {
					nodeSpan.RecordError(runErr)
				}
				mutex.Lock()
				results[nodeName] = output
				errorsByNode[nodeName] = runErr
				mutex.Unlock()
			}(name, nodeInput)
		}
		wait.Wait()
		for _, name := range names {
			if err := errorsByNode[name]; err != nil {
				var zero T
				return zero, agentcore.WrapNodeError(err, "typed_graph:"+name)
			}
			outputs[name] = results[name]
			for _, target := range graph.edges[name] {
				reachable[target] = true
			}
		}
	}

	terminalNames := make([]string, 0)
	for name := range graph.nodes {
		if len(graph.edges[name]) == 0 && reachable[name] {
			terminalNames = append(terminalNames, name)
		}
	}
	sort.Strings(terminalNames)
	values := make([]T, 0, len(terminalNames))
	for _, name := range terminalNames {
		values = append(values, outputs[name])
	}
	return graph.mergeValues(ctx, values, "terminal outputs")
}

func (graph *CompiledTypedGraph[T]) nodeInput(ctx context.Context, name string, root T, outputs map[string]T) (T, error) {
	predecessors := graph.revEdges[name]
	if len(predecessors) == 0 {
		return root, nil
	}
	values := make([]T, 0, len(predecessors))
	for _, predecessor := range predecessors {
		if value, ok := outputs[predecessor]; ok {
			values = append(values, value)
		}
	}
	return graph.mergeValues(ctx, values, "node "+name)
}

func (graph *CompiledTypedGraph[T]) mergeValues(ctx context.Context, values []T, location string) (T, error) {
	if len(values) == 1 {
		return values[0], nil
	}
	if len(values) == 0 {
		var zero T
		return zero, fmt.Errorf("typed graph: no values available for %s", location)
	}
	if graph.merge == nil {
		var zero T
		return zero, fmt.Errorf("typed graph: %s requires a Merge function", location)
	}
	return graph.merge(ctx, values)
}

func (graph *CompiledTypedGraph[T]) Info() GraphInfo {
	reachable := map[string]bool{}
	queue := []string{graph.entry}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if reachable[name] {
			continue
		}
		reachable[name] = true
		queue = append(queue, graph.edges[name]...)
	}
	typeName := reflect.TypeOf((*T)(nil)).Elem().String()
	names := make([]string, 0, len(graph.nodes))
	for name := range graph.nodes {
		names = append(names, name)
	}
	sort.Strings(names)
	info := GraphInfo{Entry: graph.entry, Nodes: make([]GraphNodeInfo, 0, len(names))}
	for _, name := range names {
		predecessors := append([]string(nil), graph.revEdges[name]...)
		successors := append([]string(nil), graph.edges[name]...)
		sort.Strings(predecessors)
		sort.Strings(successors)
		node := GraphNodeInfo{
			Name:         name,
			Kind:         "typed",
			InputType:    typeName,
			OutputType:   typeName,
			Reachable:    reachable[name],
			Terminal:     len(successors) == 0,
			Predecessors: predecessors,
			Successors:   successors,
		}
		info.Nodes = append(info.Nodes, node)
		if !node.Reachable {
			info.Diagnostics = append(info.Diagnostics, GraphDiagnostic{Severity: DiagnosticWarning, Code: "unreachable_node", Node: name, Message: fmt.Sprintf("node %q is not reachable from entry %q", name, graph.entry)})
		}
	}
	return info
}

func (graph *CompiledTypedGraph[T]) Mermaid() string {
	info := graph.Info()
	var builder strings.Builder
	builder.WriteString("flowchart TD\n")
	for _, node := range info.Nodes {
		fmt.Fprintf(&builder, "    %s[%q]\n", mermaidNodeID(node.Name), node.Name+" (typed "+node.InputType+")")
	}
	for _, node := range info.Nodes {
		for _, target := range node.Successors {
			fmt.Fprintf(&builder, "    %s --> %s\n", mermaidNodeID(node.Name), mermaidNodeID(target))
		}
	}
	return builder.String()
}
