# 示例 {#examples}

以下命令都从仓库根目录运行。需要模型服务的示例会使用你的账户凭据，可能产生费用。

## 快速开始 {#quick-start}

```sh
go run ./example/quickstart
```

运行前设置 `OPENAI_API_KEY` 和 `AGENT_MODEL`。使用其他兼容服务的配置方法见[快速开始](./guide/quickstart)。

## CLI 聊天 {#cli-chat}

```sh
go run ./example/cli-chat
```

这个命令行应用展示工具调用、专家 Agent 交接、流式输出、模型选择、技能和会话管理。[示例源码](https://github.com/covoyage/covonaut/tree/main/example/cli-chat)说明了各 Provider 所需的环境变量，以及如何启用持久化。

## A2A 服务与客户端 {#a2a-server-and-client}

分别在两个终端运行：

```sh
go run ./example/a2a-server
```

```sh
go run ./example/a2a-client
```

服务使用返回固定结果的测试 Provider，无需模型密钥，默认监听端口 8080。可以通过 `PORT` 更换端口，但需要同时检查客户端的目标 URL。

::: details 完整服务源码
<<< @/../example/a2a-server/main.go
:::

## 终端界面 {#terminal-ui}

```sh
go run ./example/tui-demo
go run ./example/tui-demo2
go run ./example/tui-demo3
```

请在交互式终端中逐个运行。这些示例使用 TUI，而不是浏览器界面，分别展示输入输出、主题、组件、聊天状态和适配器。配置见[示例源码](https://github.com/covoyage/covonaut/tree/main/example)，设计与用法见[终端指南](./guide/tui)。

## 其他完整示例 {#additional-complete-examples}

| 命令 | 演示 | 凭据 |
| --- | --- | --- |
| `go run ./example/recovery` | 中断、检查点、恢复、继续 | 无 |
| `go run ./example/orchestration` | 类型图、Pipeline／Router、流聚合 | 无 |
| `go run ./example/http-server` | 认证 HTTP、持久化线程 | 模型密钥／名称和 API token |

完整源码及执行方式见[恢复指南](./guide/recovery)、[图执行](./guide/graphs)和[HTTP API](./integrations/http)。

## 兼容测试 {#compatibility-suites}

`provider-compat` 用于测试模型服务兼容性，`itk` 是供 A2A 集成测试工具调用的 Agent。它们适合验证集成，初次使用建议先运行上面的示例。