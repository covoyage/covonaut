# Workflows & Handoffs

## Sequential Pipeline

`Pipeline` passes each step's output to the next step and stops on the first
error. `NewFuncStep` adapts a Go function, so validation, transformation, and
database access need not be represented as agents.

## Parallel Work

`Parallel` runs steps with the same input, waits for all of them, then merges
results. Result positions follow the configured step list rather than completion
order. Without a custom merge function, results are joined with a separator.

An error in one branch is reported after the branches finish; it does not undo
other branches or automatically roll back their side effects. Apply independent
deadlines and use compensation or transactions where necessary.

## Router

`Router.Route` returns a key looked up in `Router.Steps`. A missing key is an
error. Define an explicit fallback step if your application needs one.

```go
router := &workflow.Router{
    Route: func(ctx context.Context, input string) string {
        if strings.Contains(input, "code") {
            return "coder"
        }
        return "general"
    },
    Steps: map[string]workflow.Step{
        "coder": coderStep,
        "general": generalStep,
    },
}
```

These steps are application-supplied implementations. The
[offline example](./graphs#offline-example) provides a complete function-based
pipeline and router.

## Agents as Steps

`NewAgentStep` builds an agent from configuration for a step invocation; it is
not automatically a long-lived shared chat session. If your application needs
explicit lifetime and cleanup for resource-bearing extensions, implement a
function step that constructs the agent, defers `Close`, and calls `Run`.

```go
step := workflow.NewFuncStep(func(ctx context.Context, input string) (string, error) {
    child, err := agentcore.NewWithError(reviewConfig)
    if err != nil {
        return "", err
    }
    defer child.Close()
    return child.Run(ctx, input)
})
```

`RunSequentialAgents` instead accepts already constructed agents and passes
each final output to the next. The caller owns their lifetime and conversation
history. Do not concurrently reuse one agent for unrelated conversations.

## Delegate or Transfer

`HandoffConfig` names a specialist and supplies `AgentConfig` for local execution.
Use `HandoffDelegate` when the coordinator should receive a specialist's result;
use `HandoffTransfer` when control moves to the target.

Choose boundaries based on responsibility, context, tools, and permissions. A
specialist needs its own provider/model configuration; a name alone does not
create those dependencies. Delegating to a specialist does not elevate user
permissions.

The [CLI example](../examples#cli-chat) shows weather and math specialists. Use
[A2A](../integrations/a2a) for remotely hosted targets.

## Agent-as-Tool and Task Routing

`AgentAsTool` wraps a specialist configuration as a tool accepting `input`.
`AgentAsToolWithEventBus` explicitly shares a parent bus; the plain constructor
does not automatically forward all events just because it is called by a parent.
`TaskTool` selects a named `TaskOption` and wraps its task as the child's input.

```go
managedAgentTool := func(cfg agentcore.Config) *agentcore.Tool {
    tool := agentcore.AgentAsTool(cfg)
    tool.Func = func(ctx context.Context, args json.RawMessage) (any, error) {
        var params struct {
            Input string `json:"input"`
        }
        if err := json.Unmarshal(args, &params); err != nil {
            return nil, err
        }
        if params.Input == "" {
            return nil, errors.New("input must not be empty")
        }
        child, err := agentcore.NewWithError(cfg)
        if err != nil {
            return nil, err
        }
        defer child.Close()
        return child.Run(ctx, params.Input)
    }
    return tool
}
delegate := agentcore.TaskTool("delegate", []agentcore.TaskOption{
    {Name: "reviewer", Description: "Review code", Tool: managedAgentTool(reviewConfig)},
    {Name: "writer", Description: "Write a draft", Tool: managedAgentTool(writeConfig)},
})
```

The wrapper keeps `AgentAsTool`'s metadata but replaces its execution function
to check initialization and close each child. These fragments belong inside an
application function; provide the configurations and import `context`,
`encoding/json`, and `errors` as needed. Perform authorization in the outer
tool or application before delegating.

`TaskTool` calls the selected tool function directly, so child tool-level
executor hooks are not automatically re-entered. Place required policy on the
outer tool or in the child agent/host, or route explicitly through `InvokeTool`.

Current convenience adapters create child agents without an explicit per-call
`Close` in the adapter. For resource-bearing extensions, use an owned custom
wrapper that constructs the child, defers `Close`, and runs it. Do not assume
factory helpers eliminate lifetime management.

## Message Bus Delivery

`MessageBus` routes messages across topics without sharing one agent state.
Publishing is non-blocking per subscriber and drops messages when its buffer
is full. It is not durable delivery or a transactional queue. Use it only where
that loss behavior is acceptable, and manage subscription cancellation and
publisher shutdown in the host.