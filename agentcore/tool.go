package agentcore

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
)

// ToolFunc is the function signature for tool implementations.
type ToolFunc func(ctx context.Context, args json.RawMessage) (any, error)

// Tool represents a callable tool available to the agent.
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any
	Func        ToolFunc
	Before      []BeforeHook
	After       []AfterHook
}

// Definition converts a Tool to its schema representation for the model.
func (t *Tool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        t.Name,
		Description: t.Description,
		Parameters:  t.Parameters,
	}
}

// Registry is a thread-safe collection of available tools.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]*Tool
}

func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]*Tool)}
}

func (r *Registry) Register(tools ...*Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range tools {
		r.tools[t.Name] = t
	}
}

func (r *Registry) Get(name string) (*Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

func (r *Registry) Definitions() []ToolDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	defs := make([]ToolDefinition, 0, len(r.tools))
	for _, t := range r.tools {
		defs = append(defs, t.Definition())
	}
	sort.Slice(defs, func(i, j int) bool {
		return defs[i].Name < defs[j].Name
	})
	return defs
}

func (r *Registry) Unregister(names ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, name := range names {
		delete(r.tools, name)
	}
}

func (r *Registry) Count() int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return int64(len(r.tools))
}

func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Tools returns all registered tools.
func (r *Registry) Tools() []*Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tools := make([]*Tool, 0, len(r.tools))
	for _, t := range r.tools {
		tools = append(tools, t)
	}
	return tools
}
