# Covonaut 的 A2A 协议支持 {#a2a-protocol-support-for-covonaut}

`a2a` 实现 [Agent2Agent](https://a2a-protocol.org/) 协议，使 Covonaut 与其他兼容 Agent 互操作。

## 功能 {#features}

- Agent Card 发现：`/.well-known/agent-card.json`，旧 `agent.json` 别名。
- A2A 1.0 的 11 个标准 JSON-RPC 方法，包括推送配置和扩展卡片。
- `client.V1()` 的类型化请求、响应和流。
- 声明安全要求，并支持认证、版本及必需扩展的协商。
- 旧 `tasks/*` 和 Go API 兼容。
- 管理任务生命周期，支持同步响应和 SSE，传输文本、文件及结构化数据，并支持 Webhook 和远程 Agent 交接。

## 架构 {#architecture}

`types` 定义 Card、Task、Message、Part 和 Artifact；`server` 提供基于 HTTP 的 JSON-RPC 服务；`client` 调用远程 Agent；`handoff` 将远程调用接入本地 Agent 交接机制。

## 快速开始 {#quick-start}

### 1. 将 Agent 暴露为服务 {#_1-expose-your-agent-as-an-a2a-server}

配置模型和密钥后，可以运行以下程序。Agent Card 描述服务的能力和技能，`DefaultAgentHandler` 负责将请求交给 Agent 执行。

<!--@include: ../../../a2a/README.md{32,91}-->

生产环境还需处理认证、超时和资源释放。无模型本地联调可运行[服务与客户端示例](../examples#a2a-server-and-client)。

### 2. 调用远程 A2A 1.0 Agent {#_2-call-a-remote-a2a-1-0-agent}

先获取远程服务的 Agent Card，再通过标准客户端发送请求。响应可能直接包含消息，也可能返回需要继续跟踪的任务状态。

<!--@include: ../../../a2a/README.md{95,147}-->

### 3. 订阅流式任务更新 {#_3-streaming-task-updates}

片段应放在返回错误的函数中，并使用设有超时的 context。收到 EOF 表示流已结束，其他错误需要返回给调用方。使用结束后应关闭流，编译这段代码时还需导入 `errors` 和 `io`。

<!--@include: ../../../a2a/README.md{153,181}-->

### 4. 接入交接系统 {#_4-integrate-with-handoff-system}

调用方应检查创建错误，并在使用结束后关闭 Agent。注册远程目标后，模型可调用 `transfer_to_math-expert` 等工具委托任务。

<!--@include: ../../../a2a/README.md{187,218}-->

### 5. 带回调的适配器 {#_5-advanced-adapter-with-callbacks}

回调可处理输入、输出和状态变化；记录日志时避免泄漏用户内容。

<!--@include: ../../../a2a/README.md{224,244}-->

## API 参考 {#api-reference}

### 服务端点 {#server-endpoints}

| 方法 | 端点 | 说明 |
| --- | --- | --- |
| GET | `/.well-known/agent-card.json` | 1.0 发现 |
| GET | `/.well-known/agent.json` | 旧发现 |
| POST | `/` | JSON-RPC |

### A2A 1.0 JSON-RPC 方法 {#a2a-1-0-json-rpc-methods}

| 方法 | 说明 |
| --- | --- |
| `SendMessage` | 发送／继续消息 |
| `SendStreamingMessage` | 标准 JSON-RPC SSE |
| `GetTask` | 任务状态，支持 historyLength |
| `ListTasks` | 游标分页和字段控制 |
| `CancelTask` | 取消任务 |
| `SubscribeToTask` | 订阅未终止任务 |
| `CreateTaskPushNotificationConfig` | 创建 webhook |
| `GetTaskPushNotificationConfig` | 获取配置 |
| `ListTaskPushNotificationConfigs` | 列出配置 |
| `DeleteTaskPushNotificationConfig` | 删除配置 |
| `GetExtendedAgentCard` | 认证后的扩展 Card |

请求可以通过 `A2A-Version: 1.0` 使用 1.0 协议。不填写版本或指定 `0.3` 时保留旧行为，其他版本会返回 `VersionNotSupportedError`。`WithA2AExtensions` 配置必需扩展；`WithExtendedAgentCard` 和 `WithAuth` 用于配置需要认证才能访问的扩展 Agent Card。

### 旧 JSON-RPC 方法 {#legacy-json-rpc-methods}

| 方法 | 参数 | 说明 |
| --- | --- | --- |
| `tasks/send` | SendTaskRequest | 同步任务 |
| `tasks/sendSubscribe` | SendTaskRequest | SSE 任务 |
| `tasks/get` | GetTaskRequest | 获取状态 |
| `tasks/cancel` | CancelTaskRequest | 取消 |
| `tasks/pushNotification/set` | SetPushNotificationRequest | 设置 webhook |
| `tasks/pushNotification/get` | `{id}` | 获取 webhook |

### Part 类型 {#part-types}

文本、结构化数据、Base64 文件和 URI 文件使用不同构造器：

<!--@include: ../../../a2a/README.md{287,299}-->

## 任务状态 {#task-states}

任务提交后处于 `submitted`，开始执行后进入 `working`，成功结束时进入 `completed`。需要更多输入时进入 `input-required`，补充输入后可以继续；任务也可能以 `canceled` 或 `failed` 结束。

## 测试 {#testing}

```sh
go test ./a2a/ -v
```

## 参考 {#references}

- [A2A 协议规范](https://a2a-protocol.org/)
- [A2A GitHub](https://github.com/google/A2A/)
- [Go API](https://pkg.go.dev/github.com/covoyage/covonaut/a2a)