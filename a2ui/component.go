package a2ui

import "encoding/json"

// Component is a single node in an A2UI surface. Components are sent as a flat
// list (an adjacency list); parent/child relationships are expressed through ID
// references held in component properties (e.g. "child" or "children").
//
// The wire form is a flat JSON object: the reserved keys "id" and "component"
// sit alongside any number of component-specific properties. Props therefore
// holds every property other than the id and type.
type Component struct {
	// ID uniquely identifies this component within its surface. Exactly one
	// component in a surface must have the ID "root".
	ID string
	// Type is the component type, e.g. "Text", "Button", "Column" (serialized
	// as the "component" field).
	Type string
	// Props holds all component-specific properties. Values may be plain Go
	// values, Dynamic bindings, ChildList, Action, []Check, etc.
	Props map[string]any
}

// NewComponent constructs a Component with the given id, type and properties.
func NewComponent(id, typ string, props map[string]any) Component {
	if props == nil {
		props = map[string]any{}
	}
	return Component{ID: id, Type: typ, Props: props}
}

// Set assigns a property and returns the component for chaining.
func (c *Component) Set(key string, value any) *Component {
	if c.Props == nil {
		c.Props = map[string]any{}
	}
	c.Props[key] = value
	return c
}

// MarshalJSON implements json.Marshaler, flattening id, component and props
// into a single JSON object. Note: "id" and "component" are reserved JSON keys
// and any props with those names are silently omitted to avoid collision.
func (c Component) MarshalJSON() ([]byte, error) {
	m := make(map[string]any, len(c.Props)+2)
	for k, v := range c.Props {
		if k == "id" || k == "component" {
			continue
		}
		m[k] = v
	}
	m["id"] = c.ID
	m["component"] = c.Type
	return json.Marshal(m)
}

// UnmarshalJSON implements json.Unmarshaler, splitting the flat object back into
// id, type and the remaining properties.
func (c *Component) UnmarshalJSON(data []byte) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	c.Props = make(map[string]any, len(m))
	for k, raw := range m {
		switch k {
		case "id":
			if err := json.Unmarshal(raw, &c.ID); err != nil {
				return err
			}
		case "component":
			if err := json.Unmarshal(raw, &c.Type); err != nil {
				return err
			}
		default:
			var v any
			_ = json.Unmarshal(raw, &v) // always succeeds when the outer json.Unmarshal succeeded
			c.Props[k] = v
		}
	}
	return nil
}

// ChildList describes the children of a container component. It is either a
// static array of component IDs, or a template that generates one child per
// item in a bound data-model list.
type ChildList struct {
	// Static is an explicit array of child component IDs.
	Static []string
	// Template, when non-nil, generates children from a data binding.
	Template *ChildTemplate
}

// ChildTemplate generates a child component for each element of the array found
// at Path, instantiating the component identified by ComponentID for each item.
type ChildTemplate struct {
	// Path is the JSON Pointer to the array to iterate over.
	Path string `json:"path"`
	// ComponentID is the ID of the template component to instantiate per item.
	ComponentID string `json:"componentId"`
}

// StaticChildren is a convenience constructor for an explicit child list.
func StaticChildren(ids ...string) ChildList { return ChildList{Static: ids} }

// TemplateChildren is a convenience constructor for a templated child list.
func TemplateChildren(path, componentID string) ChildList {
	return ChildList{Template: &ChildTemplate{Path: path, ComponentID: componentID}}
}

// MarshalJSON implements json.Marshaler.
func (cl ChildList) MarshalJSON() ([]byte, error) {
	if cl.Template != nil {
		return json.Marshal(cl.Template)
	}
	if cl.Static == nil {
		return json.Marshal([]string{})
	}
	return json.Marshal(cl.Static)
}

// UnmarshalJSON implements json.Unmarshaler, accepting either an array of IDs or
// a template object.
func (cl *ChildList) UnmarshalJSON(data []byte) error {
	*cl = ChildList{}
	var arr []string
	if err := json.Unmarshal(data, &arr); err == nil {
		cl.Static = arr
		return nil
	}
	var tmpl ChildTemplate
	if err := json.Unmarshal(data, &tmpl); err != nil {
		return err
	}
	cl.Template = &tmpl
	return nil
}

// Action defines what happens when the user interacts with a component. It is
// either a server event or a local client-side function call.
type Action struct {
	// Event, when non-nil, is dispatched back to the server.
	Event *ActionEvent `json:"event,omitempty"`
	// FunctionCall, when non-nil, runs a local client-side function.
	FunctionCall *FunctionCall `json:"functionCall,omitempty"`
}

// ActionEvent is a named event delivered to the server, optionally carrying a
// context payload whose values may reference data-model paths.
type ActionEvent struct {
	Name    string         `json:"name"`
	Context map[string]any `json:"context,omitempty"`
}

// EventAction builds an Action that dispatches a named server event.
func EventAction(name string, context map[string]any) Action {
	return Action{Event: &ActionEvent{Name: name, Context: context}}
}

// FunctionAction builds an Action that runs a local client-side function.
func FunctionAction(name string, args map[string]any) Action {
	return Action{FunctionCall: &FunctionCall{CallName: name, Args: args}}
}

// Check is a client-side validation or condition attached to an input or button
// component. A check either invokes a function directly (Call/Args) or evaluates
// a Condition expression; Message is shown when the check fails.
type Check struct {
	// Call names the function to invoke directly.
	Call string `json:"call,omitempty"`
	// Args are the arguments for Call.
	Args map[string]any `json:"args,omitempty"`
	// Condition is a (possibly nested) boolean function expression. Used as an
	// alternative to Call/Args.
	Condition *FunctionCall `json:"condition,omitempty"`
	// Message describes why the check failed.
	Message string `json:"message,omitempty"`
}
