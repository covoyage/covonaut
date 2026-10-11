# Integrations

The protocols cover different boundaries and are not interchangeable.

| Integration | Boundary | Starting point |
| --- | --- | --- |
| MCP | External tools into an agent | [MCP guide](./mcp) |
| A2A | Agent discovery and remote task execution | [A2A guide](./a2a) |
| ACP | Editor/client agent sessions | [ACP guide](./acp) |
| AG-UI | Execution events to a web client | [AG-UI guide](./agui) |
| A2UI | Declarative UI structure and state | [A2UI guide](./a2ui) |

For a complete application endpoint, see [HTTP API](./http).

## MCP

`mcp.NewStdioExtension` launches a server process and discovers tools through
stdio. `mcp.NewHTTPExtension` connects through HTTP. Both expose discovered tools
through `Config.Extensions`.

Initialization can fail: check errors before registering an extension. Use
`ToolPrefix` to avoid collisions when connecting multiple servers. Stdio tools
execute with the process's permissions, so restrict working directories,
credentials, and commands appropriately.

Remote `isError` tool responses are preserved as tool output, allowing the model
to react; transport failures remain distinct. See the
[MCP API](https://pkg.go.dev/github.com/covoyage/covonaut/mcp).

## ACP

The `acp` package provides JSON-RPC communication with `AgentFactory` and
`AgentInstance` abstractions, session lifecycle management, and extensible
authentication. See the [ACP API](https://pkg.go.dev/github.com/covoyage/covonaut/acp).

## AG-UI

The `agui` package transports lifecycle, text, thinking, tool-call, state, and
custom events using SSE. A web client consumes these events to display execution
progress. The client renderer and the Go server must be hosted separately from
this static documentation site.

See the [AG-UI API](https://pkg.go.dev/github.com/covoyage/covonaut/agui).

## A2A and A2UI

[A2A](./a2a) handles remote agents and task lifecycles. Current discovery uses
`/.well-known/agent-card.json`; `/.well-known/agent.json` remains a legacy alias.
Prefer the typed `client.V1()` API for A2A 1.0 integrations.

[A2UI](./a2ui) describes surfaces and data-model updates, with bindings for A2A
and AG-UI. It does not execute arbitrary UI code or supply a complete browser
renderer; clients render components using their own widgets.