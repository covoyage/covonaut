# Extensions & Lifecycle Hooks

## Extension Contract

An extension implements `Name() string`, `Init(context.Context, *Agent) error`,
and `Dispose() error`. There is no `BaseExtension` type in the current API.
Optional interfaces contribute tools, global hooks, middleware, prompt suffixes,
context transformations, lifecycle hooks, and event snapshots.

| Optional interface | Contribution |
| --- | --- |
| `ToolProvider` | Registered tools |
| `HookProvider` | Global before/after tool hooks |
| `MiddlewareProvider` | Tool execution wrappers |
| `SystemPromptProvider` | System prompt suffix |
| `TransformContextProvider` | Provider-facing message transformation |
| `LifecycleProvider` | Agent-loop lifecycle hook |
| `EventSnapshotProvider` | Current state for new listeners |

Initialization happens before contributions are applied. Use `NewWithError`
to detect startup failure immediately. `Agent.Close` disposes registered
extensions in reverse order. An extension that allocates resources before
returning an initialization error should clean up that partial allocation itself.

## Complete Tool Extension

This program invokes a registered tool directly. It makes no model call and
needs no provider credentials.

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "log"

    "github.com/covoyage/covonaut/agentcore"
)

type greetingExtension struct{}

func (greetingExtension) Name() string { return "greetings" }
func (greetingExtension) Init(context.Context, *agentcore.Agent) error { return nil }
func (greetingExtension) Dispose() error { return nil }
func (greetingExtension) Tools() []*agentcore.Tool {
    return []*agentcore.Tool{{
        Name: "hello", Description: "Return a greeting",
        Parameters: map[string]any{"type": "object"},
        Func: func(ctx context.Context, args json.RawMessage) (any, error) {
            return "Hello from an extension!", nil
        },
    }}
}

func main() {
    if err := run(); err != nil {
        log.Fatal(err)
    }
}

func run() error {
    agent, err := agentcore.NewWithError(agentcore.NewConfig(
        agentcore.WithExtensions(greetingExtension{}),
        agentcore.WithValidateArguments(true),
    ))
    if err != nil {
        return err
    }
    defer agent.Close()
    output, err := agent.InvokeTool(context.Background(), "hello", json.RawMessage(`{}`))
    if err != nil {
        return err
    }
    fmt.Println(output)
    return nil
}
```

## Composition Order

Extensions are registered in supplied order. Hooks and middleware append to
configuration, prompt suffixes append to the existing prompt, and context
transformations compose after the previous transformation. Design ordering
deliberately when one extension relies on another's output.

Do not share a stateful extension instance across agents unless its implementation
supports that usage. `Init` receives the owning agent, and some extensions store
that reference.

## Lifecycle Hooks

Embed `BaseLifecycleHook` and override only needed phases. Before hooks can
return an error to stop a phase. Model hooks receive request/response context;
message-persistence hooks receive the message being stored.

`LifecycleChain` calls before hooks in order and after hooks in reverse order.
Tool-batch lifecycle hooks differ from executor per-tool hooks. `InvokeTool`
uses executor hooks, not the complete agent-loop lifecycle.

`Run` calls run-level hooks. `Continue` and `Resume` enter the loop without the
same outer `BeforeAgentRun` / `AfterAgentRun` wrapper. Enforce policies at a
boundary exercised by all your application's entry points.

## Built-in Lifecycle Policies

`GuardrailHook.Validate` examines model responses. Rejected output can be fed
back for correction rather than serving as a terminal authorization decision.
`AuditHook` reports phase details; redact logged content. Rate-limit hooks
supplement rather than replace host quotas across users and processes.

Message append hooks do not wrap wholesale history replacement by compaction.
Keep a separate durable audit store instead of assuming every mutation is an
append event.