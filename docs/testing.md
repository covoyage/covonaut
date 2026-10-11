# Testing Agent Applications

## Start Offline

Implement a deterministic `Provider` that returns known responses, tool calls,
and failures. This tests host execution without relying on model output or
spending API credits. The [recovery](./guide/recovery) and
[structured-output](./guide/structured-output) examples show minimal providers.

Test tool functions separately from the model. Then exercise their executor
pipeline using `InvokeTool` to verify hooks, validation, cancellation, and errors.
For real external operations, use test adapters or local protocol fixtures.

## Test Behavior, Not Just Text

| Area | Useful assertions |
| --- | --- |
| Tools | Allowed/rejected calls, argument coercion/validation, hook order, side effects |
| Agent loop | Turn budget, follow-up, steering, interrupt and next entry point |
| Streaming | Terminal error, attempt reset, consumer cancellation, snapshot reconciliation |
| State | Save/load, branches, version migration, checkpoint restoration |
| Graphs | Invalid topology, declared route targets, fan-in merge, step limits |
| Protocols | Initialization, capabilities, auth, notifications, disposal |
| UI | Resize, long output, Unicode width, focus, reconnect, run cancellation |

An exact final model sentence is usually a fragile assertion. Prefer the
application's contract: required fields, selected tool, operation count,
persisted history, authorization outcome, or final state.

## Local Commands

From the repository root:

```sh
go vet ./...
go build ./...
go test ./...
go run ./example/recovery
go run ./example/orchestration
npm --prefix docs run check
```

Run relevant package tests while developing a narrow change. Race-enabled tests
are useful for shared tool state, event consumers, and parallel orchestration:

```sh
go test -race ./agentcore ./graph ./workflow ./session
```

## Provider Compatibility

`example/provider-compat` contains opt-in compatibility checks. Inspect its
configuration before running against external services. Real-provider tests
should be separate from deterministic CI, with explicit credentials, budgets,
model choices, and cleanup.

## Documentation Checks

The site checker validates rendered links/assets/anchors and compiles complete
Go code blocks. The four A2UI fragments are compiled and run with offline
fixtures, including empty-input and missing-surface failures. Two workflow
fragments also verify that child agents are closed after execution. Other short
fragments are illustrative, not individually compiled.
Actual offline examples should also be run when their execution semantics matter.

Compilation cannot prove external model access, deployment permissions, cloud
token refresh, or successful interaction with a third-party protocol server.