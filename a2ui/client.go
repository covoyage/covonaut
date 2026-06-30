package a2ui

import "time"

// ClientMessage is a message sent from the client (renderer) back to the server
// (agent). It carries exactly one body.
type ClientMessage struct {
	Action *ClientAction `json:"action,omitempty"`
	Error  *ClientError  `json:"error,omitempty"`
}

// ClientAction is dispatched when the user interacts with a component that
// defines a server Action.
type ClientAction struct {
	// Name is the action name declared in the component's action.event.
	Name string `json:"name"`
	// SurfaceID is the surface where the action originated.
	SurfaceID string `json:"surfaceId"`
	// SourceComponentID is the component that triggered the action.
	SourceComponentID string `json:"sourceComponentId"`
	// Timestamp is an ISO 8601 timestamp of when the action occurred.
	Timestamp string `json:"timestamp"`
	// Context carries the resolved context payload from the component's action.
	Context map[string]any `json:"context"`
}

// NewClientAction builds a ClientAction stamped with the current time in
// RFC 3339 (ISO 8601) format.
func NewClientAction(name, surfaceID, sourceComponentID string, context map[string]any) *ClientAction {
	if context == nil {
		context = map[string]any{}
	}
	return &ClientAction{
		Name:              name,
		SurfaceID:         surfaceID,
		SourceComponentID: sourceComponentID,
		Timestamp:         time.Now().UTC().Format(time.RFC3339),
		Context:           context,
	}
}

// CodeValidationFailed is the required error code for messages that fail
// schema/structural validation.
const CodeValidationFailed = "VALIDATION_FAILED"

// ClientError reports a client-side error to the server. For validation
// failures Code must be CodeValidationFailed and Path must point at the field
// that failed.
type ClientError struct {
	// Code is a machine-readable error code (e.g. CodeValidationFailed).
	Code string `json:"code"`
	// SurfaceID is the surface where the error occurred, if applicable.
	SurfaceID string `json:"surfaceId,omitempty"`
	// Path is a JSON Pointer to the offending field, if applicable.
	Path string `json:"path,omitempty"`
	// Message is a short human-readable description.
	Message string `json:"message"`
}

// Error implements the error interface.
func (e ClientError) Error() string {
	if e.Path != "" {
		return e.Code + " at " + e.Path + ": " + e.Message
	}
	return e.Code + ": " + e.Message
}

// ServerCapabilities advertises what an agent can do. It is exchanged via the
// transport's metadata facility (e.g. an A2A AgentCard or MCP initialization).
type ServerCapabilities struct {
	// SupportedCatalogIDs lists the catalog URIs the server can generate UI for.
	SupportedCatalogIDs []string `json:"supportedCatalogIds,omitempty"`
	// AcceptsInlineCatalogs reports whether the server accepts client-provided
	// inline catalog definitions.
	AcceptsInlineCatalogs bool `json:"acceptsInlineCatalogs,omitempty"`
}

// ClientCapabilities advertises what catalogs a client can render. It is placed
// in the metadata of every client-to-server message.
type ClientCapabilities struct {
	// SupportedCatalogIDs lists the catalog URIs the client supports.
	SupportedCatalogIDs []string `json:"supportedCatalogIds"`
	// InlineCatalogs holds catalog definitions provided directly by the client.
	InlineCatalogs []map[string]any `json:"inlineCatalogs,omitempty"`
}

// ClientDataModelPayload is the structure placed in transport metadata when a
// surface has sendDataModel enabled. It maps surface IDs to their current data
// models.
type ClientDataModelPayload struct {
	Surfaces map[string]any `json:"surfaces"`
}

// Metadata keys used in transport metadata to carry A2UI capability and data
// model payloads (per the A2A binding).
const (
	MetadataClientCapabilities = "a2uiClientCapabilities"
	MetadataClientDataModel    = "a2uiClientDataModel"
	MetadataServerCapabilities = "a2uiServerCapabilities"
)
