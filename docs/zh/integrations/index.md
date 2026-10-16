# 协议集成 {#integrations}

这些协议解决不同的集成需求，应按应用场景选择，而不是互相替代。

| 集成 | 用途 | 指南 |
| --- | --- | --- |
| MCP | 向 Agent 导入外部工具 | [MCP](./mcp) |
| A2A | Agent 发现和远程任务 | [A2A](./a2a) |
| ARD | 资源发布、发现与 Registry 搜索 | [ARD](./ard) |
| ACP | 编辑器／客户端会话 | [ACP](./acp) |
| AG-UI | 向 Web 客户端发送执行事件 | [AG-UI](./agui) |
| A2UI | 描述界面结构及数据更新 | [A2UI](./a2ui) |

完整应用端点见 [HTTP API](./http)。

## MCP {#mcp}

`NewStdioExtension` 启动 MCP 服务进程，并通过标准输入输出获取工具列表；`NewHTTPExtension` 连接 HTTP 服务。两者都通过 `Config.Extensions` 注册，创建时需要检查返回错误。

连接多个服务时，用 `ToolPrefix` 避免工具名称冲突。stdio 服务继承进程权限，需要限制工作目录、可用凭据和启动命令。远程服务的 `isError` 会保留在工具输出中，连接或传输失败则作为错误处理。见 [MCP API](https://pkg.go.dev/github.com/covoyage/covonaut/mcp)。

## ACP {#acp}

ACP 通过 JSON-RPC 连接编辑器与 Agent。应用实现 `AgentFactory` 和 `AgentInstance` 后，可以管理会话生命周期，并接入认证。见 [ACP API](https://pkg.go.dev/github.com/covoyage/covonaut/acp)。

## AG-UI {#ag-ui}

AG-UI 通过 SSE 传递生命周期、文本、思考、工具执行、状态和自定义事件。浏览器界面和 Go 服务需要单独部署，静态文档站不会运行它们。见 [AG-UI API](https://pkg.go.dev/github.com/covoyage/covonaut/agui)。

## A2A 与 A2UI {#a2a-and-a2ui}

A2A 用于发现远程 Agent 并管理任务。发现地址为 `/.well-known/agent-card.json`，旧地址 `agent.json` 仍可使用。对接 1.0 协议时，建议使用提供类型化请求和响应的 `client.V1()`。

A2UI 描述界面（surface）及其数据更新，可以通过 A2A 或 AG-UI 传输。它不执行任意界面代码，也不包含完整的浏览器渲染器。客户端需要将协议中的组件映射为自己的控件。