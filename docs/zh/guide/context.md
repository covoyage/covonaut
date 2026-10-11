# 上下文引擎与压缩 {#context-engines-compaction}

上下文管理负责控制请求大小，让会话历史和工具定义尽量不超过模型限制。它不能代替长期存储或检索，也不能保证压缩后保留全部信息。

## 请求预算 {#budget-the-request}

`ContextWindow` 表示模型的上下文窗口大小。预算应包括消息、工具定义，以及为模型回答预留的空间。不同模型的分词方式不同，本地估算可能与实际用量有差异，最终仍以服务端限制为准。

```go
cfg := agentcore.NewConfig(
    agentcore.WithModel(model),
    agentcore.WithProvider(provider),
    agentcore.WithContextWindow(128000),
    agentcore.WithReserveTokens(16000),
    agentcore.WithKeepRecentTokens(4000),
    agentcore.WithCompressionThreshold(0.8),
    agentcore.WithProtectFirstN(3),
    agentcore.WithStructuredCompaction(true),
)
```

示例中的数值应按实际模型调整。将窗口大小设为正数会开启上下文管理。对于 compressor，也可以用 `AutoCompactTokenLimit` 指定触发压缩的 token 数，而不使用窗口比例。

## Compressor 与 Truncate {#compressor-and-truncate}

| 引擎 | 行为 | 代价 |
| --- | --- | --- |
| `compressor` | 用模型总结较旧历史 | 额外费用和延迟，有损摘要 |
| `truncate` | 删除中间历史，保留头部和近期尾部 | 不调用总结模型，被删信息丢失 |

通过 `CompactionConfig.Engine` 选择引擎。当前 truncate 的计算会预留四分之一窗口，它的触发方式与 compressor 不同，不能把诊断中显示的阈值当作相同的比例设置。

结构化压缩让摘要更有条理，但仍可能丢失信息。`ProtectFirstN` 保留最早的若干条非系统消息，`KeepRecentTokens` 保留近期内容。压缩后会修复工具调用与结果的配对，确保后续请求格式有效。

## 独立压缩模型 {#separate-compression-model}

可以为压缩单独选择模型和 Provider，以控制成本或改善摘要效果。该模型仍需支持待压缩的历史格式，并配置自己的凭据和服务地址。

引擎会避免反复执行效果不明显的压缩。如果受保护内容和工具定义已经超过预算，应减少保留内容、工具数量或结果大小，而不是继续重复压缩。

## 自定义引擎与诊断 {#custom-engines-and-diagnostics}

`ContextEngine` 定义会话开始与结束、用量更新、压缩判断和执行、可选工具定义及统计接口。可以通过 `WithCustomContextEngine` 传入实例，也可以注册工厂后切换引擎。仅注册工厂不会替换当前实例。

`ContextEngineStats` 返回诊断信息，`ForceCompact` 主动请求压缩，`ResetContextEngine` 清除会话相关状态。引擎会话在 Agent 关闭时结束，而不是每执行一轮就结束。

见 [ContextEngine](https://pkg.go.dev/github.com/covoyage/covonaut/agentcore#ContextEngine) 和 [CompactionConfig](https://pkg.go.dev/github.com/covoyage/covonaut/agentcore#CompactionConfig)。