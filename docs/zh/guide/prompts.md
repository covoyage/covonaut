# 提示词、规则与模型预设 {#prompts-rules-profiles}

## 系统提示词 {#system-prompt}

通过 `WithSystemPrompt` 或 `Config.SystemPrompt` 设置系统提示词，说明 Agent 的任务和回答方式。工具权限和执行限制应由应用代码检查，不能只依赖提示词。需要变量替换时，见[模板说明](./utilities#prompt-templates)。

## 项目规则 {#project-rules}

`NewRulesExtension` 读取项目规则并加入系统提示词，默认文件为 `AGENTS.md`。`WithRulesPaths` 指定其他文件，`WithRulesReader` 可以接入远程或沙箱中的读取方式。找不到的文件会被跳过。

```go
rules := agentcore.NewRulesExtension(
    agentcore.WithRulesPaths("AGENTS.md", ".cursorrules"),
)
cfg := agentcore.NewConfig(
    agentcore.WithModel(model),
    agentcore.WithProvider(provider),
    agentcore.WithExtensions(rules),
)
```

规则文件也可能包含不可信内容。读取这些规则不代表允许执行其中提到的命令，工具仍需检查权限。

## 模型预设（Profile） {#model-profiles}

`RegisterProfile` 为指定模型设置默认参数，包括提示词后缀、需要排除的工具、温度和最大轮数。用户明确填写的配置优先于这些默认值；提示词后缀则追加到原有提示词之后。

预设在创建 Agent 时应用。通过 `ApplyCallConfig` 更换模型时，不会自动应用新模型的预设，应用需要自行调整相应设置。

## 提示词缓存 {#prompt-caching}

`NewPromptCachingExtension` 在较稳定的消息前缀上添加缓存标记，供支持这一功能的模型服务使用。可以设置标记数量，以及系统提示词和近期消息的处理方式。使用零值扩展不会添加任何标记。

不同服务对缓存标记的支持不同。添加标记并不保证命中缓存、降低延迟或节省费用，应根据服务返回的用量信息验证实际效果。

见 [RulesExtension](https://pkg.go.dev/github.com/covoyage/covonaut/agentcore#RulesExtension)、[ModelProfile](https://pkg.go.dev/github.com/covoyage/covonaut/agentcore#ModelProfile) 和 [PromptCachingExtension](https://pkg.go.dev/github.com/covoyage/covonaut/agentcore#PromptCachingExtension)。