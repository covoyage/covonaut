package graph

import (
	"context"
	"sync"

	"github.com/covoyage/covonaut/agentcore"
)

// GraphStateKey is the context key for accessing per-run graph state.
type GraphStateKey struct{}

// GraphState is shared mutable state available to all nodes in a DAG execution.
// It is created at the start of Run() and accessible from any node via
// GetGraphState(ctx). Write operations are protected by a mutex since nodes
// in the same layer execute concurrently.
type GraphState struct {
	mu   sync.RWMutex
	data map[string]any
}

// NewGraphState creates an empty GraphState.
func NewGraphState() *GraphState {
	return &GraphState{data: make(map[string]any)}
}

// Get retrieves a value from the shared state.
func (gs *GraphState) Get(key string) any {
	gs.mu.RLock()
	defer gs.mu.RUnlock()
	return gs.data[key]
}

// Set stores a value in the shared state.
func (gs *GraphState) Set(key string, val any) {
	gs.mu.Lock()
	defer gs.mu.Unlock()
	gs.data[key] = val
}

// GetString is a typed convenience for Get.
func (gs *GraphState) GetString(key string) string {
	v, _ := gs.Get(key).(string)
	return v
}

// GetMessages retrieves a message slice from the shared state (e.g., for
// passing conversation context between nodes).
func (gs *GraphState) GetMessages(key string) []agentcore.Message {
	v := gs.Get(key)
	if v == nil {
		return nil
	}
	if msgs, ok := v.([]agentcore.Message); ok {
		return msgs
	}
	return nil
}

// SetMessages stores a message slice in the shared state.
func (gs *GraphState) SetMessages(key string, msgs []agentcore.Message) {
	gs.Set(key, msgs)
}

// WithGraphState embeds a GraphState in the context for the duration of a
// graph Run.
func WithGraphState(ctx context.Context, gs *GraphState) context.Context {
	return context.WithValue(ctx, GraphStateKey{}, gs)
}

// GetGraphState extracts the per-run GraphState from the context. Returns nil
// if no state has been set (e.g., the graph was compiled without WithStateFn).
func GetGraphState(ctx context.Context) *GraphState {
	v, _ := ctx.Value(GraphStateKey{}).(*GraphState)
	return v
}

// GenStateFn generates a new GraphState at the start of each Run invocation.
type GenStateFn func(ctx context.Context) *GraphState
