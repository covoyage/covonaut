package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

// Checkpoint captures the complete execution state at a specific point.
type Checkpoint struct {
	ID        string           `json:"id"`
	GraphID   string           `json:"graph_id,omitempty"`
	NodeName  string           `json:"node_name"`
	StepIndex int64            `json:"step_index"`
	State     json.RawMessage  `json:"state"`
	Metadata  map[string]any   `json:"metadata,omitempty"`
	CreatedAt time.Time        `json:"created_at"`
}

// CheckpointStore persists and retrieves checkpoints.
type CheckpointStore interface {
	Save(ctx context.Context, cp Checkpoint) error
	Load(ctx context.Context, id string) (*Checkpoint, error)
	List(ctx context.Context, graphID string) ([]Checkpoint, error)
	Delete(ctx context.Context, id string) error
}

var ErrInterrupt = fmt.Errorf("execution interrupted")

// InterruptConfig configures interrupt behavior for a node.
type InterruptConfig struct {
	Before bool
	After  bool
}

// InterruptableGraph wraps a CompiledGraph with checkpoint and interrupt support.
type InterruptableGraph struct {
	graph      *CompiledGraph
	store      CheckpointStore
	interrupts map[string]InterruptConfig

	mu          sync.Mutex
	nodeOutputs map[string]string
}

func NewInterruptableGraph(cg *CompiledGraph, store CheckpointStore) *InterruptableGraph {
	return &InterruptableGraph{
		graph:      cg,
		store:      store,
		interrupts: make(map[string]InterruptConfig),
	}
}

func (ig *InterruptableGraph) SetInterrupt(nodeName string, cfg InterruptConfig) {
	ig.interrupts[nodeName] = cfg
}

// InterruptResult is returned when execution is paused.
type InterruptResult struct {
	CheckpointID string
	NodeName     string
	Phase        string
	Output       string
}

func (ig *InterruptableGraph) Run(ctx context.Context, input string) (string, *InterruptResult, error) {
	ig.mu.Lock()
	ig.nodeOutputs = make(map[string]string)
	ig.mu.Unlock()

	return ig.runFrom(ctx, input, 0, nil)
}

func (ig *InterruptableGraph) Resume(ctx context.Context, checkpointID string, input string) (string, *InterruptResult, error) {
	cp, err := ig.store.Load(ctx, checkpointID)
	if err != nil {
		return "", nil, fmt.Errorf("checkpoint load failed: %w", err)
	}

	var saved checkpointState
	if err := json.Unmarshal(cp.State, &saved); err != nil {
		return "", nil, fmt.Errorf("checkpoint decode failed: %w", err)
	}

	ig.mu.Lock()
	ig.nodeOutputs = saved.Outputs
	ig.mu.Unlock()

	resumeInput := input
	if resumeInput == "" {
		resumeInput = saved.LastInput
	}

	return ig.runFrom(ctx, resumeInput, cp.StepIndex, cp)
}

type checkpointState struct {
	Outputs   map[string]string `json:"outputs"`
	LastInput string            `json:"last_input"`
}

