# ACP Editor Sessions

The `acp` package implements a session-oriented JSON-RPC server over line-delimited
stdio. Use it to connect an editor/client to agent instances with explicit
session, working-directory, model, and mode boundaries.

## Host Interfaces

Implement `AgentFactory` to create an `AgentInstance` for a session and report
default models/modes. An instance exposes `Run`, `Core`, `Model`, and `Mode`.
Then compose the manager and server:

```go
manager := acp.NewSessionManager(acp.SessionManagerConfig{
    AgentFactory: factory,
    SessionStore: metadataStore,
    HomeDir: stateDirectory,
})
protocolServer := acp.NewServer(acp.ServerConfig{
    SessionManager: manager,
    AgentInfo: acp.AgentInfo{Name: "my-agent", Version: "1.0.0"},
})
if err := protocolServer.Run(ctx); err != nil {
    return err
}
```

`factory`, `metadataStore`, and `stateDirectory` are host-owned dependencies.
Default I/O is stdin/stdout. Never write debug text to protocol stdout: use
stderr or the configured logger. The server cleans up sessions when it exits.

## Optional Capabilities

| Interface | Application responsibility |
| --- | --- |
| `PermissionAware` | Consult the supplied permission requester before protected tools |
| `FileSystemAware` | Route file operations through the client's filesystem |
| `Rebuildable` | Rebuild actual runtime configuration when model/mode changes |

Editor-backed files can include unsaved buffers that do not exist on disk.
Route through `ACPFileSystem` where advertised rather than assuming direct disk
reads show the editor's current content.

Without `Rebuildable`, model/mode changes update persisted metadata, not
necessarily the runtime agent configuration. Without `PermissionAware`, merely
advertising a permission request mechanism does not enforce tool authorization.

## Persistence and Authentication

ACP session metadata and agent conversation state are different stores. Configure
both if restart recovery needs both. A default/no-op metadata store is not a
durable conversation history.

`AuthProvider` supplies authentication method information. It is not by itself a
network gateway or sandbox. Protect process launch, working directories, client
capabilities, and tool execution in the host. Cancellation should propagate to
agent runs and cooperative tools.

See [ACP contracts](https://pkg.go.dev/github.com/covoyage/covonaut/acp) and the
[agent lifecycle](../guide/execution).