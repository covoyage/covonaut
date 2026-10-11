# 结构化输出 {#structured-output}

## 请求、解码、验证 {#request-decode-validate}

使用结构化输出时，需要分别完成三个步骤：

1. 用 `ResponseFormat` 请求 JSON 或 JSON Schema 响应。
2. 将返回 JSON 解码为 Go 值。
3. 在使用前验证 schema 和业务规则。

`NewJSONObjectResponseFormat` 要求返回 JSON 对象，`NewJSONSchemaResponseFormat` 进一步指定结构。服务是否严格约束输出，取决于 Provider。原生 JSON Schema 支持比单纯在提示词中要求 JSON 更可靠，但格式正确仍不代表内容符合业务要求。

`RunStructured[T]` 调用 `Run`，再将最终文本解码为 `T`。它不会根据 `T` 自动生成 schema，也不会检查业务规则。JSON 中缺少的字段可能保留 Go 零值，需要显式验证。

## 完整离线示例 {#complete-offline-example}

示例中的 Provider 返回固定 JSON，无需外部账户。它演示如何组合这些 API，不验证远程模型是否严格遵守 schema。

<!--@include: ../../guide/structured-output.md{27,82}-->

## 按需保留原始输出 {#keep-raw-output-when-needed}

`RunStructuredInto` 在解码结果的同时返回原始输出。继续已有对话时，可用 `ContinueStructured` 或 `ContinueStructuredInto`；解码其他方式取得的响应时，可用 `DecodeStructured` 或 `DecodeStructuredInto`。保存原始内容前应确认符合应用的数据保留策略。

直接调用 Provider 时，通过 `ProviderRequest.ResponseFormat` 指定格式。`ProviderResponse.Structured` 可能包含已经解析的 JSON，但应用仍需验证业务规则。

## 失败处理 {#failure-handling}

格式错误或空的 JSON 会导致解码失败。Agent 中断时可能尚未生成最终 JSON，应先处理中断。通过重试或追加提示要求模型修正时，需要限制次数，并避免重复执行付款、写入等操作。

见[兼容性](./providers#structured-output-and-thinking)和[人工审核](./recovery)。