package agentcore

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
)

var (
	ErrStaleToolRegistration = errors.New("tool registration changed; reconsider the call in the next model step")
	ErrToolStepClosed        = errors.New("tool registration step is closed")
)

type toolRegistration struct{ tool *Tool }

type stepTool struct {
	registration *toolRegistration
	tool         *Tool
}

type toolRegistrationStep struct {
	mu       sync.RWMutex
	registry *Registry
	tools    map[string]stepTool
	closed   bool
}

type toolStepContextKey struct{}
type toolInvocationContextKey struct{}
type toolInvocationIdentity struct {
	registry *Registry
	name     string
}
type toolStepContext struct {
	step   *toolRegistrationStep
	parent *toolStepContext
}

func toolStepFromContext(ctx context.Context, registry *Registry) *toolRegistrationStep {
	for link, _ := ctx.Value(toolStepContextKey{}).(*toolStepContext); link != nil; link = link.parent {
		if link.step.registry == registry {
			return link.step
		}
	}
	return nil
}

func withToolStep(ctx context.Context, step *toolRegistrationStep) context.Context {
	parent, _ := ctx.Value(toolStepContextKey{}).(*toolStepContext)
	return context.WithValue(ctx, toolStepContextKey{}, &toolStepContext{step: step, parent: parent})
}

func (r *Registry) captureStep() *toolRegistrationStep {
	r.mu.RLock()
	defer r.mu.RUnlock()
	step := &toolRegistrationStep{registry: r, tools: make(map[string]stepTool, len(r.tools))}
	for name, tool := range r.tools {
		step.tools[name] = stepTool{registration: r.registrations[name], tool: cloneRegisteredTool(tool)}
	}
	return step
}

func (step *toolRegistrationStep) close() {
	step.mu.Lock()
	defer step.mu.Unlock()
	step.closed = true
	step.tools = nil
}

// This check is dispatch admission, not a lock held across user code. Calls
// admitted before replacement/closure finish on their captured implementation.
func (step *toolRegistrationStep) resolve(name string) (*Tool, bool, error) {
	step.mu.RLock()
	defer step.mu.RUnlock()
	if step.closed {
		return nil, false, ErrToolStepClosed
	}
	step.registry.mu.RLock()
	defer step.registry.mu.RUnlock()
	entry, captured := step.tools[name]
	current := step.registry.registrations[name]
	if captured {
		if current != entry.registration {
			return nil, false, fmt.Errorf("%w: %s", ErrStaleToolRegistration, name)
		}
		return entry.tool, true, nil
	}
	if current != nil {
		return nil, false, fmt.Errorf("%w: %s was registered after this step began", ErrStaleToolRegistration, name)
	}
	return nil, false, nil
}

func (step *toolRegistrationStep) definitions() ([]ToolDefinition, error) {
	step.mu.RLock()
	defer step.mu.RUnlock()
	if step.closed {
		return nil, ErrToolStepClosed
	}
	definitions := make([]ToolDefinition, 0, len(step.tools))
	for _, entry := range step.tools {
		definitions = append(definitions, cloneRegisteredTool(entry.tool).Definition())
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].Name < definitions[j].Name })
	return definitions, nil
}

func cloneRegisteredTool(tool *Tool) *Tool {
	copy := *tool
	copy.Tags = append([]string(nil), tool.Tags...)
	copy.Before = append([]BeforeHook(nil), tool.Before...)
	copy.After = append([]AfterHook(nil), tool.After...)
	if tool.Parameters != nil {
		copy.Parameters = cloneToolSchema(reflect.ValueOf(tool.Parameters)).Interface().(map[string]any)
	}
	if tool.ConcurrencySafe != nil {
		safe := *tool.ConcurrencySafe
		copy.ConcurrencySafe = &safe
	}
	return &copy
}

// Schema metadata is a JSON-shaped tree, including typed Go slices/maps.
func cloneToolSchema(value reflect.Value) reflect.Value {
	return cloneToolSchemaValue(value, make(map[schemaCloneReference]reflect.Value))
}

type schemaCloneReference struct {
	kind    reflect.Kind
	typ     reflect.Type
	pointer uintptr
	length  int
}

func cloneToolSchemaValue(value reflect.Value, seen map[schemaCloneReference]reflect.Value) reflect.Value {
	var reference schemaCloneReference
	if (value.Kind() == reflect.Map || value.Kind() == reflect.Slice) && !value.IsNil() {
		reference = schemaCloneReference{kind: value.Kind(), typ: value.Type(), pointer: uintptr(value.UnsafePointer()), length: value.Len()}
		if copy, ok := seen[reference]; ok {
			return copy
		}
	}
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return value
		}
		copy := reflect.New(value.Type()).Elem()
		copy.Set(cloneToolSchemaValue(value.Elem(), seen))
		return copy
	case reflect.Map:
		if value.IsNil() {
			return value
		}
		copy := reflect.MakeMapWithSize(value.Type(), value.Len())
		seen[reference] = copy
		for iter := value.MapRange(); iter.Next(); {
			copy.SetMapIndex(iter.Key(), cloneToolSchemaValue(iter.Value(), seen))
		}
		return copy
	case reflect.Slice:
		if value.IsNil() {
			return value
		}
		copy := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		seen[reference] = copy
		for i := 0; i < value.Len(); i++ {
			copy.Index(i).Set(cloneToolSchemaValue(value.Index(i), seen))
		}
		return copy
	case reflect.Array:
		copy := reflect.New(value.Type()).Elem()
		for i := 0; i < value.Len(); i++ {
			copy.Index(i).Set(cloneToolSchemaValue(value.Index(i), seen))
		}
		return copy
	default:
		return value
	}
}

func (*Agent) ToolRegistrationIdentitySupported() bool { return true }

// GetToolForContext returns a detached copy of the active step's registration.
// Outside a step it retains GetTool's live configuration behavior.
func (a *Agent) GetToolForContext(ctx context.Context, name string) (*Tool, bool) {
	if step := toolStepFromContext(ctx, a.registry); step != nil {
		tool, ok, err := step.resolve(name)
		if err != nil || !ok {
			return nil, false
		}
		return cloneRegisteredTool(tool), true
	}
	return a.GetTool(name)
}

// ToolDefinitionsForContext inspects the full step, not just model visibility.
func (a *Agent) ToolDefinitionsForContext(ctx context.Context) ([]ToolDefinition, error) {
	if step := toolStepFromContext(ctx, a.registry); step != nil {
		return step.definitions()
	}
	return a.registry.Definitions(), nil
}
