# Events, Traces & Metrics

## Event Consumers

Subscribe using `On` for one event kind or `OnAll` for every event. Both return
an unsubscribe function. A request-scoped SSE writer must unsubscribe when its
request ends, especially when the agent is reused.

```go
unsubscribe := agent.OnAll(func(event agentcore.Event) {
    fmt.Println(event.EventKind())
})
defer unsubscribe()
```

The bus dispatches asynchronously and calls handlers serially from its dispatch
goroutine. Slow handlers delay others. Publishing is non-blocking and drops
events when the buffer fills. Subscription order is not a dependency mechanism.

Keep handlers short, synchronize shared state, and avoid reentrant calls that
wait for the same bus to drain. Handler panics are recovered and reported, but
recovery does not make the handler's own state consistent.

::: warning Events are not durable history
Use saved state or checkpoints for recovery. Reconnect with a snapshot and
reconcile events; do not treat every delta as guaranteed delivery.
:::

## Streaming Attempts

Use `AttemptID` to associate deltas with a provider attempt. A `message_reset`
event means failed-attempt output must be retracted. Keep transient rendered
output separate from committed messages to avoid duplicating stale text.

Handle tool results, thinking blocks, multimodal content, and terminal failures
as distinct events. An empty text delta does not necessarily end a run.

## Tracing

Set `Config.Tracer` for correlated agent/model/tool spans. Graph and workflow
configuration accepts tracing too. `RunInfo` propagates identifiers through
context and events to connect these layers.

`NewOtelTracer` adapts your OpenTelemetry tracer. The application owns exporters,
resource attributes, sampling, and shutdown. Direct provider calls outside
agents can use `NewModelSpanMiddleware`.

## Metrics

`Config.Metrics` records agent/tool errors through runtime wiring. For model
usage, duration, and errors, wrap the provider with `NewModelMetricsMiddleware`;
setting `Metrics` alone is not that wrapper.

```go
metrics := agentcore.NewOtelMetrics(meter)
instrumented := agentcore.NewModelMetricsMiddleware(metrics)(provider)
cfg := agentcore.NewConfig(
    agentcore.WithModel(model),
    agentcore.WithProvider(instrumented),
)
cfg.Metrics = metrics
cfg.Tracer = agentcore.NewOtelTracer(tracer)
```

Here `meter` and `tracer` come from your OpenTelemetry providers. Instrument
failover providers too, and avoid wrapping a provider twice if it double-counts.

Retries count additional invocations. Streaming can fail after output starts,
so track terminal errors as well as connection errors. Usage may be absent when
the provider cannot report it.

`TokenUsage.PromptTokens` includes cached input. `CacheReadTokens` and
`CacheWriteTokens` are input breakdowns, not additional tokens to add to
`TotalTokens`. Unsupported or unreported details remain zero. Anthropic and
Vertex input usage is normalized to include cache reads and writes. Within a
request, `TokenUsage.Merge` takes the maximum of cumulative stream values;
session usage sums separate requests. Merge must not be used to sum requests
or provider-specific incremental counters.

OpenTelemetry records cache reads and writes separately in
`agentcore.model.cache.token.usage`, labeled by `agentcore.cache.operation`,
without adding them again to `gen_ai.client.token.usage`.

## Operational Data

Track latency, token usage, retry/failover rates, tool failures, compaction,
queue pressure, and work awaiting review. Avoid high-cardinality metric labels
such as full prompts or arbitrary thread IDs. Treat traces and audit payloads
as potentially sensitive user data.