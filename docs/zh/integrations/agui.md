# AG-UI 事件流 {#ag-ui-event-streaming}

AG-UI 向 Web 客户端传输 Agent 的执行事件，但不包含浏览器应用。Go 服务需要单独部署，不能在 GitHub Pages 上运行。

## 挂载 Handler {#mount-a-handler}

```go
http.Handle("/agent", agui.NewHandler(cfg))
```

GET 请求用于查询服务能力，POST 请求用于发起运行。应用应在 Handler 外添加身份认证、权限检查、请求大小和预算限制。中间件包装响应写入器时，需要保留 SSE 使用的 `http.Flusher` 接口。

## 请求与响应 {#request-and-response}

```json
{
  "threadId": "conversation-123",
  "runId": "run-456",
  "messages": [{"id":"user-1","role":"user","content":"Hello"}]
}
```

其他字段见 `RunAgentInput`。当前 Handler 使用请求末尾的用户消息发起运行，不会将客户端传来的任意历史直接作为完整服务端历史。需要跨请求保留线程时，应配置 store。

SSE 包含生命周期、文本、思考、工具执行、状态和自定义事件。客户端需要处理结束及错误事件，不能根据 HTTP 连接成功判断 Agent 已成功完成任务。

## 快照与重连 {#snapshots-and-reconnection}

保存过的线程可以发送消息快照，客户端传入的状态也可以形成状态快照。这些快照用于同步界面，与恢复执行所用的持久检查点不同。

事件总线可能丢失消息，因此重连后需要根据保存的历史同步状态。尚未提交的流式文本只是临时内容；发生重试时，客户端应处理 reset，撤回失败尝试的输出，避免出现重复回答。

## 恢复与客户端工具 {#resume-and-client-tools}

当前实现将 resume 输入转换为引导消息，不会替应用验证批准记录，也不会直接完成 `Agent.Resume` 的审核恢复流程。客户端传入的 tool 和 state 字段不会自动注册服务端工具或应用全部调用设置，需要这些行为时应自行实现。

## A2UI 绑定 {#a2ui-binding}

A2UI 的界面更新可以通过 AG-UI 自定义事件发送。客户端需要实现组件目录对应的渲染器和传输适配，收到事件后才会生成控件。

见 [AG-UI API](https://pkg.go.dev/github.com/covoyage/covonaut/agui) 和[事件语义](../guide/observability)。