# Tools & Extensions

Tools connect model requests to application code. A tool supplies a name,
description, JSON Schema parameters, and a function accepting `json.RawMessage`.

## Custom Tools

The [Quick Start](./quickstart#complete-source) shows a complete custom tool.
Decode arguments into a typed struct, propagate errors, and use the supplied
context for cancellation in network or process operations.

Enable `WithValidateArguments(true)` to validate arguments against the registered
schema. Validation does not replace authorization: restrict file paths, network
destinations, and destructive actions in your tool implementation.

## Built-in Tools

```go
ext := tools.NewExtension(tools.ExtensionConfig{
    WorkingDir: "/path/to/project",
})
cfg := agentcore.NewConfig(
    agentcore.WithModel(model),
    agentcore.WithProvider(provider),
    agentcore.WithExtensions(ext),
)
```

The `tools` package includes filesystem, shell, search, browser, and code
execution operations. Pluggable operation interfaces let a host delegate
execution to remote systems or sandboxes.

File queries return paths in lexical order; grep orders matches by full path
and then line number. Output limits are applied after formatting sorts results.
The ripgrep backend sorts paths before emitting matches. The fd backend collects
all matching paths before sorting and limiting them, which costs more time and
memory for large trees. Custom `Glob` implementations that limit results
internally must choose a deterministic subset before returning it; sorting a
truncated subset cannot make its selection deterministic.

::: warning Execution boundaries
Shell allowlists and blocklists are not strong isolation. Run untrusted tools in
a sandbox and enforce permissions outside the model. Only expose tools needed
for the task.
:::

## Extensions and Hooks

### Execution Order

`Agent.InvokeTool(ctx, name, args)` uses the executor instead of calling
`Tool.Func` directly. Use it for programmatic tool calls that need the same
tool/global hooks and middleware as executor-issued calls.

The tool execution pipeline is:

1. Tool-specific before hooks.
2. Global before hooks.
3. Middleware wrapping tool execution.
4. Global after hooks.
5. Tool-specific after hooks.

A before-hook error rejects a call. After hooks receive the result and error.
An unknown tool, argument validation failure, or rejected hook is not a
successful execution. Do not invoke `GetTool(...).Func` to bypass this pipeline
when you need auditing or policy checks.

```go
result, err := agent.InvokeTool(ctx, "greet", json.RawMessage(`{"name":"Ada"}`))
if err != nil {
    return err
}
fmt.Println(result)
```

This short fragment assumes an initialized agent and surrounding function.

### Middleware and Side Effects

`TimeoutMiddleware` supplies a context deadline to the next execution function;
the tool must cooperate with cancellation. `RetryMiddleware` may execute the
same tool again. Use retries only for safe or idempotent operations, or provide
your own deduplication key for side effects such as payments or writes.

Logging hooks record tool identity and payload sizes rather than requiring you
to log full arguments. Redact sensitive values in any custom audit handler.

### Registration

Extensions contribute tools, prompts, hooks, and middleware through optional
interfaces. Implement the lifecycle methods and only the capabilities needed.
Registered extensions belong in `Config.Extensions` or
`WithExtensions(...)`.

Per-tool `Before` and `After` hooks surround execution. Global hooks and
middleware apply across tools, while lifecycle hooks cover agent execution as
well. Use these for authentication, audit logging, timeouts, and policy checks.

Other built-in extensions cover prompt caching, project rules, skills, and
[remote MCP tools](../integrations/#mcp).

`RegisterTools`, `UnregisterTools`, `ToolNames`, and `GetTool` expose the live tool
registry. Updating the registry changes available implementations, but does not
grant permissions or make model-generated arguments trustworthy.

See [extensions and lifecycle hooks](./extensions) for a complete program.

## Execution Modes

| Mode | Scheduling |
| --- | --- |
| `ModeSerial` | Calls execute serially |
| `ModeParallel` | Calls join a parallel pool unless explicitly unsafe |
| `ModeMixed` | Only explicitly safe calls join the parallel pool |

`Tool.ConcurrencySafe` is a `*bool`: nil means undeclared. Parallel mode is
fail-open for undeclared tools; mixed mode is fail-closed. Unsafe calls act as
serial barriers. Prefer mixed or serial mode for incomplete declarations.
Set positive concurrency for bounded parallelism; zero means unlimited in the
executor configuration.

## Model and User Results

`DualToolOutput` separates compact `ForLLM` content from richer `ForUser` output.
`SilentResult` suppresses display while keeping model-visible content.
`BlockCarrier` adds multimodal blocks when the provider supports them.

These are presentation choices, not confidentiality guarantees. Interfaces still
need access controls. Large outputs can be moved into an `ArtifactStore` using
`WithArtifactOffload`; this replaces model context with a retrievable reference.

See [Tool](https://pkg.go.dev/github.com/covoyage/covonaut/agentcore#Tool),
[Extension](https://pkg.go.dev/github.com/covoyage/covonaut/agentcore#Extension),
and the [tools package](https://pkg.go.dev/github.com/covoyage/covonaut/tools).