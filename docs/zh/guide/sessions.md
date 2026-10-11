# 会话与 HTTP {#sessions-http}

## 选择存储方式 {#choose-persistence}

| 存储 | 用途 |
| --- | --- |
| `store.NewFileStore` | 按 ID 保存 Agent 状态快照 |
| `session.NewFileStore` | 可分支的追加式 JSONL 会话树 |
| `session.NewAgentStore` | 将 JSONL 会话适配到 Agent 状态接口 |

配置 `Config.Store` 后，可以用同一个 key 保存和加载 Agent 状态。如果需要从执行中的某个时点恢复，则使用检查点机制，通过 `Config.Checkpoint` 配置。

会话树支持从已有消息创建分支，开始另一条对话，同时保留原来的历史。`MessagesOnPath` 返回当前分支路径上的消息，并处理其中的压缩记录。

## HTTP 服务 {#http-server}

[完整 HTTP 示例](../integrations/http)包含持久化、Bearer 认证、优雅关闭和 curl。

`server.New(cfg)` 创建支持 HTTP 和 SSE 的服务。可以将 `Handler()` 挂载到已有路由，也可以直接调用 `ListenAndServe`。配置 Store 后，`/api/chat` 按 `thread_id` 加载和保存状态；使用会话 AgentStore 时，未提供 ID 的请求可以创建新线程。

```json
{"message":"Summarize our conversation","thread_id":"thread-123","stream":false}
```

## 线程端点 {#thread-endpoints}

以下端点要求线程存储支持会话操作：

| 方法 | 端点 | 用途 |
| --- | --- | --- |
| POST／GET | `/api/threads` | 创建／列出 |
| GET／DELETE | `/api/threads/{key}` | 获取／删除 |
| POST | `/api/threads/{key}/branch` | 从当前叶或 `entry_id` 分支 |
| GET／PUT | `/api/threads/{key}/config` | 读取／保存调用配置 |
| GET／PUT | `/api/threads/{key}/thinking` | 读取／保存思考设置 |

同一项配置同时出现在多处时，请求中的值优先，其次是线程配置，最后是服务默认值。不支持会话操作的 `agentcore.Store` 仍可使用通用状态端点。

::: warning 服务需要单独部署
GitHub Pages 只托管文档，不运行 Go 服务。对外开放服务前，应配置身份认证、权限检查、请求限制和 CORS，并隔离工具执行环境。
:::

API：[session](https://pkg.go.dev/github.com/covoyage/covonaut/session)、[store](https://pkg.go.dev/github.com/covoyage/covonaut/store)、[server](https://pkg.go.dev/github.com/covoyage/covonaut/server)。

## 会话分支 {#branch-a-conversation}

```go
manager, err := sessions.Create(ctx, session.CreateOptions{Cwd: "/workspace"})
if err != nil {
    return err
}
if err := manager.AppendMessage(ctx, agentcore.Message{Role: agentcore.RoleUser, Content: "Plan A"}); err != nil {
    return err
}
branchPoint := manager.LeafID()
if err := manager.AppendMessage(ctx, agentcore.Message{Role: agentcore.RoleAssistant, Content: "First plan"}); err != nil {
    return err
}
if err := manager.Branch(branchPoint); err != nil {
    return err
}
if err := manager.AppendMessage(ctx, agentcore.Message{Role: agentcore.RoleUser, Content: "Try a different plan"}); err != nil {
    return err
}
messages := manager.MessagesOnPath()
```

示例中的 `sessions` 是 `session.FileStore`。`Branch` 改变当前分支的末端位置，不删除其他分支。可以通过 `Entries`、`GetTree`、`Leaves` 和 `Stats` 查看会话结构，用标签和会话名帮助查找。

JSONL 文件包含多个分支，不能把全部条目按顺序当作一段对话发送给模型。会话文件与 Agent 快照使用不同的版本号；读取时应使用包提供的加载器，不要直接修改文件来绕过版本检查。

## 存储策略 {#storage-policy}

本地文件存储适合单机使用，不具备分布式存储的协调能力。应用需要管理文件归属、访问权限、备份、跨进程锁和保留期限，并提供删除方式。历史可能包含凭据、工具输出和图片，应按用户数据保护。