package a2ui

import "fmt"

// ValidationError describes a single structural problem with an A2UI message or
// surface tree. Its JSON form matches the protocol's standard validation error
// payload, allowing it to be returned to an LLM for self-correction.
type ValidationError struct {
	Code      string `json:"code"`
	SurfaceID string `json:"surfaceId,omitempty"`
	Path      string `json:"path,omitempty"`
	Message   string `json:"message"`
}

// Error implements the error interface.
func (e ValidationError) Error() string {
	if e.Path != "" {
		return fmt.Sprintf("%s at %s (surface %q): %s", e.Code, e.Path, e.SurfaceID, e.Message)
	}
	return fmt.Sprintf("%s (surface %q): %s", e.Code, e.SurfaceID, e.Message)
}

// ToClientError converts the validation error into the ClientError wire form
// that a client sends back to the server.
func (e ValidationError) ToClientError() ClientError {
	return ClientError{Code: e.Code, SurfaceID: e.SurfaceID, Path: e.Path, Message: e.Message}
}

func validationErr(surfaceID, path, format string, args ...any) ValidationError {
	return ValidationError{
		Code:      CodeValidationFailed,
		SurfaceID: surfaceID,
		Path:      path,
		Message:   fmt.Sprintf(format, args...),
	}
}

// ValidateEnvelope checks a single server-to-client envelope for structural
// validity against the supplied catalog. The catalog may be nil to skip
// component/function type checks. It returns all problems found.
func ValidateEnvelope(env Envelope, cat *Catalog) []ValidationError {
	var errs []ValidationError

	if env.Kind() == KindUnknown {
		return []ValidationError{validationErr("", "", "envelope contains no recognized message body")}
	}

	switch env.Kind() {
	case KindCreateSurface:
		m := env.CreateSurface
		if m.SurfaceID == "" {
			errs = append(errs, validationErr("", "/createSurface/surfaceId", "surfaceId is required"))
		}
		if m.CatalogID == "" {
			errs = append(errs, validationErr(m.SurfaceID, "/createSurface/catalogId", "catalogId is required"))
		}
	case KindUpdateComponents:
		m := env.UpdateComponents
		if m.SurfaceID == "" {
			errs = append(errs, validationErr("", "/updateComponents/surfaceId", "surfaceId is required"))
		}
		for i, c := range m.Components {
			base := fmt.Sprintf("/updateComponents/components/%d", i)
			if c.ID == "" {
				errs = append(errs, validationErr(m.SurfaceID, base+"/id", "component id is required"))
			}
			if c.Type == "" {
				errs = append(errs, validationErr(m.SurfaceID, base+"/component", "component type is required"))
				continue
			}
			if cat != nil && !cat.HasComponent(c.Type) {
				errs = append(errs, validationErr(m.SurfaceID, base+"/component", "unknown component type %q", c.Type))
			}
		}
	case KindUpdateDataModel:
		m := env.UpdateDataModel
		if m.SurfaceID == "" {
			errs = append(errs, validationErr("", "/updateDataModel/surfaceId", "surfaceId is required"))
		}
	case KindDeleteSurface:
		m := env.DeleteSurface
		if m.SurfaceID == "" {
			errs = append(errs, validationErr("", "/deleteSurface/surfaceId", "surfaceId is required"))
		}
	}
	return errs
}

// ValidateSurfaceTree checks that a fully assembled surface forms a valid
// component tree: a root must exist, every referenced child must be defined,
// component types must exist in the catalog, and there must be no cycles.
func ValidateSurfaceTree(s *Surface, cat *Catalog) []ValidationError {
	var errs []ValidationError

	if _, ok := s.Root(); !ok {
		errs = append(errs, validationErr(s.ID, "", "surface has no %q component", RootComponentID))
	}

	for id, c := range s.Components {
		if cat != nil && c.Type != "" && !cat.HasComponent(c.Type) {
			errs = append(errs, validationErr(s.ID, "/"+id, "unknown component type %q", c.Type))
		}
		for _, ref := range childRefs(c, cat) {
			if _, ok := s.Components[ref]; !ok {
				errs = append(errs, validationErr(s.ID, "/"+id, "references undefined component %q", ref))
			}
		}
	}

	errs = append(errs, detectCycles(s, cat)...)
	return errs
}

// detectCycles reports components that participate in a child-reference cycle,
// reachable from root.
func detectCycles(s *Surface, cat *Catalog) []ValidationError {
	const (
		white = 0 // unvisited
		gray  = 1 // on the current DFS stack
		black = 2 // fully explored
	)
	color := make(map[string]int, len(s.Components))
	var errs []ValidationError

	var visit func(id string)
	visit = func(id string) {
		c, ok := s.Components[id]
		if !ok {
			return
		}
		color[id] = gray
		for _, ref := range childRefs(c, cat) {
			switch color[ref] {
			case gray:
				errs = append(errs, validationErr(s.ID, "/"+id, "circular reference involving %q", ref))
			case white:
				visit(ref)
			}
		}
		color[id] = black
	}

	if _, ok := s.Root(); ok {
		visit(RootComponentID)
	}
	// Also cover components not reachable from root so cycles elsewhere surface.
	for id := range s.Components {
		if color[id] == white {
			visit(id)
		}
	}
	return errs
}
