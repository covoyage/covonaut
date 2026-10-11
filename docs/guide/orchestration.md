# Graph & Workflow

Choose orchestration based on the control flow you need, rather than treating
every multi-step task as a multi-agent problem.

| Need | Abstraction |
| --- | --- |
| Sequential stages, parallel steps, routing | `workflow.Pipeline`, `Parallel`, `Router` |
| Explicit acyclic dependencies | `graph.Graph` compiled as a DAG |
| Cycles and iterative shared state | `graph.PregelGraph` |
| Homogeneous typed values through nodes | `graph.TypedGraph[T]` |
| Delegate to another agent or transfer control | `agentcore.HandoffConfig` |

Continue with [Runnables and streams](./runnables), [graph execution](./graphs),
or [workflows and handoffs](./workflows) for executable examples and semantics.

## Workflows

Pipelines execute ordered steps. Parallel workflows combine independent results
through a merge function. Routers select a branch from the current input.
Use the [workflow API](https://pkg.go.dev/github.com/covoyage/covonaut/workflow)
for step contracts and execution options.

## Graphs

Build a DAG by adding nodes and edges, then compile it with an entry node.
Pregel graphs support repeated super-steps and conditional routing through
shared state. Compilation provides diagnostics and deterministic introspection;
compiled graphs also expose Mermaid export for architecture documentation.

`Runnable[I, O]` supports Invoke, Stream, Collect, and Transform modes. Typed node
codecs bridge typed runnables into the string-based graph engine; `TypedGraph[T]`
keeps homogeneous values typed without that conversion.

See the [graph API](https://pkg.go.dev/github.com/covoyage/covonaut/graph) and
[Runnable API](https://pkg.go.dev/github.com/covoyage/covonaut/agentcore#Runnable).

## Agent Handoffs

A handoff target uses `AgentConfig` to configure a local specialist.
`HandoffDelegate` delegates a task, while `HandoffTransfer` transfers control.
Remote targets can be registered with the [A2A integration](../integrations/a2a).

The [CLI example](../examples#cli-chat) configures weather and math specialists
with a coordinator, illustrating local handoffs alongside tool execution.