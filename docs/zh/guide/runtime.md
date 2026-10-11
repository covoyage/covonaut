# 流式输出与可靠性 {#streaming-reliability}

## 流式事件 {#streaming-events}

通过 `WithStreaming(true)` 开启流式输出，并在运行 Agent 前订阅事件：

```go
unsubscribe := agent.On(agentcore.EventMessageDelta, func(event agentcore.Event) {
    delta := event.(*agentcore.MessageDeltaEvent)
    fmt.Print(delta.Content)
})
defer unsubscribe()
```

事件总线还会报告工具执行和生命周期变化。处理函数应快速返回，访问共享状态时需要同步。各事件的数据类型见[事件 API](https://pkg.go.dev/github.com/covoyage/covonaut/agentcore#Event)。

每段增量输出都带有 `AttemptID`。发生重试或故障切换时，`message_reset` 表示此前的部分输出应被撤回。界面需要处理这个事件，不能把所有输出直接拼接在一起。

## 上下文管理 {#context-management}

将 `ContextWindow` 设为正数会启用上下文管理。`CompressionThreshold` 设置压缩阈值，`ReserveTokens` 为回答预留空间，`KeepRecentTokens` 指定保留的近期历史。其他压缩方式见[上下文指南](./context)。

## 重试与故障切换 {#retry-and-failover}

`RetryConfig` 控制重试次数和等待时间，`ModelFailoverConfig` 指定备用模型服务及其顺序。重试和备用调用都可能产生费用，需要限制次数和预算。

工具选择可以减少发送给模型的工具定义，但不会从执行器中移除工具。大型结果也可以保存到单独的存储中，避免反复放入上下文。

### 配置重试与故障切换 {#configure-retry-and-failover}

```go
cfg.RetryConfig = &agentcore.RetryConfig{
    MaxRetries: 3, BaseDelayMs: 1000, MaxDelayMs: 15000,
}
cfg.Failover = &agentcore.ModelFailoverConfig{
    Targets: []agentcore.ModelTarget{{
        Name: "backup", Model: backupModel, Provider: backupProvider,
    }},
}
```

模型请求的重试与工具的 `RetryMiddleware` 是两回事。默认只有可重试的失败才可能触发故障切换，取消、中断和重复循环错误不会触发。可以通过 `ShouldFailover` 和 `SelectTarget` 调整策略。备用模型也必须支持当前工具、消息内容和输出格式。

上下文溢出由配置的压缩机制单独处理。遇到重复文本、重复工具调用或流重复错误时，运行时会在有限次数内加入纠正提示，而不是无限重试同一个请求。

## 工具选择 {#tool-selection}

```go
cfg.ToolSelection = &agentcore.ToolSelectionConfig{
    Selector: agentcore.KeywordToolSelector{IncludeUnmatched: true},
    MaxVisible: 12,
    AlwaysVisible: []string{"help"},
}
```

`AlwaysVisible` 中的工具需要事先注册。`KeywordToolSelector` 根据近期用户输入与工具说明的匹配程度排序。`EmbeddingToolSelector` 使用应用提供的向量计算函数，并支持缓存、分数阈值和备用选择方式；应用需要处理调用费用和错误。

选择器只决定模型能看到哪些工具定义，不检查执行权限。已注册的工具仍能按名称调用，因此需要单独授权。测试时也要检查必需工具被遗漏后的行为。

## 将大型结果移出上下文 {#artifact-offloading}

```go
cfg.ArtifactOffload = &agentcore.ArtifactOffloadConfig{
    Store: agentcore.NewMemoryArtifactStore(),
    MinBytes: 50 * 1024,
    ExcludeTools: []string{"small_status"},
}
```

较大的成功结果可以保存为产物，并在上下文中替换为 `artifact://` 引用。运行时会注册 `artifact_read`，供模型读取。错误结果和被排除的工具不作此处理；存储失败时仍保留原结果。

内存存储不会跨进程保存，也不会自动清理过期内容。生产环境需要自行设置访问权限、大小限制、保留期限和清理方式。完整读取产物会把全文重新放回上下文，因此这种方式不能保证后续请求始终很小。

## 可观测性 {#observability}

可以通过 `Tracer` 和 `Metrics` 配置链路与指标，使用 `NewOtelTracer` 和 `NewOtelMetrics` 接入 OpenTelemetry。运行标识将 Agent、模型、工具、图和工作流关联起来。采集内容可能包含敏感数据，应限制记录范围和保留时间。

如何记录模型指标，以及事件可能丢失的情况，见[可观测性指南](./observability)。