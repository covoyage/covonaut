package graph

import (
	"fmt"
	"sort"
	"strings"
)

type DiagnosticSeverity string

const (
	DiagnosticWarning DiagnosticSeverity = "warning"
)

type GraphDiagnostic struct {
	Severity DiagnosticSeverity `json:"severity"`
	Code     string             `json:"code"`
	Node     string             `json:"node,omitempty"`
	Message  string             `json:"message"`
}

type GraphNodeInfo struct {
	Name               string     `json:"name"`
	Kind               string     `json:"kind"`
	InputType          string     `json:"input_type,omitempty"`
	OutputType         string     `json:"output_type,omitempty"`
	Streaming          bool       `json:"streaming"`
	Reachable          bool       `json:"reachable"`
	Terminal           bool       `json:"terminal"`
	Predecessors       []string   `json:"predecessors,omitempty"`
	Successors         []string   `json:"successors,omitempty"`
	ConditionalTargets []string   `json:"conditional_targets,omitempty"`
	Nested             *GraphInfo `json:"nested,omitempty"`
}

type GraphInfo struct {
	Entry       string            `json:"entry"`
	Nodes       []GraphNodeInfo   `json:"nodes"`
	Diagnostics []GraphDiagnostic `json:"diagnostics,omitempty"`
}

// Info returns a deterministic, read-only description of the compiled graph.
func (cg *CompiledGraph) Info() GraphInfo {
	reachable := map[string]bool{}
	queue := []string{cg.Entry}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if reachable[name] {
			continue
		}
		reachable[name] = true
		queue = append(queue, cg.graph.edges[name]...)
	}

	names := make([]string, 0, len(cg.graph.nodes)+len(cg.StreamNodes))
	for name := range cg.graph.nodes {
		names = append(names, name)
	}
	for name := range cg.StreamNodes {
		names = append(names, name)
	}
	sort.Strings(names)
	info := GraphInfo{Entry: cg.Entry, Nodes: make([]GraphNodeInfo, 0, len(names))}
	for _, name := range names {
		predecessors := append([]string(nil), cg.RevEdges[name]...)
		successors := append([]string(nil), cg.graph.edges[name]...)
		sort.Strings(predecessors)
		sort.Strings(successors)
		node := GraphNodeInfo{
			Name:         name,
			Kind:         "step",
			Reachable:    reachable[name],
			Terminal:     len(successors) == 0,
			Predecessors: predecessors,
			Successors:   successors,
		}
		if _, ok := cg.StreamNodes[name]; ok {
			node.Streaming = true
			node.Kind = "stream"
		}
		if step, ok := cg.graph.nodes[name]; ok {
			if metadata, ok := step.(interface {
				graphNodeMetadata() (kind, inputType, outputType string, nested *GraphInfo)
			}); ok {
				node.Kind, node.InputType, node.OutputType, node.Nested = metadata.graphNodeMetadata()
			} else if nested, ok := step.(*CompiledGraph); ok {
				nestedInfo := nested.Info()
				node.Kind = "subgraph"
				node.Nested = &nestedInfo
			}
		}
		if conditional, ok := cg.graph.conditionalEdges[name]; ok {
			node.ConditionalTargets = append([]string(nil), conditional.targets...)
			sort.Strings(node.ConditionalTargets)
		}
		info.Nodes = append(info.Nodes, node)
		if !node.Reachable {
			info.Diagnostics = append(info.Diagnostics, GraphDiagnostic{
				Severity: DiagnosticWarning,
				Code:     "unreachable_node",
				Node:     name,
				Message:  fmt.Sprintf("node %q is not reachable from entry %q", name, cg.Entry),
			})
		}
	}
	return info
}

// Mermaid renders a deterministic flowchart for documentation and debugging.
func (cg *CompiledGraph) Mermaid() string {
	info := cg.Info()
	var builder strings.Builder
	builder.WriteString("flowchart TD\n")
	for _, node := range info.Nodes {
		label := node.Name
		if node.Kind != "step" {
			label += " (" + node.Kind + ")"
		}
		fmt.Fprintf(&builder, "    %s[%q]\n", mermaidNodeID(node.Name), label)
	}
	for _, node := range info.Nodes {
		conditional := make(map[string]bool, len(node.ConditionalTargets))
		for _, target := range node.ConditionalTargets {
			conditional[target] = true
		}
		for _, successor := range node.Successors {
			arrow := " --> "
			if conditional[successor] {
				arrow = " -.-> "
			}
			fmt.Fprintf(&builder, "    %s%s%s\n", mermaidNodeID(node.Name), arrow, mermaidNodeID(successor))
		}
	}
	return builder.String()
}

func mermaidNodeID(name string) string {
	var builder strings.Builder
	builder.WriteString("n_")
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' {
			builder.WriteRune(r)
		} else {
			fmt.Fprintf(&builder, "_%x_", r)
		}
	}
	return builder.String()
}
