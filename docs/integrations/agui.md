# AG-UI Event Streaming

AG-UI transports execution events to a web client. It does not provide a browser
application or host the Go server on GitHub Pages.

## Mount a Handler

```go
http.Handle("/agent", agui.NewHandler(cfg))
```

The handler accepts GET for capabilities and POST for a run. Wrap it with host
authentication, authorization, body-size limits, and request budgets. SSE needs
an `http.Flusher`; middleware must preserve that capability.

## Request and Response

```json
{
  "threadId": "conversation-123",
  "runId": "run-456",
  "messages": [
    {"id": "user-1", "role": "user", "content": "Hello"}
  ]
}
```

Use the exported `RunAgentInput` contract for additional fields. The current
handler runs the final user message; do not assume an arbitrary client-supplied
transcript replaces persisted server history. Configure a store when the thread
must continue across requests.

The response is SSE covering run lifecycle, text/thinking, tools, state, and
custom events. Read terminal run/error events, not only text deltas. A successful
HTTP connection does not prove that the agent run succeeded.

## Snapshots and Reconnection

When saved thread state is available, the handler can send a messages snapshot.
Client state input can be emitted as a state snapshot. Distinguish these
application-state snapshots from durable server checkpoints.

The underlying event bus can drop buffered events. For robust reconnects,
reconcile against persisted history and treat streamed text as transient until
committed. Handle failed-attempt reset semantics rather than appending duplicate
partial responses.

## Resume and Client Tools

Resume input is currently translated into steering messages. It is not equivalent
to directly calling `Agent.Resume` after a host-validated approval. Client-provided
tool/state fields do not automatically register executable server tools or map
every call configuration option. Implement those boundaries explicitly in the
host if your workflow needs them.

## A2UI Binding

[A2UI](./a2ui) surface updates can be carried as AG-UI custom events. The client
needs a catalog-aware renderer and data binding implementation; receiving the
event does not render a component automatically.

See [AG-UI](https://pkg.go.dev/github.com/covoyage/covonaut/agui) and
[event semantics](../guide/observability).