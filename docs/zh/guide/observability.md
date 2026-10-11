# 事件、链路与指标 {#events-traces-metrics}

## 订阅事件 {#event-consumers}

`On` 订阅一种事件，`OnAll` 订阅全部事件，两个方法都返回退订函数。如果在 HTTP 请求中订阅事件以发送 SSE，请求结束时必须退订，尤其是在多个请求复用同一 Agent 时。

```go
unsubscribe := agent.OnAll(func(event agentcore.Event) {
    fmt.Println(event.EventKind())
})
defer unsubscribe()
```

事件由总线异步分发，但处理函数在同一个 goroutine 中依次执行。处理过慢会拖延其他监听者；发布不会阻塞，缓冲区满时会丢弃事件。不要依赖订阅顺序来安排业务步骤。

处理函数应尽快返回，并保护共享状态。不要在处理函数中等待当前总线处理完所有事件，否则可能死锁。总线会捕获并报告 panic，但无法修复处理函数已经修改的状态。

::: warning 事件不是持久历史
需要恢复执行时，应读取保存的状态或检查点。客户端重连后，应根据快照同步状态，而不是假设它收到了每一段增量输出。
:::

## 区分多次请求的输出 {#streaming-attempts}

`AttemptID` 标识同一次模型请求尝试的输出，`message_reset` 表示需要撤回失败尝试的内容。界面应区分正在生成的文本和已经确认的消息。

工具结果、思考内容、多模态内容和终止错误是不同事件，需要分别处理。不能仅凭一段空文本判断运行已经结束。

## 链路追踪 {#tracing}

`Config.Tracer` 为 Agent、模型和工具记录关联的 span，图和工作流也支持链路追踪。`RunInfo` 通过 context 和事件传递运行标识。

`NewOtelTracer` 接入应用的 OpenTelemetry tracer。数据导出、资源属性、采样和关闭仍由应用配置。绕过 Agent 直接调用 Provider 时，可以使用 `NewModelSpanMiddleware`。

## 指标 {#metrics}

配置 `Metrics` 会记录 Agent 和工具错误。要记录模型用量、调用时长和错误，还需要用 `NewModelMetricsMiddleware` 包装 Provider；仅设置 `Metrics` 不会自动记录这些模型指标。

```go
metrics := agentcore.NewOtelMetrics(meter)
instrumented := agentcore.NewModelMetricsMiddleware(metrics)(provider)
cfg := agentcore.NewConfig(
    agentcore.WithModel(model),
    agentcore.WithProvider(instrumented),
)
cfg.Metrics = metrics
cfg.Tracer = agentcore.NewOtelTracer(tracer)
```

`meter` 和 `tracer` 由应用创建。备用 Provider 也需要接入，但同一个实例不要重复包装，以免重复计数。重试会增加调用次数，流式请求也可能在输出后失败。服务未返回用量时，相关指标可能缺失。

## 运维数据 {#operational-data}

建议关注调用延迟、token 用量、重试和切换频率、工具失败、压缩次数、队列积压及待审核任务。不要把完整提示词或每个会话 ID 都作为指标标签，否则会产生大量不同的时间序列。链路和审计记录可能包含用户数据，也需要脱敏和访问控制。