package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

// PregelState is the shared mutable state passed between Pregel nodes.
type PregelState map[string]any

func (s PregelState) Clone() PregelState {
	cp := make(PregelState, len(s))
	for k, v := range s {
		cp[k] = deepCopyValue(v)
	}
	return cp
}

// deepCopyValue recursively deep-copies a value that may contain nested slices
// and maps (the typical shape of PregelState values). It avoids the silent data
// loss of JSON round-tripping (e.g. channels, functions, int64 → float64).
func deepCopyValue(v any) any {
	if v == nil {
		return nil
	}
	switch val := v.(type) {
	case map[string]any:
		cp := make(map[string]any, len(val))
		for k, v := range val {
			cp[k] = deepCopyValue(v)
		}
		return cp
	case []any:
		cp := make([]any, len(val))
		for i, v := range val {
			cp[i] = deepCopyValue(v)
		}
		return cp
	case []agentcore.Message:
		cp := make([]agentcore.Message, len(val))
		for i, m := range val {
			cp[i] = m.Clone()
		}
		return cp
	}

	// Handle typed maps and slices via reflection.
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Map:
		cp := reflect.MakeMap(rv.Type())
		iter := rv.MapRange()
		for iter.Next() {
			key := iter.Key()
			val := deepCopyValue(iter.Value().Interface())
			cp.SetMapIndex(key, reflect.ValueOf(val))
		}
		return cp.Interface()
	case reflect.Slice:
		cp := reflect.MakeSlice(rv.Type(), rv.Len(), rv.Cap())
		for i := 0; i < rv.Len(); i++ {
			elem := deepCopyValue(rv.Index(i).Interface())
			cp.Index(i).Set(reflect.ValueOf(elem))
		}
		return cp.Interface()
	}

	// Immutable types (string, int, float, bool, struct without pointers)
	// are safe to share.
	return v
}

func (s PregelState) GetString(key string) string {
	v, _ := s[key].(string)
	return v
}

func (s PregelState) GetMessages(key string) []agentcore.Message {
	raw, ok := s[key]
	if !ok {
		return nil
	}
	if msgs, ok := raw.([]agentcore.Message); ok {
		return msgs
	}
	return nil
}

const PregelEnd = "__end__"

type PregelNode func(ctx context.Context, state PregelState) (PregelState, error)

type PregelEdgeRouter func(ctx context.Context, state PregelState) []string

type PregelGraph struct {
	nodes            map[string]PregelNode
	edges            map[string][]string
	conditionalEdges map[string]PregelEdgeRouter
}

func NewPregelGraph() *PregelGraph {
	return &PregelGraph{
		nodes:            make(map[string]PregelNode),
		edges:            make(map[string][]string),
		conditionalEdges: make(map[string]PregelEdgeRouter),
	}
}

func (pg *PregelGraph) AddNode(name string, node PregelNode) error {
	if name == PregelEnd {
		return fmt.Errorf("pregel: %q is a reserved name", PregelEnd)
	}
	if _, exists := pg.nodes[name]; exists {
		return fmt.Errorf("pregel: duplicate node %q", name)
	}
	pg.nodes[name] = node
	return nil
}

func (pg *PregelGraph) AddEdge(from, to string) error {
	if _, ok := pg.nodes[from]; !ok {
		return fmt.Errorf("pregel: unknown source node %q", from)
	}
	if to != PregelEnd {
		if _, ok := pg.nodes[to]; !ok {
			return fmt.Errorf("pregel: unknown target node %q", to)
		}
	}
	pg.edges[from] = append(pg.edges[from], to)
	return nil
}

func (pg *PregelGraph) SetConditionalEdge(from string, router PregelEdgeRouter) error {
	if _, ok := pg.nodes[from]; !ok {
		return fmt.Errorf("pregel: unknown source node %q", from)
	}
	pg.conditionalEdges[from] = router
	return nil
}

type CompiledPregelGraph struct {
	pg       *PregelGraph
	entry    string
	maxSteps int64
}

func (pg *PregelGraph) Compile(entryNode string, maxSteps ...int64) (*CompiledPregelGraph, error) {
	if _, ok := pg.nodes[entryNode]; !ok {
		return nil, fmt.Errorf("pregel: entry node %q not found", entryNode)
	}

	limit := int64(100)
	if len(maxSteps) > 0 && maxSteps[0] > 0 {
		limit = maxSteps[0]
	}

	return &CompiledPregelGraph{pg: pg, entry: entryNode, maxSteps: limit}, nil
}

