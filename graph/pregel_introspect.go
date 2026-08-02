package graph

import (
	"fmt"
	"sort"
	"strings"
)

type PregelNodeInfo struct {
	Name               string   `json:"name"`
	Reachable          bool     `json:"reachable"`
	StaticTargets      []string `json:"static_targets,omitempty"`
	ConditionalTargets []string `json:"conditional_targets,omitempty"`
	DynamicTargets     bool     `json:"dynamic_targets,omitempty"`
}

type PregelGraphInfo struct {
	Entry       string            `json:"entry"`
	MaxSteps    int64             `json:"max_steps"`
	Nodes       []PregelNodeInfo  `json:"nodes"`
	Diagnostics []GraphDiagnostic `json:"diagnostics,omitempty"`
}

func (graph *CompiledPregelGraph) Info() PregelGraphInfo {
	reachable := map[string]bool{}
	queue := []string{graph.entry}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if name == PregelEnd || reachable[name] {
			continue
		}
		reachable[name] = true
		queue = append(queue, graph.pg.edges[name]...)
		queue = append(queue, graph.pg.conditionalTargets[name]...)
	}
	names := make([]string, 0, len(graph.pg.nodes))
	for name := range graph.pg.nodes {
		names = append(names, name)
	}
	sort.Strings(names)
	info := PregelGraphInfo{Entry: graph.entry, MaxSteps: graph.maxSteps, Nodes: make([]PregelNodeInfo, 0, len(names))}
	for _, name := range names {
		staticTargets := append([]string(nil), graph.pg.edges[name]...)
		conditionalTargets := append([]string(nil), graph.pg.conditionalTargets[name]...)
		sort.Strings(staticTargets)
		sort.Strings(conditionalTargets)
		node := PregelNodeInfo{
			Name:               name,
			Reachable:          reachable[name],
			StaticTargets:      staticTargets,
			ConditionalTargets: conditionalTargets,
		}
		_, hasRouter := graph.pg.conditionalEdges[name]
		node.DynamicTargets = hasRouter && len(conditionalTargets) == 0
		info.Nodes = append(info.Nodes, node)
		if !node.Reachable {
			info.Diagnostics = append(info.Diagnostics, GraphDiagnostic{Severity: DiagnosticWarning, Code: "unreachable_node", Node: name, Message: fmt.Sprintf("node %q is not reachable from entry %q", name, graph.entry)})
		}
		if node.DynamicTargets {
			info.Diagnostics = append(info.Diagnostics, GraphDiagnostic{Severity: DiagnosticWarning, Code: "dynamic_targets_unknown", Node: name, Message: fmt.Sprintf("conditional targets for node %q are not declared", name)})
		}
	}
	return info
}

func (graph *CompiledPregelGraph) Mermaid() string {
	info := graph.Info()
	var builder strings.Builder
	builder.WriteString("flowchart TD\n")
	for _, node := range info.Nodes {
		fmt.Fprintf(&builder, "    %s[%q]\n", mermaidNodeID(node.Name), node.Name)
	}
	builder.WriteString("    pregel_end([\"end\"])\n")
	for _, node := range info.Nodes {
		for _, target := range node.StaticTargets {
			if target == PregelEnd {
				target = "pregel_end"
			} else {
				target = mermaidNodeID(target)
			}
			fmt.Fprintf(&builder, "    %s --> %s\n", mermaidNodeID(node.Name), target)
		}
		for _, target := range node.ConditionalTargets {
			if target == PregelEnd {
				target = "pregel_end"
			} else {
				target = mermaidNodeID(target)
			}
			fmt.Fprintf(&builder, "    %s -.-> %s\n", mermaidNodeID(node.Name), target)
		}
	}
	return builder.String()
}
