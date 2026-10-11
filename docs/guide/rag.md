# Retrieval Interfaces

`components` defines RAG contracts; it does not ship a complete vector database,
retrieval engine, document splitter, or embedding service.

## Contracts

| Interface | Responsibility |
| --- | --- |
| `Loader.Load` | Load documents from a source |
| `Embedder.Embed` | Convert texts into corresponding vectors |
| `Indexer.Index` | Persist documents into a searchable index |
| `Retriever.Retrieve` | Retrieve documents for a query |

`Document` carries ID, content, and metadata. `Source` is an implementation-defined
identifier. The embedding contract requires one vector per supplied text in
matching order; your implementation must also keep dimensions and model versions
consistent with the index.

## Compose a Retrieval Tool

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

Here `retriever` is your `components.Retriever` implementation. Limit result
count, content size, and allowed sources there. Register the tool with an agent,
or compose retrieval and answering as explicit workflow stages when that order
must be deterministic.

## Keep Data Policy Outside the Prompt

Enforce tenant and document access before results reach model context. Retrieved
content can contain malicious instructions; keep it separate from system policy.
Preserve document IDs and provenance so answers can be audited and sources can
be cited.

Design indexing, deletion, refresh, and embedding-version migration as host
operations. Context compaction is not a substitute for a retrieval index, and
the embedding tool selector ranks tools rather than providing document RAG.