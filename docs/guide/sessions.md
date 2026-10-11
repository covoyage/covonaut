# Sessions & HTTP

## Choose Persistence

| Store | Use case |
| --- | --- |
| `store.NewFileStore` | Agent state snapshots keyed by ID |
| `session.NewFileStore` | Append-only JSONL conversation trees with branching |
| `session.NewAgentStore` | Adapt JSONL sessions to the agent state interface |

For direct agent use, configure `Config.Store`, then call `SaveState` and
`LoadState` with the same key. Checkpoints are a separate mechanism for durable
execution recovery; configure `Config.Checkpoint` when required.

Session stores retain a tree of entries. Branching creates an alternate
conversation path without discarding the original history. `MessagesOnPath`
reconstructs the selected path with compaction information.

## HTTP Server

See the [complete HTTP server](../integrations/http) for persistence, bearer
authentication, graceful shutdown, and curl requests.

`server.New(cfg)` exposes an agent over HTTP/SSE. Mount its `Handler()` in your
HTTP server, or call `ListenAndServe` for a standalone server.

With `Config.Store`, `/api/chat` restores and saves state for a supplied
`thread_id`. A session-backed `AgentStore` can create a thread automatically when
the client omits that ID.

```json
{
  "message": "Summarize our conversation",
  "thread_id": "thread-123",
  "stream": false
}
```

## Thread Endpoints

The following require the session-backed thread store:

| Method | Endpoint | Purpose |
| --- | --- | --- |
| POST / GET | `/api/threads` | Create / list threads |
| GET / DELETE | `/api/threads/{key}` | Fetch / delete a thread |
| POST | `/api/threads/{key}/branch` | Branch at the current leaf or an `entry_id` |
| GET / PUT | `/api/threads/{key}/config` | Read / persist call configuration |
| GET / PUT | `/api/threads/{key}/thinking` | Read / persist thinking settings |

Request-level model, response-format, and thinking values override persisted
thread configuration, which overrides server defaults. Generic state endpoints
remain available for other `agentcore.Store` implementations.

::: warning Hosting
GitHub Pages hosts this documentation, not the Go server. Deploy the server
separately and enforce authentication, authorization, request limits, appropriate
CORS, and tool isolation before exposing it publicly.
:::

See [session](https://pkg.go.dev/github.com/covoyage/covonaut/session),
[store](https://pkg.go.dev/github.com/covoyage/covonaut/store), and
[server](https://pkg.go.dev/github.com/covoyage/covonaut/server) APIs.

## Branch a Conversation

```go
manager, err := sessions.Create(ctx, session.CreateOptions{Cwd: "/workspace"})
if err != nil {
  return err
}
if err := manager.AppendMessage(ctx, agentcore.Message{Role: agentcore.RoleUser, Content: "Plan A"}); err != nil {
  return err
}
branchPoint := manager.LeafID()
if err := manager.AppendMessage(ctx, agentcore.Message{Role: agentcore.RoleAssistant, Content: "First plan"}); err != nil {
  return err
}
if err := manager.Branch(branchPoint); err != nil {
  return err
}
if err := manager.AppendMessage(ctx, agentcore.Message{Role: agentcore.RoleUser, Content: "Try a different plan"}); err != nil {
  return err
}
messages := manager.MessagesOnPath()
```

Here `sessions` is a `session.FileStore`. Branching moves the leaf cursor without
deleting the other branch. `Entries`, `GetTree`, `Leaves`, and `Stats` support
inspection; labels and session names add navigation metadata.

`MessagesOnPath` reconstructs the selected path and accounts for compaction and
branch summaries. Do not replay every JSONL message as one linear conversation.
The session file format has its own version, separate from agent snapshot
versions. Use the package loader rather than editing persisted JSONL ad hoc.

## Storage Policy

Local file stores are useful for one host, not automatic distributed storage.
Decide ownership, file permissions, backups, locking across processes, retention,
and deletion policy before sharing them between services. Persisted history may
contain credentials, tool outputs, or images; treat it as user data.