# Graph Execution

## DAG

`Graph` connects named `Step` nodes with acyclic dependencies. Check each
`AddNode` and `AddEdge` error, then compile with an entry node. Compilation
checks dependencies and topological ordering. Independent active nodes in a
layer can execute concurrently.

`AddConditionalEdge` declares possible targets and selects one using a node's
output. A source cannot mix static outgoing edges with this conditional route.
Declare all valid targets so compilation and visualization can inspect them.

`CompileOptions.StateFn` can create per-run `GraphState` accessible through
context. Its locking protects its own access methods, not arbitrary pointers or
external resources held by nodes. Do not mutate the builder while running a
compiled graph; build a new graph when changing topology.

## Typed Boundaries

`TypedNode[I, O]` adapts a typed Runnable into the string graph. Explicit `Decode`
and `Encode` functions define the boundary, with `JSONDecode[T]` and
`JSONEncode[T]` available as helpers. Decode/encode failures are execution errors,
not a reason to silently substitute a zero value.

`TypedGraph[T]` instead keeps the same type across all nodes. It avoids string
serialization. Multiple predecessor or terminal values require an explicit
`TypedCompileOptions[T].Merge` function. That function receives a collection of
values; avoid coupling business logic to an incidental branch order.

Typed values may still contain slices, maps, or pointers. Passing a struct by
value does not deep-copy those members. Treat branch inputs as immutable or
copy mutable data before parallel modification.

## Offline Example

```sh
go run ./example/orchestration
```

The program normalizes and measures a document in a typed graph, publishes it
through a workflow router, and demonstrates stream aggregation. It requires no
provider credentials.

<<< @/../example/orchestration/main.go

## Pregel

Use `PregelGraph` for cyclic execution with shared state. Each super-step gives
active nodes a snapshot, waits for their results, and merges updates before
scheduling the next nodes. This is different from mutating one shared map from
several goroutines.

Declare conditional targets with `SetConditionalEdgeWithTargets`. Route to
`PregelEnd` to finish, and configure a finite step limit even when your business
logic has an expected termination condition.

Avoid ambiguous concurrent writes to the same state key. State cloning handles
common map/slice/message values, but does not make arbitrary external objects,
channels, or pointer-rich application state independently mutable.

## Unified Runner and Streaming

`NewDAGRunner` and `NewPregelRunner` provide a common execution entry point with
runner configuration, tracing, and optional checkpoint storage. They do not
make DAG and Pregel trigger semantics interchangeable.

`Runner.RunStream` currently supports DAG mode only and rejects output-based
conditional edges. Non-streaming steps are adapted by collecting their input
and wrapping their result. This does not make a blocking node progressively
stream its internal work.

## Introspection and Recovery

Compiled graphs and runners expose `AsTool(graph.ToolConfig{...})` so an agent
can invoke a deterministic process through an `input` parameter. Pregel's
string bridge puts input in `PregelState["input"]` and reads `"output"` from the
result. Define those state conventions explicitly in your nodes.

Compiled graphs provide `Info`, diagnostics, and Mermaid export. Use them to
inspect reachable nodes, declared targets, and execution structure before
production runs.

Graph `CheckpointStore` and `InterruptableGraph` are separate from agent
`CheckpointSaver`. Interruptable graphs can pause before or after selected nodes
and resume by checkpoint ID. Test the specific topology, conditional routing,
and restart behavior you use; do not assume checkpointing provides exactly-once
external effects.