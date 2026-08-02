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
	RunModeDAG   RunMode = "dag"
	RunModePregl RunMode = "pregel"
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
	StateFn  GenStateFn      // optional: per-run GraphState generator
	Tracer   agentcore.Tracer
}

// Runner is the unified execution engine for both DAG and Pregel graphs.
// It replaces the separate CompiledGraph.Run() and CompiledPregelGraph.Run()
// with a single execution model.
type Runner struct {
	nodes                    map[string]Step
	streamNodes              map[string]agentcore.StreamStep
	edges                    map[string][]string
	conditionals             map[string]conditionalEdge
	pregelConditionals       map[string]PregelEdgeRouter
	pregelConditionalTargets map[string][]string
	pregelNodes              map[string]PregelNode
	pregelState              *PregelState
	revEdges                 map[string][]string
	entry                    string
	config                   RunnerConfig
	sorted                   [][]string // only for DAG mode
	stateFn                  GenStateFn
}

// NewDAGRunner creates a runner from a CompiledGraph.
func NewDAGRunner(cg *CompiledGraph, opts ...RunnerConfig) *Runner {
	cfg := RunnerConfig{Mode: RunModeDAG, Trigger: TriggerAllPredecessors, MaxSteps: cg.MaxSteps, Tracer: cg.Tracer}
	stateFn := cg.StateFn
	if len(opts) > 0 {
		cfg = opts[0]
		cfg.Mode = RunModeDAG
		if cfg.Trigger == "" {
			cfg.Trigger = TriggerAllPredecessors
		}
		if cfg.MaxSteps <= 0 {
			cfg.MaxSteps = cg.MaxSteps
		}
		if cfg.StateFn != nil {
			stateFn = cfg.StateFn
		}
		if cfg.Tracer == nil {
			cfg.Tracer = cg.Tracer
		}
	}

	revEdges := make(map[string][]string)
	for k, v := range cg.RevEdges {
		revEdges[k] = v
	}

	streamNodes := make(map[string]agentcore.StreamStep, len(cg.StreamNodes))
	for k, v := range cg.StreamNodes {
		streamNodes[k] = v
	}

	return &Runner{
		nodes:        cg.graph.nodes,
		streamNodes:  streamNodes,
		edges:        cg.graph.edges,
		conditionals: cg.graph.conditionalEdges,
		revEdges:     revEdges,
		entry:        cg.Entry,
		config:       cfg,
		sorted:       cg.Sorted,
		stateFn:      stateFn,
	}
}

// NewPregelRunner creates a runner from a CompiledPregelGraph. Each superstep
// gives active nodes independent clones of one shared snapshot, then merges
// their outputs deterministically after all nodes finish.
func NewPregelRunner(cpg *CompiledPregelGraph, state *PregelState, opts ...RunnerConfig) *Runner {
	cfg := RunnerConfig{Mode: RunModePregl, Trigger: TriggerAnyPredecessor, MaxSteps: cpg.maxSteps}
	if len(opts) > 0 {
		cfg = opts[0]
		cfg.Mode = RunModePregl
		if cfg.Trigger == "" {
			cfg.Trigger = TriggerAnyPredecessor
		}
		if cfg.MaxSteps <= 0 {
			cfg.MaxSteps = cpg.maxSteps
		}
	}
	if state == nil {
		internalState := PregelState{}
		state = &internalState
	} else if *state == nil {
		*state = PregelState{}
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
		edges:                    cpg.pg.edges,
		revEdges:                 revEdges,
		entry:                    cpg.entry,
		config:                   cfg,
		pregelConditionals:       cpg.pg.conditionalEdges,
		pregelConditionalTargets: cpg.pg.conditionalTargets,
		pregelNodes:              cpg.pg.nodes,
		pregelState:              state,
	}
}

// Run executes the graph using the configured mode.
func (r *Runner) Run(ctx context.Context, input string) (string, error) {
	ctx, span, _ := agentcore.StartComponentRun(ctx, r.config.Tracer, "graph", r.entry)
	defer span.End()
	switch r.config.Mode {
	case RunModeDAG:
		return r.runDAG(ctx, input)
	case RunModePregl:
		return r.runPregelStyle(ctx, input)
	default:
		return r.runDAG(ctx, input)
	}
}

