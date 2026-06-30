package a2ui

import (
	"encoding/json"
	"fmt"
)

// Dynamic is a data-binding-aware value. Any A2UI property that can be bound to
// the data model is represented as a Dynamic. It resolves to exactly one of:
//
//   - a literal value (string, number, boolean, list, object);
//   - a data binding, expressed as a JSON Pointer ({"path": "/user/name"});
//   - a client-side function call ({"call": "formatString", "args": {...}}).
//
// Use the constructors Lit, Bind and Call to build Dynamic values.
type Dynamic struct {
	// Literal holds a static value. It is used when neither Path nor Function
	// is set.
	Literal any
	// IsPath reports whether this Dynamic is a data binding. When true, Path
	// holds the JSON Pointer.
	IsPath bool
	// Path is the JSON Pointer for a data binding (valid only when IsPath).
	Path string
	// Function holds a client-side function call, when non-nil.
	Function *FunctionCall
}

// Lit returns a Dynamic wrapping a literal value.
func Lit(v any) Dynamic { return Dynamic{Literal: v} }

// Bind returns a Dynamic that binds to the data model at the given JSON Pointer.
func Bind(path string) Dynamic { return Dynamic{IsPath: true, Path: path} }

// Call returns a Dynamic that evaluates a client-side function.
func Call(name string, args map[string]any) Dynamic {
	return Dynamic{Function: &FunctionCall{CallName: name, Args: args}}
}

// MarshalJSON implements json.Marshaler.
func (d Dynamic) MarshalJSON() ([]byte, error) {
	switch {
	case d.Function != nil:
		return json.Marshal(d.Function)
	case d.IsPath:
		return json.Marshal(map[string]string{"path": d.Path})
	default:
		return json.Marshal(d.Literal)
	}
}

// UnmarshalJSON implements json.Unmarshaler. It distinguishes function calls
// (objects with a "call" key), data bindings (objects whose only key is "path")
// and literals (anything else).
func (d *Dynamic) UnmarshalJSON(data []byte) error {
	*d = Dynamic{}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err == nil && probe != nil {
		if _, ok := probe["call"]; ok {
			var fc FunctionCall
			if err := json.Unmarshal(data, &fc); err != nil {
				return err
			}
			d.Function = &fc
			return nil
		}
		if raw, ok := probe["path"]; ok {
			if len(probe) != 1 {
				return fmt.Errorf("a2ui: dynamic with %q and extra keys is invalid", "path")
			}
			var p string
			if err := json.Unmarshal(raw, &p); err != nil {
				return err
			}
			d.IsPath = true
			d.Path = p
			return nil
		}
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	d.Literal = v
	return nil
}

// FunctionCall references a named client-side function from the active catalog,
// invoked with the provided named arguments. Argument values may themselves be
// literals, data bindings ({"path": ...}) or nested function calls.
type FunctionCall struct {
	// CallName is the registered function name (serialized as "call").
	CallName string `json:"call"`
	// Args holds the named arguments passed to the function.
	Args map[string]any `json:"args,omitempty"`
}
