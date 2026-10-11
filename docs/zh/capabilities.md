# 功能概览 {#capability-map}

下面按功能列出相关指南，并说明哪些部分需要由应用自行处理。

| 功能 | 指南 | 应用需要负责什么 |
| --- | --- | --- |
| Agent 执行与调用方式 | [执行](./guide/execution) | 管理运行与资源，隔离用户会话 |
| 配置组与函数式选项 | [配置](./guide/configuration) | 设置适合模型的参数和预算 |
| 工具、钩子、并发与输出 | [工具](./guide/tools) | 检查权限，防止重复操作 |
| 文件、Shell、Git、网络、浏览器与终端 | [工具目录](./guide/builtin-tools) | 配置依赖，隔离不可信执行 |
| 扩展与生命周期 | [扩展](./guide/extensions) | 管理资源，安排扩展顺序 |
| 提示词、规则、模型预设与缓存 | [提示词](./guide/prompts) | 审查指令来源，验证缓存效果 |
| 技能与按需加载 | [技能](./guide/skills) | 审查技能内容，保护管理接口 |
| 消息、图片、思考内容与转换 | [消息](./guide/messages) | 保护数据，检查模型兼容性 |
| 将 JSON 解码为 Go 类型 | [结构化输出](./guide/structured-output) | 验证业务规则 |
| 模型服务适配器 | [模型服务](./guide/providers) | 提供凭据，确认账户权限 |
| 重试、故障切换、工具选择与结果卸载 | [可靠性](./guide/runtime) | 控制费用，管理结果存储与重试 |
| 上下文管理与摘要 | [压缩](./guide/context) | 设置预算，评估信息丢失 |
| 事件、链路与指标 | [可观测性](./guide/observability) | 配置数据采集，脱敏并同步状态 |
| 普通调用与流式处理 | [Runnable](./guide/runnables) | 正确聚合结果，处理取消 |
| DAG、Pregel、类型化图与图工具 | [图](./guide/graphs) | 定义合并规则和结束条件 |
| 流水线、路由、交接与任务委托 | [工作流](./guide/workflows) | 分配职责与权限，释放资源 |
| 状态快照、会话树与分支 | [会话](./guide/sessions) | 保存数据，设置权限和保留期限 |
| 检查点、中断与恢复 | [恢复](./guide/recovery) | 验证审核决定，防止重复操作 |
| HTTP、SSE 与线程接口 | [HTTP](./integrations/http) | 认证用户，部署服务 |
| MCP 工具、资源与提示词 | [MCP](./integrations/mcp) | 选择可信服务，保护连接 |
| A2A 远程任务 | [A2A](./integrations/a2a) | 认证远程请求，检查协议兼容性 |
| ACP 编辑器会话 | [ACP](./integrations/acp) | 实现实例工厂和所需能力 |
| AG-UI 执行事件 | [AG-UI](./integrations/agui) | 实现客户端界面，同步会话状态 |
| A2UI 声明式界面 | [A2UI](./integrations/a2ui) | 根据组件目录实现客户端渲染 |
| 终端界面与事件适配 | [终端](./guide/tui) | 兼容不同终端，管理运行与关闭 |
| RAG 组件接口 | [检索](./guide/rag) | 接入检索后端，检查文档权限 |
| 模板、文件队列与文本匹配 | [辅助包](./guide/utilities) | 验证输入，检查文件修改 |

## 阅读路径 {#reading-paths}

- 初次使用：先读快速开始，再了解配置、工具和模型服务。
- 开发交互应用：了解执行、消息和事件，再选择 HTTP 或终端界面。
- 组织多步骤流程：了解 Runnable、图、工作流和执行恢复。
- 准备上线：检查可靠性、会话存储、生产清单和测试方法。

[包索引](./reference)提供各包的 API 文档链接。本站内容对应 `main` 分支；实际可用的功能还取决于所连接的服务和模型。