// RunStream executes the graph in streaming mode. Each node receives a
// *StreamReader[string] as input and produces one as output. Nodes that only
// implement Step are automatically adapted (input stream collected → Run →
// result wrapped as single-element stream). The final output is returned as a
// stream so the caller can consume chunks progressively.
func (r *Runner) RunStream(ctx context.Context, input string) (*agentcore.StreamReader[string], error) {
	if r.config.Mode != RunModeDAG {
		return nil, fmt.Errorf("streaming mode only supports RunModeDAG")
	}
	if len(r.conditionals) > 0 {
		return nil, fmt.Errorf("streaming mode does not support output-based conditional edges")
	}
	ctx, span, _ := agentcore.StartComponentRun(ctx, r.config.Tracer, "graph", r.entry)
	defer span.End()
	if r.stateFn != nil {
		state := r.stateFn(ctx)
		if state != nil {
			ctx = WithGraphState(ctx, state)
		}
	}

	inputStream := agentcore.NewStreamFromValue(input)
	streams := map[string]*agentcore.StreamReader[string]{}
	streams[r.entry] = inputStream

	var steps int64
	for _, layer := range r.sorted {
		var layerNodes []string
		for _, name := range layer {
			if _, reachable := streams[name]; reachable || name == r.entry {
				layerNodes = append(layerNodes, name)
			}
		}
		if len(layerNodes) == 0 {
			continue
		}

		type streamResult struct {
			name   string
			stream *agentcore.StreamReader[string]
		}
		resultCh := make(chan streamResult, len(layerNodes))
		errCh := make(chan error, len(layerNodes))
		var wg sync.WaitGroup

		for _, name := range layerNodes {
			steps++
			if steps > r.config.MaxSteps {
				return nil, agentcore.WrapNodeError(agentcore.ErrExceedMaxSteps, "runner:dag:stream")
			}

			inStream := r.resolveInputStream(name, inputStream, streams)
			streamStep := r.nodeAsStreamStep(name)

			wg.Add(1)
			go func(nodeName string, ss agentcore.StreamStep, in *agentcore.StreamReader[string]) {
				defer wg.Done()
				nodeCtx, span, _ := agentcore.StartComponentRun(ctx, r.config.Tracer, "graph_node", nodeName)
				defer span.End()
				out, err := ss.RunStream(nodeCtx, in)
				if err != nil {
					span.RecordError(err)
					errCh <- agentcore.WrapNodeError(err, "runner:dag:stream:"+nodeName)
					return
				}
				resultCh <- streamResult{name: nodeName, stream: out}
			}(name, streamStep, inStream)
		}

		wg.Wait()
		close(resultCh)
		close(errCh)

		if err, ok := <-errCh; ok {
			return nil, err
		}

		for res := range resultCh {
			streams[res.name] = res.stream
			for _, to := range r.edges[res.name] {
				if _, exists := streams[to]; !exists {
					streams[to] = agentcore.NewStreamReader[string](1)
				}
			}
		}
	}

	terminalStream := r.mergeTerminalStreams(streams)
	return terminalStream, nil
}

// nodeAsStreamStep returns the node as a StreamStep. StreamStep nodes are
// used directly; Step nodes are auto-wrapped via AsStreamStep.
func (r *Runner) nodeAsStreamStep(name string) agentcore.StreamStep {
	if ss, ok := r.streamNodes[name]; ok {
		return ss
	}
	return agentcore.AsStreamStep(r.nodes[name])
}

func (r *Runner) resolveInputStream(name string, graphInput *agentcore.StreamReader[string], streams map[string]*agentcore.StreamReader[string]) *agentcore.StreamReader[string] {
	preds := r.revEdges[name]
	if len(preds) == 0 {
		return graphInput
	}
	var predStreams []*agentcore.StreamReader[string]
	for _, p := range preds {
		if s, ok := streams[p]; ok {
			predStreams = append(predStreams, s)
		}
	}
	if len(predStreams) == 1 {
		return predStreams[0]
	}
	return agentcore.Merge(predStreams...)
}

