# Runnables & Streams

`Runnable[I, O]` is a shared data-flow interface, not another agent loop.
It lets ordinary Go functions and streaming components participate in a common
pipeline without depending on a model.

## Four Modes

| Mode | Input | Output |
| --- | --- | --- |
| `Invoke` | One `I` | One `O` |
| `Stream` | One `I` | `StreamReader[O]` |
| `Collect` | `StreamReader[I]` | One `O` |
| `Transform` | `StreamReader[I]` | `StreamReader[O]` |

Implement the mode that matches your component, using `NewInvokeRunnable`,
`NewStreamRunnable`, `NewCollectRunnable`, or `NewTransformRunnable`. The
constructor derives the other modes, but those conversions have specific
semantics.

## Derived Modes Are Not Automatic Concatenation

- Invoke-backed components stream one result; derived Collect consumes the
  input stream and invokes the component on its **last** item.
- Stream-backed components derive Invoke by consuming the output stream and
  returning its **last** item.
- Collect-backed components wrap single inputs and outputs as one-item streams.
- Transform-backed components wrap single inputs and consume the final output
  item when a single result is requested.

If your stream emits text deltas, use `CollectString` to join chunks. If it emits
successive full snapshots, taking the last value may be exactly what you want.
If you need a reduction, implement `Collect` explicitly rather than assuming a
derived mode accumulates every value.

The [offline orchestration example](./graphs#offline-example) prints the
difference between a last-item result (`"!"`) and joined text (`"hello!"`).

## Stream Lifecycle

`Send` returns false when a consumer no longer needs data. Producers should
stop rather than continue generating unused results. Close a producer's stream
on completion and use `SetError` for a terminal failure. Consumers should check
`Err` after exhaustion and close or cancel streams they stop reading early.

```go
defer stream.Close()
for {
    item, ok := stream.Recv()
    if !ok {
        break
    }
    consume(item)
}
if err := stream.Err(); err != nil {
    return err
}
```

Buffer capacity provides bounded decoupling, not unlimited storage. Keep
blocking work outside the producer's send loop cancellable with the caller's
context. `Map`, `Pipe`, and `Collect` provide common transformations.

## Adapting to Steps

The graph and workflow engines use string-based `Step` boundaries. Runnable
adapters supply explicit input/output conversion. For heterogeneous typed nodes,
use [TypedNode codecs](./graphs#typed-boundaries); for homogeneous typed data,
use `TypedGraph[T]` without string conversion.