# 问题排查 {#troubleshooting}

## 配置无法编译 {#configuration-does-not-compile}

Go 允许通过嵌入结构体访问字段，但不能在 `Config` 字面量中直接初始化这些字段。应填写对应的嵌套配置组，或使用函数式选项。还需检查示例是否适用于当前 API：路由使用 `Router.Steps`，交接使用 `HandoffConfig.AgentConfig`，Bedrock 需要显式凭据，Vertex 使用 `vertex.NewGemini` 或 `NewAnthropic`。

## 工具不执行 {#tools-do-not-execute}

依次检查工具是否已注册、名称是否正确、模型是否支持工具调用，以及参数验证、before 钩子和权限检查是否拒绝了调用。选择器可能只隐藏工具定义，并未取消注册。可以通过 `InvokeTool` 绕过模型，单独测试工具执行器。

`UnknownToolHandler` 可以返回说明，帮助模型纠正工具名称，但不要把未知名称一律映射到高权限操作。

## 输出重复 {#repeated-or-duplicate-output}

检查 `AttemptID` 和 `message_reset`，不要将失败尝试的输出与成功结果拼接。复用 Agent 时，还要检查是否忘记退订事件，或重复绑定了 TUI。确认收到的是新增文本片段还是完整快照，两者的合并方式不同。

运行时对重复输出的纠正次数有限。应检查工具参数、执行错误，以及返回给模型的结果是否足以让它继续任务。

## 上下文仍溢出 {#context-still-overflows}

检查实际模型窗口、工具定义大小、回答预留空间，以及保留的早期和近期消息。压缩会丢失信息，但不会删除受保护内容。完整读取产物后，上下文也会再次增大。可以减少工具数量和结果大小，改用按需检索，或调整预算。

## 会话丢失 {#conversation-is-missing}

确认保存和加载使用同一个线程 ID 或 key，并连接到正确的存储。会话树应通过 `MessagesOnPath` 读取当前路径，不能直接使用全部 JSONL 条目。内存 saver 的数据不会在重启后保留；创建新历史之前，应先检查是否发生了版本或迁移错误。

## 恢复不符合预期 {#resume-fails-or-does-nothing-expected}

`Resume` 要求状态中包含中断原因，不能仅根据回答看起来未完成就调用。它也不会自动重新执行已获批准的工具。AG-UI 的 resume 输入不能代替应用验证批准记录并恢复 Agent 的流程。

## 流停止或挂起 {#streams-stop-or-hang}

检查流的终止错误和取消信号。`Send` 返回 false 后，发送方应停止；发送完成后关闭流。事件处理函数不能长时间阻塞，也不能等待当前总线处理完事件。HTTP 中间件应保留 `Flusher`，代理不要缓冲实时输出。

## 云认证失败 {#cloud-authentication-fails}

按适配器要求传入凭据，确认账户有权访问端点，区域和模型 ID 正确，临时 token 尚未过期。不能根据通用认证错误推断适配器支持 SDK 的全部凭据查找方式。

## 本地正常，Pages 部署后无法访问 {#pages-works-locally-but-not-online}

检查 VitePress 的 base 是否为 `/covonaut/`，Pages Source 是否正确，以及工作流权限、环境审批和构建产物是否正常。GitHub Pages 只托管静态文档，不会运行 Go 服务。见[部署说明](./contributing#github-pages-deployment)。