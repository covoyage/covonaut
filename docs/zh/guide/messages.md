# 消息与多模态内容 {#messages-multimodal-content}

## 消息结构 {#message-structure}

`Message` 保存角色、文本、工具调用及其 ID、元数据、缓存标记、内容块和可选的调用标识。纯文本可以放在 `Content` 中；图片、思考等不同类型的内容应放在 `Blocks` 中，而不是全部转成一段文字。

| 字段 | 用途 |
| --- | --- |
| `Role` | system、user、assistant 或 tool |
| `Content` | 传统文本 |
| `ToolCalls` | 带稳定 ID 的工具请求 |
| `ToolCallID` | 结果与调用关联 |
| `Blocks` | 文本、图片、思考和工具内容 |
| `Type` | 标准、压缩、分支摘要或自定义类型 |
| `ID` | 合并或替换状态时使用的可选消息标识 |

`Clone` 会复制支持的字段和常见嵌套元数据，但不保证深拷贝任意对象。不要假设消息副本中的自定义可变对象已与原对象完全分离。

## 构建图片输入 {#build-image-inputs}

```go
message := agentcore.Message{
    Role: agentcore.RoleUser,
    Content: "Describe the architecture in this image.",
}.AppendImageURLBlock("https://example.com/architecture.png")
agent.State().AddMessage(message)
output, err := agent.Continue(ctx)
```

示例需要一个已经配置好的 Agent，且模型支持图片。直接调用 `State().AddMessage` 不会自动触发消息持久化钩子。由于 `Run` 接受的是文本，多模态应用需要自行构造消息并管理状态，或直接构造 Provider 请求。

Data URL 可以携带编码后的图片。HTTP 地址、内联数据和云存储地址是否被接受，取决于 Provider。添加图片前应检查来源、文件大小和敏感内容。

## 展示与 Provider 转换 {#display-and-provider-conversion}

`MessageTextBody` 将消息整理成便于查看的文本，包括图片、思考和工具调用的文字表示，但这个结果不能直接当作标准模型请求。`MessageCollapseForLLM` 和 `DefaultConvertToLLM` 用于准备模型输入，保留支持的内容块，并避免同一文本在 `Content` 和 `Blocks` 中重复发送。

默认转换会跳过 `MessageTypeCustom`，并将压缩摘要和分支摘要转换为标准消息。如果需要发送自定义类型，可以设置 `Config.ConvertToLLM`。

`TransformContext` 在消息转换和工具历史整理之前运行。转换时应尽量只处理副本，避免通过共享的 slice 或 map 意外修改已保存的历史。

## 工具配对 {#tool-pairing}

模型服务通常要求 assistant 发起的每个工具调用都有对应结果。`NormalizeToolCallHistory` 会在请求前为缺少结果的调用补上一条中断说明。这只能修复消息格式，不能判断工具是否已经完成了外部操作。

保存、分支和转换消息时应保留调用与结果的 ID。迁移旧历史后，还应测试能否继续请求模型，不能只检查聊天界面是否显示正常。

## 思考块 {#thinking-blocks}

思考内容采用服务特定的格式，可能包含签名或摘要。应用应决定哪些内容展示、哪些保存，避免将其当作普通回答或直接写入日志。更换模型服务后，也不能假设原来的思考内容仍可直接发送。