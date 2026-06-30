package a2ui

import (
	"errors"
	"fmt"
)

// RootComponentID is the reserved ID of the component that serves as the root of
// a surface's component tree.
const RootComponentID = "root"

// Surface holds the client-side state of a single A2UI surface: its
// configuration, the flat map of components received so far, and the current
// data model.
type Surface struct {
	ID            string
	CatalogID     string
	Theme         map[string]any
	SendDataModel bool
	// Components maps component ID to its definition (adjacency list).
	Components map[string]Component
	// DataModel is the surface's current application state.
	DataModel any
}

// Root returns the surface's root component, if it has been defined yet.
func (s *Surface) Root() (Component, bool) {
	c, ok := s.Components[RootComponentID]
	return c, ok
}

// Get resolves a data-binding path against the surface's data model.
func (s *Surface) Get(path string) (any, bool) {
	return GetData(s.DataModel, path)
}

// Errors returned by SurfaceStore operations.
var (
	// ErrSurfaceExists is returned when createSurface targets an existing
	// surface that has not been deleted.
	ErrSurfaceExists = errors.New("a2ui: surface already exists")
	// ErrSurfaceNotFound is returned when a message targets a surface that has
	// not been created.
	ErrSurfaceNotFound = errors.New("a2ui: surface not found")
)

// SurfaceStore maintains the set of live surfaces on the client side and applies
// incoming server-to-client envelopes to them. It is not safe for concurrent
// use; guard it externally if shared across goroutines.
type SurfaceStore struct {
	surfaces map[string]*Surface
}

// NewSurfaceStore returns an empty surface store.
func NewSurfaceStore() *SurfaceStore {
	return &SurfaceStore{surfaces: map[string]*Surface{}}
}

// Surfaces returns a copy of the current set of surfaces keyed by ID.
func (s *SurfaceStore) Surfaces() map[string]*Surface {
	m := make(map[string]*Surface, len(s.surfaces))
	for k, v := range s.surfaces {
		m[k] = v
	}
	return m
}

// Surface returns the surface with the given ID, if present.
func (s *SurfaceStore) Surface(id string) (*Surface, bool) {
	srf, ok := s.surfaces[id]
	return srf, ok
}

// Apply processes a single server-to-client envelope, mutating the store's
// surface state according to the protocol's semantics.
func (s *SurfaceStore) Apply(env Envelope) error {
	switch env.Kind() {
	case KindCreateSurface:
		return s.applyCreate(env.CreateSurface)
	case KindUpdateComponents:
		return s.applyComponents(env.UpdateComponents)
	case KindUpdateDataModel:
		return s.applyDataModel(env.UpdateDataModel)
	case KindDeleteSurface:
		s.applyDelete(env.DeleteSurface)
		return nil
	default:
		return ErrNoBody
	}
}

func (s *SurfaceStore) applyCreate(m *CreateSurface) error {
	if _, exists := s.surfaces[m.SurfaceID]; exists {
		return fmt.Errorf("%w: %q", ErrSurfaceExists, m.SurfaceID)
	}
	s.surfaces[m.SurfaceID] = &Surface{
		ID:            m.SurfaceID,
		CatalogID:     m.CatalogID,
		Theme:         m.Theme,
		SendDataModel: m.SendDataModel,
		Components:    map[string]Component{},
		DataModel:     map[string]any{},
	}
	return nil
}

func (s *SurfaceStore) applyComponents(m *UpdateComponents) error {
	srf, ok := s.surfaces[m.SurfaceID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrSurfaceNotFound, m.SurfaceID)
	}
	for _, c := range m.Components {
		srf.Components[c.ID] = c
	}
	return nil
}

func (s *SurfaceStore) applyDataModel(m *UpdateDataModel) error {
	srf, ok := s.surfaces[m.SurfaceID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrSurfaceNotFound, m.SurfaceID)
	}
	updated, err := ApplyUpdate(srf.DataModel, m.Path, m.Value, m.ValueSet)
	if err != nil {
		return err
	}
	srf.DataModel = updated
	return nil
}

func (s *SurfaceStore) applyDelete(m *DeleteSurface) {
	// Deleting a non-existent surface is a no-op per spec.
	delete(s.surfaces, m.SurfaceID)
}

// ClientDataModel collects the data models of all surfaces with sendDataModel
// enabled into the payload that the client attaches to its outgoing messages.
func (s *SurfaceStore) ClientDataModel() ClientDataModelPayload {
	surfaces := map[string]any{}
	for id, srf := range s.surfaces {
		if srf.SendDataModel {
			surfaces[id] = srf.DataModel
		}
	}
	return ClientDataModelPayload{Surfaces: surfaces}
}

// childRefs returns the IDs of every component referenced as a child by c,
// using the catalog to know which properties are structural references.
func childRefs(c Component, cat *Catalog) []string {
	var refs []string

	def, known := cat.Components[c.Type]
	singleFields := []string{"child"}
	listFields := []string{"children"}
	nestedFields := map[string]string{}
	if known {
		if len(def.ChildFields) > 0 {
			singleFields = def.ChildFields
		}
		if len(def.ChildListFields) > 0 {
			listFields = def.ChildListFields
		}
		if len(def.NestedChildFields) > 0 {
			nestedFields = def.NestedChildFields
		}
	}

	for _, f := range singleFields {
		if id, ok := c.Props[f].(string); ok && id != "" {
			refs = append(refs, id)
		}
	}
	for _, f := range listFields {
		refs = append(refs, childListRefs(c.Props[f])...)
	}
	for prop, key := range nestedFields {
		if arr, ok := c.Props[prop].([]any); ok {
			for _, item := range arr {
				if obj, ok := item.(map[string]any); ok {
					if id, ok := obj[key].(string); ok && id != "" {
						refs = append(refs, id)
					}
				}
			}
		}
	}
	return refs
}

// childListRefs extracts referenced component IDs from a ChildList-shaped value
// in its decoded (any) form. A static array yields its string elements; a
// template object yields its componentId.
func childListRefs(v any) []string {
	switch cl := v.(type) {
	case []any:
		var refs []string
		for _, e := range cl {
			if id, ok := e.(string); ok && id != "" {
				refs = append(refs, id)
			}
		}
		return refs
	case []string:
		return cl
	case map[string]any:
		if id, ok := cl["componentId"].(string); ok && id != "" {
			return []string{id}
		}
	case ChildList:
		if cl.Template != nil {
			return []string{cl.Template.ComponentID}
		}
		return cl.Static
	}
	return nil
}
