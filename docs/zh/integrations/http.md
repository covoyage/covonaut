# HTTP API {#http-api}

## 完整服务 {#complete-server}

示例将 Handler 挂载到应用自己的 HTTP 服务，用 Bearer token 保护全部路由，并禁用技能管理接口。聊天请求设有大小限制，线程保存为 JSONL 文件，服务仅监听本机回环地址。

```sh
export OPENAI_API_KEY='your-provider-key'
export AGENT_MODEL='your-model'
export CHAT_API_TOKEN='your-local-access-token'
go run ./example/http-server
```

`SESSION_DIR` 默认为 `./sessions`。使用其他兼容模型服务时，可以设置 `OPENAI_BASE_URL`。实际部署应从安全的配置来源读取 token，避免将它留在 Shell 历史或源码中。

<<< @/../example/http-server/main.go

## 聊天请求 {#chat-requests}

```sh
curl http://127.0.0.1:8080/api/chat \
  -H "Authorization: Bearer $CHAT_API_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"message":"Hello","stream":false}'
```

未提供线程 ID 时，会话存储会创建新线程。后续请求携带返回的 `thread_id`，即可继续对话。将 `stream` 设为 true 可以接收 SSE，客户端需要解析这些事件；本地测试时可用 `curl -N`。

每次请求可以设置 `model`、`response_format`、`thinking` 和 `skills`。请求配置优先于线程配置，线程配置优先于服务默认值。省略字段不一定会清除已经保存的值。

## 线程、分支与状态 {#threads-branches-and-state}

[会话指南](../guide/sessions#thread-endpoints)列出了线程端点。创建分支时使用 `entry_id`，不要把数组下标当作条目 ID。读取、修改或清除线程配置及思考设置时，应遵循对应端点的约定。

`/api/states` 用于访问状态，与线程接口分开。`/agui/{path}` 接受 [AG-UI](./agui) 的请求格式，不使用上面的 ChatRequest。

## 安全与生命周期 {#security-and-lifetime}

框架不提供完整的身份认证服务。示例使用的共享 token 只适合本地单用户环境。生产环境应将线程与用户绑定，逐线程检查权限，并限制用户可选择的模型及调用配额。

CORS 不能代替身份认证。应明确配置允许的来源，不要通过代理公开所有状态。`SetMaxRequestBodyBytes` 不一定作用于单独挂载的 AG-UI Handler，应用需要为全部路由设置请求大小和速率限制。

挂载 Handler 时，由应用负责关闭流程：先等待正在处理的请求结束，再释放资源。代理的缓冲和超时设置需要适合 SSE。模型和工具也应设置各自的执行超时，HTTP 读取超时不能限制模型推理时间。