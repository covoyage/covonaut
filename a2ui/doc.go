// Package a2ui implements the A2UI (Agent-to-UI) protocol, version 0.9.1, as
// specified at https://a2ui.org.
//
// A2UI lets agents describe rich, interactive user interfaces declaratively and
// stream them to clients that render the components with their own native
// widgets. The protocol separates UI structure (components) from application
// state (the data model) and is transport-agnostic.
//
// This package provides:
//
//   - Full message types for the server-to-client envelope
//     (createSurface, updateComponents, updateDataModel, deleteSurface) and the
//     client-to-server messages (action, error) plus capability exchange.
//   - A data-binding model (Dynamic values, function calls, child lists).
//   - The basic component catalog and a registry for custom catalogs.
//   - A JSON Pointer (RFC 6901) data-model engine implementing the protocol's
//     upsert/remove semantics.
//   - Client-side surface state management with adjacency-list tree resolution.
//   - Structural validation that emits the protocol's standard error format.
//   - JSONL stream encoding/decoding.
//   - Server-side builder helpers for constructing UIs ergonomically.
//   - Transport bindings for A2A and AG-UI, both already supported by covonaut.
//
// The package has no external dependencies beyond the covonaut module itself.
package a2ui

// Version is the A2UI protocol version implemented by this package. Every
// server-to-client envelope carries this value in its "version" field.
const Version = "v0.9.1"

// MIMEType is the canonical media type for A2UI messages, standardized in
// v0.9.1. Transports that carry typed payloads (e.g. A2A data parts) should use
// this value.
const MIMEType = "application/a2ui+json"
