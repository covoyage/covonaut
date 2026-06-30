package graph

import (
	"context"
	"fmt"
	"sync"

	"github.com/covoyage/covonaut/agentcore"
)

// RunMode controls how the unified graph runner executes.
type RunMode string

const (
	RunModeDAG    RunMode = "dag"
	RunModePregl  RunMode = "pregel"
)

// NodeTriggerMode controls when a node fires.
type NodeTriggerMode string

const (
	// TriggerAllPredecessors waits for ALL predecessors (DAG default).
	TriggerAllPredecessors NodeTriggerMode = "all"
	// TriggerAnyPredecessor fires when ANY predecessor completes (Pregel default).
	TriggerAnyPredecessor NodeTriggerMode = "any"
)

// RunnerConfig configures the unified graph runner.
type RunnerConfig struct {
	Mode     RunMode         // dag or pregel
	Trigger  NodeTriggerMode // all or any
	MaxSteps int64
	Store    CheckpointStore // optional: enable checkpointing
}

// Runner is the unified execution engine for both DAG and Pregel graphs.
// It replaces the separate CompiledGraph.Run() and CompiledPregelGraph.Run()
// with a single execution model.
type Runner struct {
	nodes    map[string]Step
	edges    map[string][]string
	revEdges map[string][]string
	entry    string
	config   RunnerConfig
	sorted   [][]string // only for DAG mode
}

// NewDAGRunner creates a runner from a CompiledGraph.
func NewDAGRunner(cg *CompiledGraph, opts ...RunnerConfig) *Runner {
	cfg := RunnerConfig{Mode: RunModeDAG, Trigger: TriggerAllPredecessors, MaxSteps: cg.MaxSteps}
	if len(opts) > 0 {
		cfg = opts[0]
		if cfg.Mode == "" {
			cfg.Mode = RunModeDAG
		}
		if cfg.Trigger == "" {
			cfg.Trigger = TriggerAllPredecessors
		}
		if cfg.MaxSteps <= 0 {
			cfg.MaxSteps = cg.MaxSteps
		}
	}

	revEdges := make(map[string][]string)
	for k, v := range cg.RevEdges {
		revEdges[k] = v
	}

	return &Runner{
		nodes:    cg.graph.nodes,
		edges:    cg.graph.edges,
		revEdges: revEdges,
		entry:    cg.Entry,
		config:   cfg,
		sorted:   cg.Sorted,
	}
}

// NewPregelRunner creates a runner from a CompiledPregelGraph by wrapping
// PregelNodes as Steps operating on shared state.
func NewPregelRunner(cpg *CompiledPregelGraph, state *PregelState, opts ...RunnerConfig) *Runner {
	cfg := RunnerConfig{Mode: RunModePregl, Trigger: TriggerAnyPredecessor, MaxSteps: cpg.maxSteps}
	if len(opts) > 0 {
		cfg = opts[0]
		if cfg.Mode == "" {
			cfg.Mode = RunModePregl
		}
		if cfg.Trigger == "" {
			cfg.Trigger = TriggerAnyPredecessor
		}
		if cfg.MaxSteps <= 0 {
			cfg.MaxSteps = cpg.maxSteps
		}
	}

	nodes := make(map[string]Step)
	for name, fn := range cpg.pg.nodes {
		nodeFn := fn
		sharedState := state
		nodes[name] = &pregelStepAdapter{fn: nodeFn, state: sharedState}
	}

	revEdges := make(map[string][]string)
	for from, tos := range cpg.pg.edges {
		for _, to := range tos {
			if to != PregelEnd {
				revEdges[to] = append(revEdges[to], from)
			}
		}
	}

	return &Runner{
		nodes:    nodes,
		edges:    cpg.pg.edges,
		revEdges: revEdges,
		entry:    cpg.entry,
		config:   cfg,
	}
}

type pregelStepAdapter struct {
	fn    PregelNode
	state *PregelState
}

func (a *pregelStepAdapter) Run(ctx context.Context, _ string) (string, error) {
	out, err := a.fn(ctx, *a.state)
	if err != nil {
		return "", err
	}
	for k, v := range out {
		(*a.state)[k] = v
	}
	return "ok", nil
}

