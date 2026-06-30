package a2ui

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// Envelope is a single server-to-client A2UI message. Every message carries a
// Version and contains exactly one of the four message bodies.
type Envelope struct {
	Version          string            `json:"version,omitempty"`
	CreateSurface    *CreateSurface    `json:"createSurface,omitempty"`
	UpdateComponents *UpdateComponents `json:"updateComponents,omitempty"`
	UpdateDataModel  *UpdateDataModel  `json:"updateDataModel,omitempty"`
	DeleteSurface    *DeleteSurface    `json:"deleteSurface,omitempty"`
}

// MessageKind identifies which body an Envelope carries.
type MessageKind string

const (
	KindCreateSurface    MessageKind = "createSurface"
	KindUpdateComponents MessageKind = "updateComponents"
	KindUpdateDataModel  MessageKind = "updateDataModel"
	KindDeleteSurface    MessageKind = "deleteSurface"
	KindUnknown          MessageKind = ""
)

// Kind reports which message body the envelope carries. It returns KindUnknown
// if the envelope is empty or malformed.
func (e Envelope) Kind() MessageKind {
	switch {
	case e.CreateSurface != nil:
		return KindCreateSurface
	case e.UpdateComponents != nil:
		return KindUpdateComponents
	case e.UpdateDataModel != nil:
		return KindUpdateDataModel
	case e.DeleteSurface != nil:
		return KindDeleteSurface
	default:
		return KindUnknown
	}
}

// SurfaceID returns the surface ID targeted by the envelope, or "" if none.
func (e Envelope) SurfaceID() string {
	switch e.Kind() {
	case KindCreateSurface:
		return e.CreateSurface.SurfaceID
	case KindUpdateComponents:
		return e.UpdateComponents.SurfaceID
	case KindUpdateDataModel:
		return e.UpdateDataModel.SurfaceID
	case KindDeleteSurface:
		return e.DeleteSurface.SurfaceID
	default:
		return ""
	}
}

// CreateSurface signals the client to create a new surface and begin rendering
// it. A surface must be created before any updateComponents or updateDataModel
// message can target it.
type CreateSurface struct {
	// SurfaceID is the unique identifier for the surface.
	SurfaceID string `json:"surfaceId"`
	// CatalogID uniquely identifies the component/function catalog used by the
	// surface (recommended to be a domain-prefixed URI).
	CatalogID string `json:"catalogId"`
	// Theme holds optional theme parameters defined by the catalog.
	Theme map[string]any `json:"theme,omitempty"`
	// SendDataModel, when true, instructs the client to attach the surface's
	// full data model to the metadata of every message it sends to the server.
	SendDataModel bool `json:"sendDataModel,omitempty"`
}

// UpdateComponents adds or updates a flat list of components within a surface.
// Sending a component whose ID already exists updates it in place.
type UpdateComponents struct {
	SurfaceID  string      `json:"surfaceId"`
	Components []Component `json:"components"`
}

// UpdateDataModel inserts, replaces or removes a value in a surface's data
// model. The value at Path is replaced with Value. If Path is empty or "/", the
// entire data model is replaced. If Value is absent (ValueSet false), the key at
// Path is removed.
type UpdateDataModel struct {
	// SurfaceID is the surface whose data model is updated.
	SurfaceID string
	// Path is a JSON Pointer to the location to update. Empty means root.
	Path string
	// Value is the new value at Path. Only meaningful when ValueSet is true.
	Value any
	// ValueSet distinguishes "value present" (even if null) from "value
	// omitted" (which means remove the key at Path).
	ValueSet bool
}

// SetValue records a value to assign at the update's path and returns the
// update for chaining.
func (u *UpdateDataModel) SetValue(v any) *UpdateDataModel {
	u.Value = v
	u.ValueSet = true
	return u
}

// MarshalJSON implements json.Marshaler, omitting "value" when it was never set
// (the protocol's removal semantics).
func (u UpdateDataModel) MarshalJSON() ([]byte, error) {
	m := map[string]any{"surfaceId": u.SurfaceID}
	if u.Path != "" {
		m["path"] = u.Path
	}
	if u.ValueSet {
		m["value"] = u.Value
	}
	return json.Marshal(m)
}

// UnmarshalJSON implements json.Unmarshaler, tracking whether "value" was
// present in the source JSON.
func (u *UpdateDataModel) UnmarshalJSON(data []byte) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	*u = UpdateDataModel{}
	if raw, ok := m["surfaceId"]; ok {
		if err := json.Unmarshal(raw, &u.SurfaceID); err != nil {
			return err
		}
	}
	if raw, ok := m["path"]; ok {
		if err := json.Unmarshal(raw, &u.Path); err != nil {
			return err
		}
	}
	if raw, ok := m["value"]; ok {
		var v any
		_ = json.Unmarshal(raw, &v) // always succeeds when the outer json.Unmarshal succeeded
		u.Value = v
		u.ValueSet = true
	}
	return nil
}

// DeleteSurface removes a surface and all its components and data from the UI.
type DeleteSurface struct {
	SurfaceID string `json:"surfaceId"`
}

// ---------------------------------------------------------------------------
// Envelope constructors
// ---------------------------------------------------------------------------

// NewCreateSurface builds a createSurface envelope.
func NewCreateSurface(surfaceID, catalogID string) Envelope {
	return Envelope{
		Version:       Version,
		CreateSurface: &CreateSurface{SurfaceID: surfaceID, CatalogID: catalogID},
	}
}

// NewUpdateComponents builds an updateComponents envelope.
func NewUpdateComponents(surfaceID string, components ...Component) Envelope {
	return Envelope{
		Version:          Version,
		UpdateComponents: &UpdateComponents{SurfaceID: surfaceID, Components: components},
	}
}

// NewUpdateDataModel builds an updateDataModel envelope that sets value at path.
func NewUpdateDataModel(surfaceID, path string, value any) Envelope {
	return Envelope{
		Version:         Version,
		UpdateDataModel: &UpdateDataModel{SurfaceID: surfaceID, Path: path, Value: value, ValueSet: true},
	}
}

// NewRemoveDataModel builds an updateDataModel envelope that removes the key at
// path (no value sent).
func NewRemoveDataModel(surfaceID, path string) Envelope {
	return Envelope{
		Version:         Version,
		UpdateDataModel: &UpdateDataModel{SurfaceID: surfaceID, Path: path},
	}
}

// NewDeleteSurface builds a deleteSurface envelope.
func NewDeleteSurface(surfaceID string) Envelope {
	return Envelope{
		Version:       Version,
		DeleteSurface: &DeleteSurface{SurfaceID: surfaceID},
	}
}

// ErrMultipleBodies indicates an envelope carried more than one message body.
var ErrMultipleBodies = errors.New("a2ui: envelope must contain exactly one message body")

// ErrNoBody indicates an envelope carried no recognized message body.
var ErrNoBody = errors.New("a2ui: envelope contains no message body")

// ParseEnvelope decodes a single A2UI envelope from JSON and verifies it carries
// exactly one message body.
func ParseEnvelope(data []byte) (Envelope, error) {
	var e Envelope
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&e); err != nil {
		return Envelope{}, fmt.Errorf("a2ui: decode envelope: %w", err)
	}
	n := 0
	if e.CreateSurface != nil {
		n++
	}
	if e.UpdateComponents != nil {
		n++
	}
	if e.UpdateDataModel != nil {
		n++
	}
	if e.DeleteSurface != nil {
		n++
	}
	switch {
	case n == 0:
		return Envelope{}, ErrNoBody
	case n > 1:
		return Envelope{}, ErrMultipleBodies
	}
	return e, nil
}
