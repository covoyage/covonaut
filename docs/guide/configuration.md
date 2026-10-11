# Configuration

`agentcore.Config` embeds four configuration groups. Use functional options for
small agents, or explicit nested groups for larger applications.

## Functional Options

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

Here and in other short snippets, `model` and `provider` are values supplied by
your application. See the [Quick Start](./quickstart) for a complete program.

## Configuration Groups

| Group | Responsibilities |
| --- | --- |
| `ModelConfig` | Provider, model, generation parameters, streaming, thinking, structured output, failover |
| `ExecutionConfig` | Turns, concurrency, argument validation, middleware, tool selection, steering |
| `CompactionConfig` | Context budget, compression thresholds, recent history, context engine |
| `SkillConfig` | Available and selected skills, loading paths, skill API controls |

Tools, extensions, lifecycle hooks, stores, checkpoints, tracing, and retries
are configured directly on `Config`.

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

::: warning Go struct literals
Embedded fields can be read as `cfg.Model`, but cannot be initialized with
`Config{Model: model}`. Use `ModelConfig: ModelConfig{Model: model}` or
`WithModel(model)` instead.
:::

## Choosing Defaults

Set the model explicitly, use context deadlines, and choose turn and concurrency
limits appropriate to your tools. Context-window sizes and provider capabilities
are model-specific; do not copy another model's token budget unchanged.

See the [Config API](https://pkg.go.dev/github.com/covoyage/covonaut/agentcore#Config)
for the full field list and defaults.