func (cpg *CompiledPregelGraph) Run(ctx context.Context, initial PregelState) (PregelState, error) {
	state := initial.Clone()
	active := []string{cpg.entry}
	var steps int64

	for len(active) > 0 {
		steps++
		if steps > cpg.maxSteps {
			return state, agentcore.WrapNodeError(agentcore.ErrExceedMaxSteps, "pregel")
		}

		var nextActive []string
		nextSet := make(map[string]bool)

		results := make(map[string]PregelState)
		errs := make(map[string]error)
		var mu sync.Mutex
		var wg sync.WaitGroup

		for _, name := range active {
			node, ok := cpg.pg.nodes[name]
			if !ok {
				return state, agentcore.NewNodeError("node not found", nil, "pregel", name)
			}

			wg.Add(1)
			go func(nodeName string, nodeFn PregelNode) {
				defer wg.Done()
				snapshot := state.Clone()
				out, err := nodeFn(ctx, snapshot)
				mu.Lock()
				results[nodeName] = out
				errs[nodeName] = err
				mu.Unlock()
			}(name, node)
		}

		wg.Wait()

		for name, err := range errs {
			if err != nil {
				return state, agentcore.WrapNodeError(err, "pregel:"+name)
			}
		}

		for _, out := range results {
			for k, v := range out {
				state[k] = v
			}
		}

		for _, name := range active {
			if staticTargets, ok := cpg.pg.edges[name]; ok {
				for _, t := range staticTargets {
					if t == PregelEnd {
						return state, nil
					}
					if !nextSet[t] {
						nextSet[t] = true
						nextActive = append(nextActive, t)
					}
				}
			}

			if router, ok := cpg.pg.conditionalEdges[name]; ok {
				targets := router(ctx, state)
				for _, t := range targets {
					if t == PregelEnd {
						return state, nil
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

	return state, nil
}

func (cpg *CompiledPregelGraph) RunString(ctx context.Context, input string) (string, error) {
	initial := PregelState{"input": input}
	final, err := cpg.Run(ctx, initial)
	if err != nil {
		return "", err
	}
	return final.GetString("output"), nil
}

// PregelCheckpointer adds checkpoint support to Pregel execution.
type PregelCheckpointer struct {
	graph *CompiledPregelGraph
	store CheckpointStore
}

func NewPregelCheckpointer(cpg *CompiledPregelGraph, store CheckpointStore) *PregelCheckpointer {
	return &PregelCheckpointer{graph: cpg, store: store}
}

func (pc *PregelCheckpointer) RunWithCheckpoints(ctx context.Context, initial PregelState, graphID string) (PregelState, error) {
	state := initial.Clone()
	active := []string{pc.graph.entry}
	var steps int64

	for len(active) > 0 {
		steps++
		if steps > pc.graph.maxSteps {
			return state, agentcore.WrapNodeError(agentcore.ErrExceedMaxSteps, "pregel_checkpointed")
		}

		stateBytes, err := json.Marshal(state)
		if err != nil {
			return state, fmt.Errorf("pregel checkpoint marshal failed: %w", err)
		}
		cp := Checkpoint{
			ID:        fmt.Sprintf("pregel_%s_step_%d", graphID, steps),
			GraphID:   graphID,
			NodeName:  active[0],
			StepIndex: steps,
			State:     stateBytes,
			Metadata:  map[string]any{"active_nodes": active},
			CreatedAt: time.Now(),
		}
		if err := pc.store.Save(ctx, cp); err != nil {
			return state, fmt.Errorf("pregel checkpoint save failed: %w", err)
		}

		var nextActive []string
		nextSet := make(map[string]bool)

		for _, name := range active {
			node, ok := pc.graph.pg.nodes[name]
			if !ok {
				return state, agentcore.NewNodeError("node not found", nil, "pregel_checkpointed", name)
			}
			out, err := node(ctx, state)
			if err != nil {
				return state, agentcore.WrapNodeError(err, "pregel_checkpointed:"+name)
			}
			for k, v := range out {
				state[k] = v
			}

			if targets, ok := pc.graph.pg.edges[name]; ok {
				for _, t := range targets {
					if t == PregelEnd {
						return state, nil
					}
					if !nextSet[t] {
						nextSet[t] = true
						nextActive = append(nextActive, t)
					}
				}
			}
			if router, ok := pc.graph.pg.conditionalEdges[name]; ok {
				for _, t := range router(ctx, state) {
					if t == PregelEnd {
						return state, nil
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

	return state, nil
}
