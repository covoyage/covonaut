# Checkpoints & Human Review

## Three Persistence Concepts

| Mechanism | What it keeps | How to use it |
| --- | --- | --- |
| `Store` | A keyed state snapshot | `SaveState` / `LoadState` |
| Session tree | Append-only entries and conversation branches | `session.FileStore`, optionally `session.AgentStore` |
| `CheckpointSaver` | A sequence of execution snapshots | Turn checkpoints, `SaveCheckpoint`, `RestoreLatestCheckpoint` |

None provides exactly-once execution of an external side effect. A snapshot may
be written before or after a tool changes another system. Use application-level
transactions, idempotency keys, or reconciliation for that boundary.

## Automatic Checkpoints

```go
saver := agentcore.NewMemoryCheckpointSaver()
saver.MaxCheckpointsPerThread = 20
cfg.Checkpoint = &agentcore.CheckpointSettings{
    Saver: saver,
    ThreadID: "conversation-123",
    SaveOnTurnStart: true,
}
```

By default, a configured saver receives checkpoints at turn end.
`SaveOnTurnStart` also saves before the provider call, after steering and
compaction. `SkipSaveOnTurnEnd` disables automatic turn-end saves.

The memory saver survives neither process exit nor machine failure. Implement
`CheckpointSaver` with durable storage for restart recovery. Shared savers must
be concurrency-safe; thread IDs must isolate users and conversations. An empty
thread ID defaults to `default`, which is inappropriate for unrelated users.

## Interrupt and Resume

A tool can return `NewInterruptError` or `NewInterruptErrorWithData`. The agent
records a friendly tool result and interrupt reason, then returns control.
Check both the returned error and `Interrupted()`; a nil error does not mean
that the conversation reached a final answer.

The host decides what happens next. Inspect structured interrupt data, collect
a human decision, queue a message with `Steer`, and call `Resume`.
`Resume` requires an interrupt reason and continues from the stored tool result.
It does not automatically execute the original operation after an approval.

::: warning Approval is a host policy
Authenticate the reviewer, bind the decision to a specific operation, and enforce
that decision in the side-effecting tool. A model receiving the word "approved"
is not sufficient authorization. Parallel batches may already have executed
other tools when one requests an interrupt.
:::

## Offline Example

```sh
go run ./example/recovery
```

No credentials are needed. A deterministic provider requests review, a checkpoint
captures the interrupt, and another agent restores and resumes the conversation.
Both agents share the memory saver; this is restoration within one process,
not persistence across restarts.

<<< @/../example/recovery/main.go

## Choose the Next Entry Point

| Method | Behavior |
| --- | --- |
| `Run(ctx, input)` | Append a user message and run using existing history |
| `Continue(ctx)` | Continue history without appending user input |
| `Resume(ctx)` | Require an interrupt, clear it, then continue |

`RestoreLatestCheckpoint` restores state and interrupt metadata, but does not
itself call the provider. Choose the next entry point based on restored status.

## Schema Migration

Snapshots carry a version. `StateSnapshotMigrator` advances one version at a
time; a version jump or a snapshot newer than the supported version is an error.
Legacy unversioned snapshots use version zero. Configure `WithStateMigrators`
for ordinary snapshots and the checkpoint migration settings where required.

Test migration against persisted fixtures before upgrading a production
application. Preserve copies until recovery has been validated.