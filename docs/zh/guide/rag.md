# 检索接口 {#retrieval-interfaces}

`components` 定义检索增强生成（RAG）所需的接口，不包含完整的向量数据库、检索引擎、文档切分实现或向量计算服务。

## 接口约定 {#contracts}

| 接口 | 职责 |
| --- | --- |
| `Loader.Load` | 从来源加载文档 |
| `Embedder.Embed` | 将文本按顺序转换为向量 |
| `Indexer.Index` | 将文档持久化到索引 |
| `Retriever.Retrieve` | 按查询检索文档 |

`Document` 包含 ID、内容和元数据，`Source` 的含义由具体实现约定。生成的向量应与输入逐项对应、顺序一致，其维度和模型版本也需要与索引兼容。

## 组合检索工具 {#compose-a-retrieval-tool}

```go
retrieval := &agentcore.Tool{
    Name: "retrieve", Description: "Find relevant approved documents",
    Parameters: map[string]any{
        "type": "object",
        "properties": map[string]any{"query": map[string]any{"type": "string"}},
        "required": []string{"query"},
    },
    Func: func(ctx context.Context, raw json.RawMessage) (any, error) {
        var input struct { Query string `json:"query"` }
        if err := json.Unmarshal(raw, &input); err != nil {
            return nil, err
        }
        return retriever.Retrieve(ctx, input.Query)
    },
}
```

`retriever` 由应用提供，需要限制返回数量、内容大小和可访问来源。将工具注册到 Agent 后，由模型决定何时调用；如果要求每次都先检索再回答，应将检索写成固定的工作流步骤。

## 数据策略不依赖提示词 {#keep-data-policy-outside-the-prompt}

将检索内容发送给模型之前，应检查用户所属租户及文档访问权限。文档可能包含恶意指令，不应当作系统指令执行。保留文档 ID 和来源，便于审计和引用。

应用负责建立和刷新索引、删除文档，以及在更换向量模型时迁移数据。上下文压缩不会创建检索索引，基于向量的工具选择也不能代替文档检索。