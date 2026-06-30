package a2ui

// Builder assembles the envelopes for a single surface on the server (agent)
// side. It accumulates components and data-model updates and emits an ordered
// slice of envelopes ready to stream to the client.
//
// Typical usage:
//
//	envs := a2ui.NewSurface("profile", a2ui.BasicCatalogID).
//		Add(a2ui.Column("root", "name", "title")).
//		Add(a2ui.Text("name", "Ada Lovelace")).
//		Add(a2ui.Text("title", "Mathematician")).
//		Data("/user/name", "Ada").
//		Build()
type Builder struct {
	surfaceID     string
	catalogID     string
	theme         map[string]any
	sendDataModel bool
	components    []Component
	dataUpdates   []*UpdateDataModel
}

// NewSurface starts building a surface with the given ID and catalog.
func NewSurface(surfaceID, catalogID string) *Builder {
	return &Builder{surfaceID: surfaceID, catalogID: catalogID}
}

// Theme sets the surface theme.
func (b *Builder) Theme(theme map[string]any) *Builder {
	b.theme = theme
	return b
}

// SendDataModel enables client-to-server data-model synchronization for the
// surface.
func (b *Builder) SendDataModel(enabled bool) *Builder {
	b.sendDataModel = enabled
	return b
}

// Add appends one or more components to the surface.
func (b *Builder) Add(components ...Component) *Builder {
	b.components = append(b.components, components...)
	return b
}

// Data records a data-model update that sets value at path.
func (b *Builder) Data(path string, value any) *Builder {
	b.dataUpdates = append(b.dataUpdates, &UpdateDataModel{
		SurfaceID: b.surfaceID, Path: path, Value: value, ValueSet: true,
	})
	return b
}

// RemoveData records a data-model update that removes the key at path.
func (b *Builder) RemoveData(path string) *Builder {
	b.dataUpdates = append(b.dataUpdates, &UpdateDataModel{SurfaceID: b.surfaceID, Path: path})
	return b
}

// Build returns the ordered envelopes for the surface: a createSurface, then a
// single updateComponents (if any components were added), then the data-model
// updates in the order they were declared.
func (b *Builder) Build() []Envelope {
	envs := make([]Envelope, 0, 2+len(b.dataUpdates))

	cs := &CreateSurface{
		SurfaceID:     b.surfaceID,
		CatalogID:     b.catalogID,
		Theme:         b.theme,
		SendDataModel: b.sendDataModel,
	}
	envs = append(envs, Envelope{Version: Version, CreateSurface: cs})

	if len(b.components) > 0 {
		envs = append(envs, Envelope{
			Version:          Version,
			UpdateComponents: &UpdateComponents{SurfaceID: b.surfaceID, Components: b.components},
		})
	}
	for _, u := range b.dataUpdates {
		envs = append(envs, Envelope{Version: Version, UpdateDataModel: u})
	}
	return envs
}

// Delete returns a deleteSurface envelope for the builder's surface.
func (b *Builder) Delete() Envelope {
	return NewDeleteSurface(b.surfaceID)
}

// ---------------------------------------------------------------------------
// Basic-catalog component constructors
// ---------------------------------------------------------------------------

// Text creates a Text component. The text argument may be a plain string or a
// Dynamic binding.
func Text(id string, text any) Component {
	return NewComponent(id, "Text", map[string]any{"text": text})
}

// Image creates an Image component bound to the given URL.
func Image(id string, url any) Component {
	return NewComponent(id, "Image", map[string]any{"url": url})
}

// Icon creates an Icon component referencing a named system icon.
func Icon(id, name string) Component {
	return NewComponent(id, "Icon", map[string]any{"name": name})
}

// Column creates a vertical container of the given child IDs.
func Column(id string, children ...string) Component {
	return NewComponent(id, "Column", map[string]any{"children": StaticChildren(children...)})
}

// Row creates a horizontal container of the given child IDs.
func Row(id string, children ...string) Component {
	return NewComponent(id, "Row", map[string]any{"children": StaticChildren(children...)})
}

// List creates a scrollable list container of the given child IDs.
func List(id string, children ...string) Component {
	return NewComponent(id, "List", map[string]any{"children": StaticChildren(children...)})
}

// TemplateList creates a List whose children are generated from a data-model
// array via a template component.
func TemplateList(id, path, templateID string) Component {
	return NewComponent(id, "List", map[string]any{"children": TemplateChildren(path, templateID)})
}

// Card creates a Card wrapping a single child component.
func Card(id, child string) Component {
	return NewComponent(id, "Card", map[string]any{"child": child})
}

// Divider creates a Divider component.
func Divider(id string) Component {
	return NewComponent(id, "Divider", nil)
}

// Button creates a Button with the given label text and action.
func Button(id string, text any, action Action) Component {
	return NewComponent(id, "Button", map[string]any{"text": text, "action": action})
}

// TextField creates a text input bound to the given data-model path.
func TextField(id, path string) Component {
	return NewComponent(id, "TextField", map[string]any{"value": Bind(path)})
}

// CheckBox creates a checkbox with a label, bound to the given data-model path.
func CheckBox(id string, label any, path string) Component {
	return NewComponent(id, "CheckBox", map[string]any{"label": label, "value": Bind(path)})
}

// Slider creates a slider bound to the given data-model path.
func Slider(id, path string) Component {
	return NewComponent(id, "Slider", map[string]any{"value": Bind(path)})
}
