# Streaming & Reliability

## Streaming Events

Enable provider streaming with `WithStreaming(true)`. Subscribe before running
the agent:

```go
unsubscribe := agent.On(agentcore.EventMessageDelta, func(event agentcore.Event) {
    delta := event.(*agentcore.MessageDeltaEvent)
    fmt.Print(delta.Content)
})
defer unsubscribe()
```

The event bus also reports tool execution, run lifecycle, and other runtime
changes. Keep handlers fast and synchronize access to shared application state.
Use the [event API](https://pkg.go.dev/github.com/covoyage/covonaut/agentcore#Event)
for payload types.

Streaming deltas carry an `AttemptID`. On retry or failover, a `message_reset`
event retracts partial output from a failed attempt. Consumers must handle this
event rather than blindly appending every delta to the transcript.

## Context Management

Set `CompactionConfig.ContextWindow` to enable context-budget management.
`CompressionThreshold` controls when compression starts; `ReserveTokens` leaves
room for the response and `KeepRecentTokens` protects recent history.
Structured compaction and alternate context engines are available for more
specialized workloads.

## Retry and Failover

`RetryConfig` controls retry limits and backoff. `ModelFailoverConfig` supplies an
ordered list of alternate provider/model targets. Keep retry budgets bounded and
remember that repeated model calls can incur additional charges.

Tool selection can limit the definitions exposed to each model call while
registered tools remain available to the executor. Artifact offloading moves
large results into a retrievable store instead of repeatedly embedding them in
the conversation.

### Configure Retry and Failover

```go
cfg.RetryConfig = &agentcore.RetryConfig{
    MaxRetries: 3, BaseDelayMs: 1000, MaxDelayMs: 15000,
}
cfg.Failover = &agentcore.ModelFailoverConfig{
    Targets: []agentcore.ModelTarget{{
        Name: "backup", Model: backupModel, Provider: backupProvider,
    }},
}
```

Retries operate on provider calls; tool `RetryMiddleware` is a different policy.
Default failover follows retryable failures, not every error. Cancellation,
interrupts, and repetition-loop errors do not enter the default failover path.
`ShouldFailover` and `SelectTarget` customize policy. Alternate providers must
support your tools, message blocks, and requested output format.

Context overflow is handled separately through compaction when configured.
Repetition recovery injects bounded corrective prompts for repeated text, tool
calls, or a stream-repetition error reported by middleware. It is not an
unbounded same-request retry policy.

## Tool Selection

```go
cfg.ToolSelection = &agentcore.ToolSelectionConfig{
    Selector: agentcore.KeywordToolSelector{IncludeUnmatched: true},
    MaxVisible: 12,
    AlwaysVisible: []string{"help"},
}
```

Register the named always-visible tools. `KeywordToolSelector` ranks recent-user
text against tool definitions; `EmbeddingToolSelector` uses a supplied embedding
function with caching, a score threshold, and optional fallback. The application
owns embedding costs and provider errors.

Selection filters schemas sent to the model, not executor permissions. Registered
tools remain executable by name; enforce authorization separately. Test both
tool discovery quality and what happens when a required tool is omitted.

## Artifact Offloading

```go
cfg.ArtifactOffload = &agentcore.ArtifactOffloadConfig{
    Store: agentcore.NewMemoryArtifactStore(),
    MinBytes: 50 * 1024,
    ExcludeTools: []string{"small_status"},
}
```

Large successful tool results can be stored with metadata and replaced in model
context by an `artifact://` reference. The runtime ensures `artifact_read` is
available when offloading is configured. Error results and excluded tools are
not offloaded. If storing fails, the original result remains in context.

The memory store has no durable retention or automatic eviction policy. A host
must enforce tenant access, size limits, retention, and cleanup in a production
store. Reading the complete artifact can put its full content back into context;
offloading is not a guarantee that every later request will stay small.

## Observability

Set `Config.Tracer` and `Config.Metrics` for distributed traces and runtime
metrics. OpenTelemetry adapters are included. Run information correlates agent,
model, tool, graph, and workflow activity across contexts and events.

Use [NewOtelTracer](https://pkg.go.dev/github.com/covoyage/covonaut/agentcore#NewOtelTracer)
and [NewOtelMetrics](https://pkg.go.dev/github.com/covoyage/covonaut/agentcore#NewOtelMetrics)
with your application's OpenTelemetry providers. Telemetry may contain sensitive
attributes; configure collection and retention accordingly.

See [events, traces, and metrics](./observability) for provider instrumentation
and event delivery guarantees.