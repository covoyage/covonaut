package agentcore

import (
	"context"
	"sync"
)

// Extension is a plugin that can augment an agent with tools, hooks, and lifecycle callbacks.
type Extension interface {
	// Name returns a unique identifier for the extension.
	Name() string
	// Init is called once when the extension is registered with an agent.
	Init(ctx context.Context, agent *Agent) error
	// Dispose is called when the agent is shutting down or the extension is unloaded.
	Dispose() error
}

// ToolProvider is an optional interface extensions can implement to contribute tools.
type ToolProvider interface {
	Tools() []*Tool
}

// HookProvider is an optional interface extensions can implement to contribute hooks.
type HookProvider interface {
	BeforeHooks() []BeforeHook
	AfterHooks() []AfterHook
}

// MiddlewareProvider is an optional interface extensions can implement to contribute middleware.
type MiddlewareProvider interface {
	Middleware() []Middleware
}

// SystemPromptProvider is an optional interface extensions can implement
// to append content to the system prompt.
type SystemPromptProvider interface {
	SystemPromptSuffix() string
}

// TransformContextProvider is an optional interface extensions can implement
// to inject or rewrite messages before they are sent to the provider.
type TransformContextProvider interface {
	TransformContext(ctx context.Context, msgs []Message) []Message
}

// LifecycleProvider is an optional interface extensions can implement
// to participate in the agent execution lifecycle.
type LifecycleProvider interface {
	LifecycleHook() LifecycleHook
}

// EventSnapshotProvider is an optional interface extensions can implement
// to expose their current state as events for newly attached listeners.
type EventSnapshotProvider interface {
	SnapshotEvents() []Event
}

// ExtensionRegistry manages the lifecycle of extensions attached to an agent.
type ExtensionRegistry struct {
	mu         sync.RWMutex
	extensions []Extension
}

func NewExtensionRegistry() *ExtensionRegistry {
	return &ExtensionRegistry{}
}

// Register adds extensions and immediately initializes them.
func (r *ExtensionRegistry) Register(ctx context.Context, agent *Agent, exts ...Extension) error {
	for _, ext := range exts {
		if err := ext.Init(ctx, agent); err != nil {
			return err
		}

		agent.configMu.Lock()
		if tp, ok := ext.(ToolProvider); ok {
			agent.RegisterTools(tp.Tools()...)
		}
		if hp, ok := ext.(HookProvider); ok {
			for _, h := range hp.BeforeHooks() {
				agent.config.GlobalBefore = append(agent.config.GlobalBefore, h)
			}
			for _, h := range hp.AfterHooks() {
				agent.config.GlobalAfter = append(agent.config.GlobalAfter, h)
			}
		}
		if mp, ok := ext.(MiddlewareProvider); ok {
			agent.config.Middleware = append(agent.config.Middleware, mp.Middleware()...)
		}
		if sp, ok := ext.(SystemPromptProvider); ok {
			suffix := sp.SystemPromptSuffix()
			if suffix != "" {
				if agent.config.SystemPrompt != "" {
					agent.config.SystemPrompt += "\n\n" + suffix
				} else {
					agent.config.SystemPrompt = suffix
				}
			}
		}
		if tp, ok := ext.(TransformContextProvider); ok {
			prev := agent.config.TransformContext
			agent.config.TransformContext = func(ctx context.Context, msgs []Message) []Message {
				if prev != nil {
					msgs = prev(ctx, msgs)
				}
				return tp.TransformContext(ctx, msgs)
			}
		}
		if lp, ok := ext.(LifecycleProvider); ok {
			agent.config.Lifecycle = appendLifecycleHook(agent.config.Lifecycle, lp.LifecycleHook())
		}
		agent.configMu.Unlock()

		r.mu.Lock()
		r.extensions = append(r.extensions, ext)
		r.mu.Unlock()
	}
	return nil
}

func appendLifecycleHook(existing, next LifecycleHook) LifecycleHook {
	if next == nil {
		return existing
	}
	if existing == nil {
		return next
	}
	if chain, ok := existing.(LifecycleChain); ok {
		return append(chain, next)
	}
	return LifecycleChain{existing, next}
}

// Dispose tears down all registered extensions in reverse order.
func (r *ExtensionRegistry) Dispose() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var firstErr error
	for i := len(r.extensions) - 1; i >= 0; i-- {
		if err := r.extensions[i].Dispose(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	r.extensions = nil
	return firstErr
}

// Names returns the names of all registered extensions.
func (r *ExtensionRegistry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, len(r.extensions))
	for i, ext := range r.extensions {
		names[i] = ext.Name()
	}
	return names
}

// Visit calls fn for the extension with the given name.
func (r *ExtensionRegistry) Visit(name string, fn func(Extension)) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, ext := range r.extensions {
		if ext.Name() == name {
			fn(ext)
			return
		}
	}
}

// SnapshotEvents collects current-state events from extensions that expose them.
func (r *ExtensionRegistry) SnapshotEvents() []Event {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Event
	for _, ext := range r.extensions {
		if sp, ok := ext.(EventSnapshotProvider); ok {
			out = append(out, sp.SnapshotEvents()...)
		}
	}
	return out
}
