# Agent 执行 {#agent-execution}

## 创建与关闭 {#construct-and-dispose}

`New(cfg)` 创建 Agent。如果扩展初始化失败，错误会被保留，直到调用执行方法时返回。`NewWithError(cfg)` 则立即返回错误并关闭创建失败的实例，适合在应用启动时检查配置和依赖。

```go
agent, err := agentcore.NewWithError(cfg)
if err != nil {
    return err
}
defer agent.Close()
```

这段代码应放在返回 `error` 的函数中。`Close` 会释放扩展、关闭 Agent 自己管理的事件总线，并结束上下文引擎会话。可以重复调用，但关闭后不要再次运行该 Agent。

## 设置执行上限 {#bound-the-work}

`Run` 会保留已有历史，并追加本次用户输入；历史中没有系统提示词时，会添加一次。`Continue` 直接继续已有对话，不添加输入。`Resume` 用于继续被中断的执行。

`MaxTurns` 限制本次执行的轮数，而不是整个会话累计的轮数。

除了模型和 Provider，还应设置最大轮数和执行超时。网络或进程工具需要响应传入 context 的取消信号。限制轮数并不等于限制运行时长，也不能防止工具执行危险操作。

工具并发数需要单独设置。多个工具访问同一资源时，应做好同步，并在每个操作中检查权限。

## 直接调用工具 {#direct-tool-invocation}

应用代码可以通过 `InvokeTool` 直接调用已注册工具，无需请求模型。调用会经过工具执行器的钩子和中间件，但不会触发完整的 Agent 生命周期。见[执行顺序](./tools#execution-order)。

## 引导与后续输入 {#steering-and-follow-up}

`Steer(message)` 将消息加入队列，在下一次模型调用前提供给模型。它不会停止正在执行的工具；需要停止运行时，应取消相应 context。

`FollowUp(message)` 在当前一轮对话结束、不再有工具调用时补充输入，Agent 随后可能继续执行。`SteeringMode` 和 `FollowUpMode` 决定每次如何读取队列中的消息。

`ClearSteering` 清除待处理的引导消息，`SteeringPending` 返回其数量。取消运行后，应清理不再需要的消息，避免下一次运行时意外使用它们。

## 运行时配置 {#runtime-configuration}

`Config()` 返回当前配置。`ApplyCallConfig` 可以更新模型、思考参数、响应格式、选中的技能等设置；思考和快速模式也有专用设置方法。并非所有配置都能在运行时修改，修改一份配置副本也不会自动更新 Agent。

模型预设（Profile）在创建 Agent 时应用，运行时更换模型不会自动应用新模型的预设。见[提示词与模型预设](./prompts)。

## 状态与持久化 {#state-and-persistence}

`State()` 返回当前状态。配置 `Config.Store` 后，可以使用同一个 key 调用 `SaveState` 和 `LoadState`。加载或版本迁移失败时应明确处理错误，不要直接用空历史替换用户会话。

见[会话](./sessions)、[Agent API](https://pkg.go.dev/github.com/covoyage/covonaut/agentcore#Agent)和可运行的[恢复示例](./recovery)。