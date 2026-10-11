# Context Engines & Compaction

Context management fits useful history and tool definitions into a model
budget. It is not durable memory, retrieval, or lossless storage.

## Budget the Request

`ContextWindow` is the model's context capacity. Account for messages, exposed
tool schemas, and response reservation. Estimates are not a universal tokenizer
guarantee; provider-side limits still apply.

```go
cfg := agentcore.NewConfig(
    agentcore.WithModel(model),
    agentcore.WithProvider(provider),
    agentcore.WithContextWindow(128000),
    agentcore.WithReserveTokens(16000),
    agentcore.WithKeepRecentTokens(4000),
    agentcore.WithCompressionThreshold(0.8),
    agentcore.WithProtectFirstN(3),
    agentcore.WithStructuredCompaction(true),
)
```

Adjust these illustrative budgets to your model. A positive context window
enables management. `AutoCompactTokenLimit` supplies an absolute trigger instead
of the fractional threshold for the compressor.

## Compressor and Truncate

| Engine | Behavior | Trade-off |
| --- | --- | --- |
| `compressor` | Summarize older history with a model | Additional cost and latency; lossy summary |
| `truncate` | Drop middle history, retaining protected head and recent tail | No summary call; removed information is lost |

Set `CompactionConfig.Engine` to choose a built-in engine. The truncate engine's
trigger currently uses an estimated budget with a quarter-window reservation;
its threshold diagnostics should not be assumed to implement the compressor's
fractional-trigger policy.

Structured compaction organizes a summary rather than making it lossless.
`ProtectFirstN` protects early non-system messages and `KeepRecentTokens`
protects recent context. Tool-call/result boundaries are repaired so the next
request remains structurally valid.

## Separate Compression Model

A compression model/provider can reduce summarization cost or specialize it.
It must still handle the history being compressed. Configure its credentials
and endpoint explicitly where needed.

Anti-thrashing logic avoids repeated low-benefit compression. If retained
content and tool schemas already exceed the budget, reduce protected content,
tool exposure, or result sizes instead of repeatedly summarizing.

## Custom Engines and Diagnostics

`ContextEngine` defines session lifecycle, usage updates, compaction decisions,
compression, optional tool schemas, and statistics. Supply a prebuilt engine
through `WithCustomContextEngine`, or register a factory and explicitly switch
the active instance. Registering a factory does not recreate the active engine.

`ContextEngineStats` reports diagnostics. `ForceCompact` requests compression;
`ResetContextEngine` clears session-specific state. Session end occurs when the
agent is closed, not at every model turn.

See [ContextEngine](https://pkg.go.dev/github.com/covoyage/covonaut/agentcore#ContextEngine)
and [CompactionConfig](https://pkg.go.dev/github.com/covoyage/covonaut/agentcore#CompactionConfig).