func (ig *InterruptableGraph) runFrom(ctx context.Context, input string, startLayer int64, resumeCP *Checkpoint) (string, *InterruptResult, error) {
	var steps int64
	skipFirstInterruptBefore := resumeCP != nil

	for layerIdx := startLayer; layerIdx < int64(len(ig.graph.Sorted)); layerIdx++ {
		layer := ig.graph.Sorted[layerIdx]

		var layerNodes []string
		for _, name := range layer {
			ig.mu.Lock()
			_, reachable := ig.nodeOutputs[name]
			ig.mu.Unlock()
			if reachable || name == ig.graph.Entry {
				layerNodes = append(layerNodes, name)
			}
		}
		if len(layerNodes) == 0 {
			continue
		}

		for _, name := range layerNodes {
			steps++
			if steps > ig.graph.MaxSteps {
				return "", nil, agentcore.WrapNodeError(agentcore.ErrExceedMaxSteps, "interruptable_graph")
			}

			intCfg, hasInterrupt := ig.interrupts[name]

			if hasInterrupt && intCfg.Before {
				if skipFirstInterruptBefore {
					skipFirstInterruptBefore = false
				} else {
					ir, err := ig.saveCheckpoint(ctx, name, "before", input, layerIdx)
					if err != nil {
						return "", nil, err
					}
					return "", ir, nil
				}
			}

			nodeInput := ig.resolveNodeInput(name, input)

			step := ig.graph.graph.nodes[name]
			out, err := step.Run(ctx, nodeInput)
			if err != nil {
				return "", nil, agentcore.WrapNodeError(err, "interruptable_graph:"+name)
			}

			ig.mu.Lock()
			ig.nodeOutputs[name] = out
			for _, to := range ig.graph.graph.edges[name] {
				if _, exists := ig.nodeOutputs[to]; !exists {
					ig.nodeOutputs[to] = ""
				}
			}
			ig.mu.Unlock()

			if hasInterrupt && intCfg.After {
				ir, err := ig.saveCheckpoint(ctx, name, "after", input, layerIdx+1)
				if err != nil {
					return "", nil, err
				}
				ir.Output = out
				return "", ir, nil
			}
		}
	}

	ig.mu.Lock()
	result := FindTerminalOutput(allNodes(ig.graph.graph.nodes, ig.graph.StreamNodes), ig.graph.graph.edges, ig.nodeOutputs)
	ig.mu.Unlock()
	return result, nil, nil
}

func (ig *InterruptableGraph) resolveNodeInput(name, graphInput string) string {
	ig.mu.Lock()
	defer ig.mu.Unlock()

	preds := ig.graph.RevEdges[name]
	if len(preds) == 0 {
		return graphInput
	}
	var parts []string
	for _, p := range preds {
		if out, ok := ig.nodeOutputs[p]; ok && out != "" {
			parts = append(parts, out)
		}
	}
	if len(parts) == 0 {
		return graphInput
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return JoinOutputs(parts)
}

func (ig *InterruptableGraph) saveCheckpoint(ctx context.Context, nodeName, phase, input string, layerIdx int64) (*InterruptResult, error) {
	ig.mu.Lock()
	state := checkpointState{
		Outputs:   ig.nodeOutputs,
		LastInput: input,
	}
	ig.mu.Unlock()

	stateBytes, err := json.Marshal(state)
	if err != nil {
		return nil, fmt.Errorf("checkpoint encode failed: %w", err)
	}

	cpID := fmt.Sprintf("cp_%s_%s_%d", nodeName, phase, time.Now().UnixNano())
	cp := Checkpoint{
		ID:        cpID,
		NodeName:  nodeName,
		StepIndex: layerIdx,
		State:     stateBytes,
		Metadata:  map[string]any{"phase": phase},
		CreatedAt: time.Now(),
	}

	if err := ig.store.Save(ctx, cp); err != nil {
		return nil, fmt.Errorf("checkpoint save failed: %w", err)
	}

	return &InterruptResult{
		CheckpointID: cpID,
		NodeName:     nodeName,
		Phase:        phase,
	}, nil
}

// --- In-memory checkpoint store ---

type MemoryCheckpointStore struct {
	mu          sync.RWMutex
	checkpoints map[string]Checkpoint
}

func NewMemoryCheckpointStore() *MemoryCheckpointStore {
	return &MemoryCheckpointStore{checkpoints: make(map[string]Checkpoint)}
}

func (s *MemoryCheckpointStore) Save(_ context.Context, cp Checkpoint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checkpoints[cp.ID] = cp
	return nil
}

func (s *MemoryCheckpointStore) Load(_ context.Context, id string) (*Checkpoint, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp, ok := s.checkpoints[id]
	if !ok {
		return nil, fmt.Errorf("checkpoint %q not found", id)
	}
	return &cp, nil
}

func (s *MemoryCheckpointStore) List(_ context.Context, _ string) ([]Checkpoint, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Checkpoint, 0, len(s.checkpoints))
	for _, cp := range s.checkpoints {
		result = append(result, cp)
	}
	return result, nil
}

func (s *MemoryCheckpointStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.checkpoints, id)
	return nil
}
