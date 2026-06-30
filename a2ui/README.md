# A2UI Protocol Support for Covonaut

This package implements the [A2UI (Agent-to-UI) Protocol](https://a2ui.org),
version **0.9.1** (current production release), for the Covonaut agent framework.

A2UI lets agents describe rich, interactive user interfaces *declaratively* and
stream them to clients that render the components using their own native widgets.
The protocol separates UI **structure** (components) from application **state**
(the data model), enabling safe, progressive, framework-agnostic rendering across
trust boundaries — without executing arbitrary code.

## Features

- **Full message model** — the server-to-client envelope
  (`createSurface`, `updateComponents`, `updateDataModel`, `deleteSurface`) and
  client-to-server messages (`action`, `error`) plus capability exchange.
- **Data binding** — `Dynamic` values (literal / JSON-Pointer binding / function
  call), `ChildList` (static arrays and templated lists), actions and checks.
- **Basic catalog** — the standard v0.9.1 component and function catalog, with a
  registry for custom catalogs.
- **Data-model engine** — RFC 6901 JSON Pointer get/set/remove implementing the
  protocol's upsert and array semantics (including the `-` append token).
- **Client-side surface state** — `SurfaceStore` applies envelopes, maintains the
  component adjacency list and data model, and gathers the client data model.
- **Validation** — structural validation that emits the protocol's standard
  `VALIDATION_FAILED` error format, plus full surface-tree checks (root presence,
  dangling references, cycle detection).
- **JSONL streaming** — `Encoder`/`Decoder` for the recommended JSON Lines framing.
- **Server-side builder** — fluent `Builder` and component constructors
  (`Text`, `Column`, `Button`, …) for ergonomically generating UIs.
- **Transport bindings** — for **A2A** and **AG-UI**, both already supported by
  Covonaut.

Zero external dependencies beyond the Covonaut module.

## Architecture

```
a2ui/
├── doc.go            Package overview, Version + MIME constants
├── dynamic.go        Dynamic values & FunctionCall (data binding)
├── component.go      Component, ChildList, Action, Check
├── message.go        Server→client envelope + constructors + ParseEnvelope
├── client.go         Client→server messages + capabilities
├── catalog.go        Catalog, ComponentDef, basic catalog, registry
├── datamodel.go      JSON Pointer engine (get/apply/escape)
├── surface.go        SurfaceStore + client-side surface state
├── validate.go       Structural & tree validation (VALIDATION_FAILED)
├── stream.go         JSONL Encoder/Decoder
├── builder.go        Server-side Builder + component constructors
├── binding_a2a.go    A2A transport binding (DataPart per envelope)
└── binding_agui.go   AG-UI transport binding (CUSTOM event per envelope)
```

## Quick Start

### Generate a UI (server / agent side)

```go
package main

import (
    "os"

    "github.com/covoyage/covonaut/a2ui"
)

func main() {
    envs := a2ui.NewSurface("profile", a2ui.BasicCatalogID).
        Theme(map[string]any{"primaryColor": "#00BFFF"}).
        Add(a2ui.Column("root", "name", "title")).
        Add(a2ui.Text("name", a2ui.Bind("/user/name"))).
        Add(a2ui.Text("title", "Mathematician")).
        Data("/user/name", "Ada Lovelace").
        Build()

    // Stream as JSON Lines.
    enc := a2ui.NewEncoder(os.Stdout)
    _ = enc.EncodeAll(envs)
}
```

### Consume a UI (client / renderer side)

```go
store := a2ui.NewSurfaceStore()

dec := a2ui.NewDecoder(reader)
for {
    env, err := dec.Decode()
    if err == io.EOF {
        break
    }
    if err != nil {
        log.Fatal(err)
    }
    if err := store.Apply(env); err != nil {
        log.Printf("apply: %v", err)
    }
}

surface, _ := store.Surface("profile")
root, _ := surface.Root()          // walk the adjacency list from here
name, _ := surface.Get("/user/name") // resolve a data binding
```

### Validate before rendering

```go
cat := a2ui.BasicCatalog()
for _, env := range envs {
    if errs := a2ui.ValidateEnvelope(env, cat); len(errs) > 0 {
        // Each error marshals to the protocol's VALIDATION_FAILED format,
        // ready to feed back to an LLM for self-correction.
    }
}

surface, _ := store.Surface("profile")
treeErrs := a2ui.ValidateSurfaceTree(surface, cat) // root, refs, cycles
```

## Transport Bindings

A2UI is transport-agnostic. This package provides bindings for the two transports
Covonaut already implements.

### A2A

Each A2UI envelope maps to a single A2A message `Part` (a data part tagged with
the `application/a2ui+json` MIME type):

```go
msg, _ := a2ui.EnvelopesToMessage(string(a2a.RoleAgent), envs) // → a2a.Message
envs, _ := a2ui.MessageEnvelopes(msg)                          // ← a2a.Message
```

### AG-UI

Each envelope rides on AG-UI's `CUSTOM` event channel:

```go
ev := a2ui.ToCustomEvent(env)               // → agui.CustomEvent
env, ok, _ := a2ui.FromCustomEvent(ev)      // ← agui.CustomEvent
```

## Protocol Notes

- **Adjacency list** — components are a flat list linked by ID references; exactly
  one component must have the ID `root`. Definitions may arrive in any order and
  are buffered until `root` exists (progressive rendering).
- **Data model** — per-surface state addressed by JSON Pointer. `updateDataModel`
  upserts the value at `path`; omitting the value removes the key (array elements
  become `null`, preserving length); `path` of `/` replaces the whole model.
- **`sendDataModel`** — when enabled on a surface, the client attaches that
  surface's data model to outgoing message metadata
  (`a2uiClientDataModel`). See `SurfaceStore.ClientDataModel`.
- **Versions** — this package targets v0.9.1. The v1.0 candidate adds
  client-to-server RPC (`actionResponse`) and action IDs; those are not yet
  implemented.

## References

- [A2UI Specification v0.9.1](https://a2ui.org/specification/v0.9.1-a2ui/)
- [Message Reference](https://a2ui.org/reference/messages/)
- [Transports](https://a2ui.org/concepts/transports/)
