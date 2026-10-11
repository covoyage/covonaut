# 配置 {#configuration}

`agentcore.Config` 包含四个嵌入式配置组。配置项较少时，可以使用函数式选项；需要集中管理较多设置时，可以直接填写各配置组。

## 函数式选项 {#functional-options}

```go
cfg := agentcore.NewConfig(
    agentcore.WithName("assistant"),
    agentcore.WithModel(model),
    agentcore.WithProvider(provider),
    agentcore.WithSystemPrompt("You are a helpful assistant."),
    agentcore.WithMaxTurns(20),
)
agent, err := agentcore.NewWithError(cfg)
if err != nil {
    return err
}
defer agent.Close()
```

示例中的 `model` 和 `provider` 需要由应用提供。可直接运行的完整程序见[快速开始](./quickstart)。

## 配置组 {#configuration-groups}

| 配置组 | 职责 |
| --- | --- |
| `ModelConfig` | Provider、模型、生成参数、流式输出、思考参数、结构化输出、故障切换 |
| `ExecutionConfig` | 轮数、并发、参数验证、中间件、工具选择、引导消息 |
| `CompactionConfig` | 上下文预算、压缩阈值、保留历史、上下文引擎 |
| `SkillConfig` | 可用技能、选中的技能、加载路径、技能 API 设置 |

工具、扩展、生命周期钩子、存储、检查点、链路追踪和重试等设置直接放在 `Config` 中。

```go
cfg := agentcore.Config{
    ModelConfig: agentcore.ModelConfig{
        Name: "assistant", Model: model, Provider: provider,
    },
    ExecutionConfig: agentcore.ExecutionConfig{
        MaxTurns: 20, ValidateArguments: true,
    },
    CompactionConfig: agentcore.CompactionConfig{
        ContextWindow: 128000, CompressionThreshold: 0.8,
    },
    SystemPrompt: "You are a helpful assistant.",
}
```

::: warning Go 结构体字面量
Go 允许通过 `cfg.Model` 访问嵌入结构体的字段，但不允许在 `Config` 字面量中直接写 `Model: model`。初始化时应填写嵌套的 `ModelConfig`，或使用 `WithModel(model)`。
:::

## 选择默认值 {#choosing-defaults}

建议明确指定模型，并根据工具的用途设置执行超时、最大轮数和并发数。不同模型的上下文窗口和功能不同，不能直接照搬其他模型的 token 预算。

完整字段与默认值见 [Config API](https://pkg.go.dev/github.com/covoyage/covonaut/agentcore#Config)。