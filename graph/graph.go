package graph

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/covoyage/covonaut/agentcore"
)

// Step is a unit of work in a graph node.
// Re-exported from the root package for convenience.
type Step = agentcore.Step

// Graph is a DAG-based execution engine. Nodes are named Steps connected
// by directed edges. The engine performs topological sorting at compile time
// and executes independent branches in parallel at runtime.
type Graph struct {
	nodes map[string]Step
	edges map[string][]string
}

func NewGraph() *Graph {
	return &Graph{
		nodes: make(map[string]Step),
		edges: make(map[string][]string),
	}
}

func (g *Graph) AddNode(name string, step Step) error {
	if _, exists := g.nodes[name]; exists {
		return fmt.Errorf("graph: duplicate node %q", name)
	}
	g.nodes[name] = step
	return nil
}

func (g *Graph) AddEdge(from, to string) error {
	if _, ok := g.nodes[from]; !ok {
		return fmt.Errorf("graph: unknown source node %q", from)
	}
	if _, ok := g.nodes[to]; !ok {
		return fmt.Errorf("graph: unknown target node %q", to)
	}
	g.edges[from] = append(g.edges[from], to)
	return nil
}

func (g *Graph) AddConditionalEdge(from string, route func(ctx context.Context, output string) string, targets []string) error {
	if _, ok := g.nodes[from]; !ok {
		return fmt.Errorf("graph: unknown source node %q", from)
	}
	for _, t := range targets {
		if _, ok := g.nodes[t]; !ok {
			return fmt.Errorf("graph: unknown target node %q", t)
		}
	}
	routerName := "__conditional_" + from
	g.nodes[routerName] = &conditionalBridge{
		route:   route,
		targets: targets,
		graph:   g,
	}
	g.edges[from] = append(g.edges[from], routerName)
	return nil
}

type conditionalBridge struct {
	route   func(ctx context.Context, output string) string
	targets []string
	graph   *Graph
}

func (cb *conditionalBridge) Run(ctx context.Context, input string) (string, error) {
	target := cb.route(ctx, input)
	for _, t := range cb.targets {
		if t == target {
			return t, nil
		}
	}
	return "", agentcore.NewNodeError("conditional edge: no matching target", nil, "conditional", target)
}

// CompileOptions configures graph compilation.
type CompileOptions struct {
	EntryNode string
	MaxSteps  int64
}

// CompiledGraph is a validated, ready-to-execute DAG.
type CompiledGraph struct {
	graph    *Graph
	Entry    string
	Sorted   [][]string
	MaxSteps int64
	InDegree map[string]int64
	RevEdges map[string][]string
}

func (g *Graph) Compile(opts CompileOptions) (*CompiledGraph, error) {
	if opts.EntryNode == "" {
		return nil, fmt.Errorf("graph: EntryNode is required")
	}
	if _, ok := g.nodes[opts.EntryNode]; !ok {
		return nil, fmt.Errorf("graph: entry node %q not found", opts.EntryNode)
	}

	maxSteps := opts.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 100
	}

	inDegree := make(map[string]int64)
	revEdges := make(map[string][]string)
	for name := range g.nodes {
		inDegree[name] = 0
	}
	for from, tos := range g.edges {
		for _, to := range tos {
			inDegree[to]++
			revEdges[to] = append(revEdges[to], from)
		}
		_ = from
	}

	sorted, err := topoSort(g.nodes, g.edges, inDegree)
	if err != nil {
		return nil, err
	}

	return &CompiledGraph{
		graph:    g,
		Entry:    opts.EntryNode,
		Sorted:   sorted,
		MaxSteps: maxSteps,
		InDegree: inDegree,
		RevEdges: revEdges,
	}, nil
}

func topoSort(nodes map[string]Step, edges map[string][]string, inDegreeOrig map[string]int64) ([][]string, error) {
	inDegree := make(map[string]int64)
	for k, v := range inDegreeOrig {
		inDegree[k] = v
	}

	var layers [][]string
	remaining := int64(len(nodes))

	for remaining > 0 {
		var layer []string
		for name := range inDegree {
			if inDegree[name] == 0 {
				layer = append(layer, name)
			}
		}
		if len(layer) == 0 {
			return nil, fmt.Errorf("graph: cycle detected — cannot topologically sort")
		}
		layers = append(layers, layer)
		for _, name := range layer {
			delete(inDegree, name)
			remaining--
			for _, to := range edges[name] {
				inDegree[to]--
			}
		}
	}
	return layers, nil
}

// Run executes the compiled graph.
func (cg *CompiledGraph) Run(ctx context.Context, input string) (string, error) {
	outputs := make(map[string]string)
	outputs[cg.Entry] = ""

	var steps int64
	for _, layer := range cg.Sorted {
		var layerNodes []string
		for _, name := range layer {
			if _, reachable := outputs[name]; reachable || name == cg.Entry {
				layerNodes = append(layerNodes, name)
			}
		}
		if len(layerNodes) == 0 {
			continue
		}

		results := make(map[string]string)
		errs := make(map[string]error)
		var mu sync.Mutex
		var wg sync.WaitGroup

		for _, name := range layerNodes {
			steps++
			if steps > cg.MaxSteps {
				return "", agentcore.WrapNodeError(agentcore.ErrExceedMaxSteps, "graph")
			}

			nodeInput := input
			if preds, ok := cg.RevEdges[name]; ok && len(preds) > 0 {
				var parts []string
				for _, p := range preds {
					if out, exists := outputs[p]; exists {
						parts = append(parts, out)
					}
				}
				if len(parts) == 1 {
					nodeInput = parts[0]
				} else if len(parts) > 1 {
					nodeInput = JoinOutputs(parts)
				}
			}

			wg.Add(1)
			go func(nodeName, nodeIn string) {
				defer wg.Done()
				step := cg.graph.nodes[nodeName]
				out, err := step.Run(ctx, nodeIn)
				mu.Lock()
				results[nodeName] = out
				errs[nodeName] = err
				mu.Unlock()
			}(name, nodeInput)
		}

		wg.Wait()

		for name, err := range errs {
			if err != nil {
				return "", agentcore.WrapNodeError(err, "graph:"+name)
			}
		}
		for name, out := range results {
			outputs[name] = out
			for _, to := range cg.graph.edges[name] {
				outputs[to] = ""
			}
		}
	}

	return FindTerminalOutput(cg.graph.nodes, cg.graph.edges, outputs), nil
}

var _ Step = (*CompiledGraph)(nil)

// FindTerminalOutput finds the output of terminal nodes (nodes with no outgoing edges).
func FindTerminalOutput(nodes map[string]Step, edges map[string][]string, outputs map[string]string) string {
	terminal := make(map[string]bool)
	for name := range nodes {
		if _, hasEdges := edges[name]; !hasEdges || len(edges[name]) == 0 {
			terminal[name] = true
		}
	}
	var names []string
	for name := range terminal {
		if out, ok := outputs[name]; ok && out != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	var parts []string
	for _, name := range names {
		parts = append(parts, outputs[name])
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return JoinOutputs(parts)
}

// JoinOutputs merges multiple outputs with a separator.
func JoinOutputs(parts []string) string {
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += "\n---\n"
		}
		result += p
	}
	return result
}
