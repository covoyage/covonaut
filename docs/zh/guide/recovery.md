# 检查点与人工审核 {#checkpoints-human-review}

## 三种持久化概念 {#three-persistence-concepts}

| 机制 | 保存内容 | 使用方式 |
| --- | --- | --- |
| `Store` | 按 key 保存状态快照 | `SaveState`／`LoadState` |
| 会话树 | 追加式条目和分支 | `session.FileStore`，可配 `AgentStore` |
| `CheckpointSaver` | 多个时点的执行快照 | 自动保存、`SaveCheckpoint`、`RestoreLatestCheckpoint` |

这些机制都不能保证外部操作只执行一次。工具可能已经完成写入，但快照尚未保存；恢复后再次运行就可能重复写入。对此，需要应用使用事务、幂等键或查询外部状态来核对执行结果。

## 自动检查点 {#automatic-checkpoints}

```go
saver := agentcore.NewMemoryCheckpointSaver()
saver.MaxCheckpointsPerThread = 20
cfg.Checkpoint = &agentcore.CheckpointSettings{
    Saver: saver,
    ThreadID: "conversation-123",
    SaveOnTurnStart: true,
}
```

配置 saver 后，默认在每轮结束时保存检查点。启用 `SaveOnTurnStart`，还会在处理引导消息和上下文压缩之后、调用模型之前保存。`SkipSaveOnTurnEnd` 可以关闭轮末的自动保存。

内存 saver 的数据会在进程退出后丢失。如果需要重启后恢复，应实现持久化 saver。多个调用共享实例时需保证并发安全，并用不同 ThreadID 区分用户和会话。空 ID 会使用 `default`，不要让无关会话共用这个值。

## 中断与恢复 {#interrupt-and-resume}

工具可以返回 `NewInterruptError` 或 `NewInterruptErrorWithData` 来暂停执行。Agent 会记录中断原因和一条说明中断的工具结果，然后返回。调用方需要同时检查返回错误和 `Interrupted()`；没有错误不代表已经得到最终回答。

应用收到中断后，可以检查附带数据、请用户审核，再通过 `Steer` 告知模型审核结果并调用 `Resume`。恢复会从已有工具结果继续，不会因为审核通过就自动重新执行原来的操作。

::: warning 审核通过后仍需检查权限
应用应验证审核者身份，记录批准的是哪项操作，并让工具执行时检查这份批准记录。仅告诉模型“已批准”并不能完成授权。如果工具并行执行，其他操作可能在中断发生前已经完成。
:::

## 离线示例 {#offline-example}

```sh
go run ./example/recovery
```

示例无需模型凭据。测试 Provider 返回固定的审核请求，检查点保存中断状态，另一个 Agent 读取后继续执行。两个实例共用内存 saver，因此演示的是同一进程内恢复，而不是重启后的恢复。

<<< @/../example/recovery/main.go

## 恢复后调用哪个方法 {#choose-the-next-entry-point}

| 方法 | 行为 |
| --- | --- |
| `Run(ctx, input)` | 追加用户消息并运行 |
| `Continue(ctx)` | 不追加输入，继续历史 |
| `Resume(ctx)` | 要求并清除中断后继续 |

`RestoreLatestCheckpoint` 只恢复状态和中断信息，不调用模型。读取检查点后，应根据状态选择上表中的方法。

## 快照版本迁移 {#schema-migration}

快照包含版本号。`StateSnapshotMigrator` 每次升级一个版本，跳过版本或读取比当前程序更新的快照都会报错。旧的无版本快照按版本零处理。普通快照通过 `WithStateMigrators` 配置迁移，检查点则使用对应的迁移设置。

上线升级前，应使用实际保存的快照测试迁移。确认能够恢复之前，不要删除旧数据备份。