# 包索引 {#package-index}

导出函数的签名、构造器和类型定义请查阅 pkg.go.dev。本站内容跟随 main 分支；如果项目固定使用某个版本，应查看该版本的 API 文档。

## 运行时与编排 {#runtime-and-orchestration}

| 包 | 职责 |
| --- | --- |
| [agentcore](https://pkg.go.dev/github.com/covoyage/covonaut/agentcore) | Agent 执行循环、工具、配置、事件、钩子、状态和 Runnable |
| [graph](https://pkg.go.dev/github.com/covoyage/covonaut/graph) | DAG、Pregel、类型图 |
| [workflow](https://pkg.go.dev/github.com/covoyage/covonaut/workflow) | 流水线、并行、路由 |
| [components](https://pkg.go.dev/github.com/covoyage/covonaut/components) | 文档加载、向量计算、索引和检索接口 |
| [tools](https://pkg.go.dev/github.com/covoyage/covonaut/tools) | 内置工具及底层操作接口 |
| [skill](https://pkg.go.dev/github.com/covoyage/covonaut/skill) | 技能加载与元数据解析 |
| [prompt](https://pkg.go.dev/github.com/covoyage/covonaut/prompt) | 模板 |
| [filequeue](https://pkg.go.dev/github.com/covoyage/covonaut/filequeue) | 协调进程内同一路径的文件操作 |
| [fuzzy](https://pkg.go.dev/github.com/covoyage/covonaut/fuzzy) | 规范化匹配与替换 |
| [pkg/util](https://pkg.go.dev/github.com/covoyage/covonaut/pkg/util) | 环境变量、字符串和错误处理辅助函数 |

## 持久化与接口 {#persistence-and-interfaces}

| 包 | 职责 |
| --- | --- |
| [session](https://pkg.go.dev/github.com/covoyage/covonaut/session) | JSONL 会话树及 AgentStore 适配 |
| [store](https://pkg.go.dev/github.com/covoyage/covonaut/store) | 状态存储 |
| [server](https://pkg.go.dev/github.com/covoyage/covonaut/server) | HTTP 服务、SSE 和线程管理 |
| [tui](https://pkg.go.dev/github.com/covoyage/covonaut/tui) | 终端引擎 |
| [mcp](https://pkg.go.dev/github.com/covoyage/covonaut/mcp) | 客户端和工具扩展 |
| [a2a](https://pkg.go.dev/github.com/covoyage/covonaut/a2a) | 远程 Agent 发现和任务管理 |
| [ard](https://pkg.go.dev/github.com/covoyage/covonaut/ard) | ARD 资源发布、发现与搜索 |
| [acp](https://pkg.go.dev/github.com/covoyage/covonaut/acp) | 编辑器与 Agent 的会话通信 |
| [agui](https://pkg.go.dev/github.com/covoyage/covonaut/agui) | 向 Web 客户端传输执行事件 |
| [a2ui](https://pkg.go.dev/github.com/covoyage/covonaut/a2ui) | 声明式界面、数据绑定与传输适配 |

## 模型适配器 {#providers}

- [chatcompat](https://pkg.go.dev/github.com/covoyage/covonaut/provider/chatcompat)
- [anthropic](https://pkg.go.dev/github.com/covoyage/covonaut/provider/anthropic)
- [gemini](https://pkg.go.dev/github.com/covoyage/covonaut/provider/gemini)
- [bedrock](https://pkg.go.dev/github.com/covoyage/covonaut/provider/bedrock)
- [vertex](https://pkg.go.dev/github.com/covoyage/covonaut/provider/vertex)
- [copilot](https://pkg.go.dev/github.com/covoyage/covonaut/provider/copilot)
- [mistral](https://pkg.go.dev/github.com/covoyage/covonaut/provider/mistral)