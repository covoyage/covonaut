package a2ui

// Catalog describes the set of components and functions a surface may use. The
// envelope schema is catalog-agnostic; a Catalog supplies the information needed
// to validate component types, function names and parent/child structure.
type Catalog struct {
	// ID is the unique catalog identifier (matches CreateSurface.CatalogID).
	ID string
	// Components maps component type names to their definitions.
	Components map[string]ComponentDef
	// Functions is the set of registered function names.
	Functions map[string]struct{}
	// ThemeProperties is the set of recognized theme property names.
	ThemeProperties map[string]struct{}
}

// ComponentDef describes a single component type within a catalog, including
// which of its properties hold structural references to other components.
type ComponentDef struct {
	// Name is the component type name.
	Name string
	// ChildFields lists properties holding a single child component ID.
	ChildFields []string
	// ChildListFields lists properties holding a ChildList (array of IDs or a
	// template).
	ChildListFields []string
	// NestedChildFields maps property names (containing an array of objects) to
	// the key within each object that holds a child component ID. This handles
	// components like Tabs where children are nested inside a structured array.
	NestedChildFields map[string]string
	// Input reports whether the component is an interactive input (establishing
	// two-way data binding).
	Input bool
}

// HasComponent reports whether the catalog defines the given component type.
func (c *Catalog) HasComponent(typ string) bool {
	_, ok := c.Components[typ]
	return ok
}

// HasFunction reports whether the catalog registers the given function name.
func (c *Catalog) HasFunction(name string) bool {
	_, ok := c.Functions[name]
	return ok
}

// BasicCatalogID is the identifier of the A2UI v0.9.1 basic component catalog.
const BasicCatalogID = "https://a2ui.org/specification/v0_9_1/catalogs/basic/catalog.json"

// BasicCatalog returns the standard A2UI v0.9.1 basic catalog: the baseline set
// of components, functions and theme properties.
func BasicCatalog() *Catalog {
	funcs := []string{
		"required", "regex", "length", "numeric", "email",
		"formatString", "formatNumber", "formatCurrency", "formatDate",
		"pluralize", "openUrl", "and", "or", "not",
	}
	fnSet := make(map[string]struct{}, len(funcs))
	for _, f := range funcs {
		fnSet[f] = struct{}{}
	}

	themeProps := []string{"primaryColor", "iconUrl", "agentDisplayName"}
	themeSet := make(map[string]struct{}, len(themeProps))
	for _, p := range themeProps {
		themeSet[p] = struct{}{}
	}

	defs := []ComponentDef{
		{Name: "Text"},
		{Name: "Image"},
		{Name: "Icon"},
		{Name: "Video"},
		{Name: "AudioPlayer"},
		{Name: "Row", ChildListFields: []string{"children"}},
		{Name: "Column", ChildListFields: []string{"children"}},
		{Name: "List", ChildListFields: []string{"children"}},
		{Name: "Card", ChildFields: []string{"child"}},
		{Name: "Tabs", NestedChildFields: map[string]string{"tabs": "child"}},
		{Name: "Divider"},
		{Name: "Modal", ChildFields: []string{"child", "entryPointChild"}},
		{Name: "Button", ChildFields: []string{"child"}},
		{Name: "CheckBox", Input: true},
		{Name: "TextField", Input: true},
		{Name: "DateTimeInput", Input: true},
		{Name: "ChoicePicker", Input: true},
		{Name: "Slider", Input: true},
	}
	comps := make(map[string]ComponentDef, len(defs))
	for _, d := range defs {
		comps[d.Name] = d
	}

	return &Catalog{
		ID:              BasicCatalogID,
		Components:      comps,
		Functions:       fnSet,
		ThemeProperties: themeSet,
	}
}

// CatalogRegistry maps catalog IDs to catalogs, enabling validation against the
// catalog declared by each surface.
type CatalogRegistry struct {
	catalogs map[string]*Catalog
}

// NewCatalogRegistry returns a registry pre-populated with the basic catalog.
func NewCatalogRegistry() *CatalogRegistry {
	r := &CatalogRegistry{catalogs: map[string]*Catalog{}}
	r.Register(BasicCatalog())
	return r
}

// Register adds (or replaces) a catalog in the registry.
func (r *CatalogRegistry) Register(c *Catalog) {
	if c == nil {
		return
	}
	r.catalogs[c.ID] = c
}

// Lookup returns the catalog for the given ID, if registered.
func (r *CatalogRegistry) Lookup(id string) (*Catalog, bool) {
	c, ok := r.catalogs[id]
	return c, ok
}
