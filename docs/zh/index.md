# Covonaut {#covonaut}

用 Go 构建 Agent、工具和多 Agent 工作流。

Covonaut 是一个 Go 开发库，提供模型调用、工具执行、会话存储、图与工作流编排，以及常用协议的集成。你可以用它构建自己的 Agent 应用；模型账户、服务部署和用户界面仍由应用管理。

## 安装 {#install}

需要 **Go 1.25 或更高版本**。

```sh
go get github.com/covoyage/covonaut/agentcore
```

第一次使用时，建议按[快速开始](./guide/quickstart)运行一个带自定义工具的 Agent。如果已经接入模型服务，可以直接阅读[配置指南](./guide/configuration)。

## 从哪里开始 {#choose-your-starting-point}

想了解整体设计，可以先读[设计思想](./guide/design)。想按具体任务查找用法，可以查看[功能概览](./capabilities)。

| 任务 | 相关包 |
| --- | --- |
| [构建带自定义工具的 Agent](./guide/tools) | `agentcore`、`provider`、`tools` |
| [编排多个 Agent 或处理步骤](./guide/orchestration) | `graph`、`workflow` |
| [保存会话并创建分支](./guide/sessions) | `session`、`store` |
| [连接外部工具和远程 Agent](./integrations/) | `mcp`、`a2a`、`acp` |
| [构建 Web 或终端界面](./examples) | `server`、`agui`、`a2ui`、`tui` |

## Agent 如何运行 {#execution-model}

Agent 先将会话历史和工具定义发送给模型。如果模型要求调用工具，Agent 就执行相应函数，将结果写入历史，再次请求模型。这一过程会持续到模型给出最终回答，或达到设定的执行上限。

扩展可以添加工具和钩子；图与工作流则用来组织多个步骤，将 Agent 的执行纳入更完整的业务流程。

需要实时展示输出时，可以订阅[流式事件](./guide/runtime)。需要在后续请求中继续同一段对话时，可以配置[会话存储](./guide/sessions)。

## API 参考 {#api-reference}

完整的类型和函数说明见 [pkg.go.dev](https://pkg.go.dev/github.com/covoyage/covonaut)。本站内容对应仓库的 `main` 分支；如果项目固定了依赖版本，请查阅该版本的 API 文档。

部署前阅读[生产检查清单](./production)和[测试指南](./testing)。配置、流式输出、持久化或协议连接异常时，可查阅[问题排查](./troubleshooting)。