func (r *Runner) mergeTerminalStreams(streams map[string]*agentcore.StreamReader[string]) *agentcore.StreamReader[string] {
	terminal := make(map[string]bool)
	for name := range r.nodes {
		if edges, hasEdges := r.edges[name]; !hasEdges || len(edges) == 0 {
			terminal[name] = true
		}
	}
	var terminalStreams []*agentcore.StreamReader[string]
	for name := range terminal {
		if s, ok := streams[name]; ok {
			terminalStreams = append(terminalStreams, s)
		}
	}
	if len(terminalStreams) == 1 {
		return terminalStreams[0]
	}
	return agentcore.Merge(terminalStreams...)
}

func (r *Runner) runDAG(ctx context.Context, input string) (string, error) {
	if r.stateFn != nil {
		state := r.stateFn(ctx)
		if state != nil {
			ctx = WithGraphState(ctx, state)
		}
	}
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
				nodeCtx, span, _ := agentcore.StartComponentRun(ctx, r.config.Tracer, "graph_node", nodeName)
				defer span.End()
				out, err := r.runnerGetNode(nodeName).Run(nodeCtx, nodeIn)
				if err != nil {
					span.RecordError(err)
				}
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
			if conditional, ok := r.conditionals[name]; ok {
				target := conditional.route(ctx, out)
				matched := false
				for _, candidate := range conditional.targets {
					if candidate == target {
						outputs[target] = ""
						matched = true
						break
					}
				}
				if !matched {
					return "", agentcore.NewNodeError("conditional edge: no matching target", nil, "runner:dag", name, target)
				}
				continue
			}
			for _, target := range r.edges[name] {
				outputs[target] = ""
			}
		}
	}

	return FindTerminalOutput(allNodes(r.nodes, r.streamNodes), r.edges, outputs), nil
}

// runnerGetNode resolves a node name to a Step. StreamStep nodes are auto-adapted
// via StreamStepToStep for the non-streaming execution path.
func (r *Runner) runnerGetNode(name string) Step {
	if step, ok := r.nodes[name]; ok {
		return step
	}
	if ss, ok := r.streamNodes[name]; ok {
		return agentcore.StreamStepToStep(ss)
	}
	return nil
}

func (r *Runner) runPregelStyle(ctx context.Context, input string) (string, error) {
	if r.stateFn != nil {
		state := r.stateFn(ctx)
		if state != nil {
			ctx = WithGraphState(ctx, state)
		}
	}
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

		baseState := r.pregelState.Clone()
		results := make(map[string]PregelState, len(active))
		errs := make(map[string]error)
		var mu sync.Mutex
		var wg sync.WaitGroup

		for _, name := range active {
			node, ok := r.pregelNodes[name]
			if !ok {
				return "", agentcore.NewNodeError("node not found", nil, "runner:pregel", name)
			}

			wg.Add(1)
			go func(nodeName string, nodeFn PregelNode) {
				defer wg.Done()
				nodeCtx, span, _ := agentcore.StartComponentRun(ctx, r.config.Tracer, "graph_node", nodeName)
				output, err := nodeFn(nodeCtx, baseState.Clone())
				if err != nil {
					span.RecordError(err)
				}
				span.End()
				mu.Lock()
				results[nodeName] = output
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
			for key, value := range results[name] {
				(*r.pregelState)[key] = value
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
			if router, ok := r.pregelConditionals[name]; ok {
				state := r.pregelState.Clone()
				for _, target := range router(ctx, state) {
					if err := r.validatePregelTarget(name, target); err != nil {
						return "", err
					}
					if target == PregelEnd {
						return "done", nil
					}
					if !nextSet[target] {
						nextSet[target] = true
						nextActive = append(nextActive, target)
					}
				}
			}
		}

		active = nextActive
	}

	return "done", nil
}

func (r *Runner) validatePregelTarget(source, target string) error {
	declared := r.pregelConditionalTargets[source]
	if len(declared) > 0 {
		for _, candidate := range declared {
			if candidate == target {
				return nil
			}
		}
		return agentcore.NewNodeError("conditional edge returned undeclared target", nil, "runner:pregel", source, target)
	}
	if target == PregelEnd {
		return nil
	}
	if _, ok := r.pregelNodes[target]; !ok {
		return agentcore.NewNodeError("conditional edge returned unknown target", nil, "runner:pregel", source, target)
	}
	return nil
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