// Run executes the graph using the configured mode.
func (r *Runner) Run(ctx context.Context, input string) (string, error) {
	switch r.config.Mode {
	case RunModeDAG:
		return r.runDAG(ctx, input)
	case RunModePregl:
		return r.runPregelStyle(ctx, input)
	default:
		return r.runDAG(ctx, input)
	}
}

func (r *Runner) runDAG(ctx context.Context, input string) (string, error) {
	outputs := make(map[string]string)
	outputs[r.entry] = ""

	var steps int64
	for _, layer := range r.sorted {
		var layerNodes []string
		for _, name := range layer {
			if _, reachable := outputs[name]; reachable || name == r.entry {
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
			if steps > r.config.MaxSteps {
				return "", agentcore.WrapNodeError(agentcore.ErrExceedMaxSteps, "runner:dag")
			}

			nodeInput := r.resolveInput(name, input, outputs)

			wg.Add(1)
			go func(nodeName, nodeIn string) {
				defer wg.Done()
				out, err := r.nodes[nodeName].Run(ctx, nodeIn)
				mu.Lock()
				results[nodeName] = out
				errs[nodeName] = err
				mu.Unlock()
			}(name, nodeInput)
		}

		wg.Wait()

		for name, err := range errs {
			if err != nil {
				return "", agentcore.WrapNodeError(err, "runner:dag:"+name)
			}
		}
		for name, out := range results {
			outputs[name] = out
			for _, to := range r.edges[name] {
				outputs[to] = ""
			}
		}
	}

	return FindTerminalOutput(r.nodes, r.edges, outputs), nil
}

func (r *Runner) runPregelStyle(ctx context.Context, input string) (string, error) {
	active := []string{r.entry}
	var steps int64

	for len(active) > 0 {
		steps++
		if steps > r.config.MaxSteps {
			return "", agentcore.WrapNodeError(agentcore.ErrExceedMaxSteps, "runner:pregel")
		}

		if r.config.Store != nil {
			_ = r.saveStepCheckpoint(ctx, active, steps)
		}

		var nextActive []string
		nextSet := make(map[string]bool)

		errs := make(map[string]error)
		var mu sync.Mutex
		var wg sync.WaitGroup

		for _, name := range active {
			node, ok := r.nodes[name]
			if !ok {
				return "", agentcore.NewNodeError("node not found", nil, "runner:pregel", name)
			}

			wg.Add(1)
			go func(nodeName string, step Step) {
				defer wg.Done()
				_, err := step.Run(ctx, input)
				mu.Lock()
				errs[nodeName] = err
				mu.Unlock()
			}(name, node)
		}

		wg.Wait()

		for name, err := range errs {
			if err != nil {
				return "", agentcore.WrapNodeError(err, "runner:pregel:"+name)
			}
		}

		for _, name := range active {
			if targets, ok := r.edges[name]; ok {
				for _, t := range targets {
					if t == PregelEnd {
						return "done", nil
					}
					if !nextSet[t] {
						nextSet[t] = true
						nextActive = append(nextActive, t)
					}
				}
			}
		}

		active = nextActive
	}

	return "done", nil
}

func (r *Runner) resolveInput(name, graphInput string, outputs map[string]string) string {
	preds := r.revEdges[name]
	if len(preds) == 0 {
		return graphInput
	}
	var parts []string
	for _, p := range preds {
		if out, ok := outputs[p]; ok {
			parts = append(parts, out)
		}
	}
	if len(parts) == 1 {
		return parts[0]
	}
	if len(parts) > 1 {
		return JoinOutputs(parts)
	}
	return graphInput
}

func (r *Runner) saveStepCheckpoint(ctx context.Context, active []string, step int64) error {
	cp := Checkpoint{
		ID:        fmt.Sprintf("runner_step_%d", step),
		NodeName:  active[0],
		StepIndex: step,
		Metadata:  map[string]any{"active_nodes": active, "mode": string(r.config.Mode)},
	}
	return r.config.Store.Save(ctx, cp)
}
