# Agent Execution

## Construct and Dispose

`New(cfg)` constructs an agent. When an extension cannot initialize, the agent
retains the error and surfaces it from execution entry points.
`NewWithError(cfg)` reports this failure immediately and closes the failed
instance. Prefer it when application startup must fail fast.

```go
agent, err := agentcore.NewWithError(cfg)
if err != nil {
    return err
}
defer agent.Close()
```

This fragment belongs inside a function returning an error. `Close` disposes
extensions and the owned event bus, and ends the context-engine session. It is
safe to call repeatedly, but do not run a closed agent again.

## Bound the Work

`Run` preserves history across calls and adds a new user input each time. The
system prompt is persisted once when absent. `Continue` adds no new input,
while `Resume` requires an existing interrupt. `MaxTurns` applies to one loop
invocation, not cumulatively to the conversation's entire lifetime.

Set a model and provider, a maximum turn count, and a context deadline. Tools
that perform network or process operations should respect the context passed to
them. Turn limits bound loop iterations, not wall-clock duration or every
possible side effect.

Tool concurrency is configured separately from the number of model turns. A
parallel tool batch can execute several functions with shared host resources;
synchronize those resources and preserve authorization in every function.

## Direct Tool Invocation

Use `InvokeTool` when application code needs a registered tool without a model
decision. It passes through the executor's hook and middleware pipeline. It is
not a provider call, and is not a substitute for all agent-run lifecycle events.
See [tool execution](./tools#execution-order).

## Steering and Follow-up

`Steer(message)` queues input for before the next model call. It changes future
reasoning; it does not forcibly terminate an already running tool. Cancellation
uses the run's context instead.

`FollowUp(message)` queues input for after the current conversation finishes
without additional tool calls, at which point the loop can restart.

`SteeringMode` and `FollowUpMode` control how queued messages are drained.
`ClearSteering` removes pending steering messages after cancellation, and
`SteeringPending` reports their count. Clear unused steering input deliberately
so it cannot leak into a later run.

## Runtime Configuration

`Config()` returns the current configuration. `ApplyCallConfig` updates supported
per-call fields such as model, thinking, response format, and selected skills.
Dedicated setters exist for thinking and fast mode. Not every constructor field
is a runtime setter; modifying an unrelated configuration copy does not
reconfigure the agent.

Model profiles are applied at construction, not automatically reapplied when
the model changes at runtime. See [profiles and prompts](./prompts).

## State and Persistence

`State()` exposes the current agent state. `SaveState` and `LoadState` require
`Config.Store` and use a caller-supplied key. Load failures and schema migration
errors must be handled; do not silently replace a user's conversation with an
empty one.

See [sessions and HTTP](./sessions) and the
[Agent API](https://pkg.go.dev/github.com/covoyage/covonaut/agentcore#Agent).

For an executable example, see [checkpoints and human review](./recovery).