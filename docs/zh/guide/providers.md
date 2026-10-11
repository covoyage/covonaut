# 模型服务与输出 {#providers-output}

## 选择适配器 {#select-an-adapter}

| 包 | 对接的服务或 API |
| --- | --- |
| `provider/chatcompat` | OpenAI Chat Completions 或 Responses，通过 `Protocol` 选择 |
| `provider/anthropic` | Anthropic Messages |
| `provider/gemini` | Gemini 原生 REST |
| `provider/bedrock` | AWS Bedrock Converse / ConverseStream |
| `provider/vertex` | Google Cloud Vertex AI 上的 Gemini 或 Anthropic |
| `provider/copilot` | OAuth 认证的 Copilot chat |
| `provider/mistral` | Mistral Conversations |

这些适配器都实现 `agentcore.Provider`，但凭据格式、可用模型和支持功能并不相同。有些构造函数会返回错误，并要求填写相应云服务的配置。

接入时应根据服务提供的 API 协议选择，而不只是看厂商名称。
下面的配置片段是独立的备选方案，选择一项即可。使用前导入对应的
`github.com/covoyage/covonaut/provider/<name>` 包和 `os`。

## Chat Completions 兼容性 {#chat-completions-compatibility}

```go
provider := chatcompat.New(chatcompat.Config{
    APIKey:   os.Getenv("OPENAI_API_KEY"),
    BaseURL:  os.Getenv("OPENAI_BASE_URL"),
    Protocol: chatcompat.APIProtocolChat,
})
```

不填写 `BaseURL` 时使用 OpenAI。接入 DeepSeek、通义千问、Moonshot 等兼容服务时，填写对应的 API Key 和 API 地址。
该协议使用 `/chat/completions` 接口；修改 `BaseURL` 不会切换 API 协议。

`ExtraHeaders`、`EndpointPath`、`PrepareMessages`、`BuildExtraBody` 和 `DisableSystemPrompt` 用于适配不同服务的请求格式。宣称协议兼容的服务也可能不支持某些参数，需要按实际接口验证。

## OpenAI Responses API {#openai-responses-api}

```go
provider := chatcompat.New(chatcompat.Config{
    APIKey:   os.Getenv("OPENAI_API_KEY"),
    BaseURL:  os.Getenv("OPENAI_BASE_URL"),
    Protocol: chatcompat.APIProtocolResponses,
})
```

这会使用 `/responses` 接口，以及 Responses 的请求、响应和流式事件格式。
服务必须支持 Responses API，仅兼容 Chat Completions 并不足够。
`EndpointPath` 自定义配置只用于 Chat Completions。

## 使用所选 Provider 运行 Agent {#run-with-the-selected-provider}

选择一个配置片段后，将 `provider` 传给 Agent。使用以下代码时，导入
`agentcore`、`fmt` 和 `os`，将片段放在返回错误的函数内，并传入有超时限制的 `ctx`。
把 `MODEL` 设置为服务支持的模型 ID；Bedrock 使用模型或推理配置文件的 ID。

```go
agent, err := agentcore.NewWithError(agentcore.NewConfig(
    agentcore.WithModel(os.Getenv("MODEL")),
    agentcore.WithProvider(provider),
))
if err != nil {
    return err
}
defer agent.Close()

out, err := agent.Run(ctx, "Hello")
if err != nil {
    return err
}
fmt.Println(out)
```

无论选择哪种协议，Agent 的调用方式都相同。适配器负责将消息、工具调用和流式响应转换为对应服务的协议格式。

## 结构化输出与思考 {#structured-output-and-thinking}

用 `WithResponseFormat(...)` 和 `NewJSONSchemaResponseFormat` 指定响应结构，再通过 `RunStructured[T]` 将结果解码为 Go 类型。使用结果前必须检查错误。

| 适配器 | 结构化输出 | 思考配置 |
| --- | --- | --- |
| Chat Completions | 原生 `response_format`，依赖服务／模型 | 此适配器不映射 `Thinking` |
| Anthropic | Schema 提示词回退及 JSON 提取 | 映射到服务设置，受模型限制 |
| Gemini | 原生 MIME／schema | 映射到 `thinkingConfig`，受模型限制 |

表中只列出部分适配器。其他适配器及 Responses API 的行为应以实现和兼容测试为准。通过提示词要求模型返回 JSON，与服务原生约束输出结构不是一回事。具体用法见[结构化输出](./structured-output)和[消息](./messages)。

## 多模态消息 {#multimodal-messages}

消息块可以保留文本、图片和思考内容，适配器将图片 URL 或 data URL 转换为服务接受的格式。不同服务支持的地址类型、文件大小和模型能力不同。见[适配器索引](../reference#providers)和[兼容测试](https://github.com/covoyage/covonaut/tree/main/example/provider-compat)。

## 原生适配器配置 {#native-adapter-setup}

使用以下片段前，需要导入对应的适配器包和 `os`。模型名称统一设置在 `agentcore.Config` 中。

### Anthropic 和 Gemini {#anthropic-and-gemini}

```go
anthropicProvider := anthropic.New(anthropic.Config{APIKey: os.Getenv("ANTHROPIC_API_KEY")})
geminiProvider := gemini.New(gemini.Config{APIKey: os.Getenv("GEMINI_API_KEY")})
```

模型名称应使用服务提供的标识。思考预算和最大输出 token 数需要符合模型限制。创建 Provider 不会验证账户是否能访问该模型，仍需处理实际调用中的错误。

### AWS Bedrock {#aws-bedrock}

```go
provider, err := bedrock.New(bedrock.Config{
    Region: os.Getenv("AWS_REGION"),
    AccessKey: os.Getenv("AWS_ACCESS_KEY_ID"),
    SecretKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
    SessionToken: os.Getenv("AWS_SESSION_TOKEN"),
})
if err != nil {
    return err
}
```

当前适配器要求传入凭据，不会自动使用 AWS SDK 的完整凭据查找机制。区域和模型必须对当前账户开放；使用临时凭据时，应用还需负责刷新。

### Vertex AI {#vertex-ai}

```go
provider, err := vertex.NewGemini(vertex.Config{
    Project: os.Getenv("GOOGLE_CLOUD_PROJECT"),
    Region: os.Getenv("GOOGLE_CLOUD_REGION"),
    AccessToken: os.Getenv("VERTEX_ACCESS_TOKEN"),
})
if err != nil {
    return err
}
```

在 Vertex 上使用 Anthropic Messages 时，改用下面的配置：

```go
provider, err := vertex.NewAnthropic(vertex.Config{
    Project: os.Getenv("GOOGLE_CLOUD_PROJECT"),
    Region: os.Getenv("GOOGLE_CLOUD_REGION"),
    AccessToken: os.Getenv("VERTEX_ACCESS_TOKEN"),
})
if err != nil {
    return err
}
```

`NewGemini` 使用 Gemini 原生内容接口，`NewAnthropic` 通过 Vertex 的原始预测接口发送 Anthropic Messages 格式的请求。
选择所用模型开放的区域，两者都通过访问令牌认证。
当前实现使用传入的 token，或执行 `gcloud auth print-access-token` 获取。正式部署建议明确传入 token，不应假设支持自动读取 ADC 或自动刷新凭据。

### Copilot 和 Mistral {#copilot-and-mistral}

```go
copilotProvider, err := copilot.New(copilot.Config{Token: os.Getenv("COPILOT_GITHUB_TOKEN")})
if err != nil {
    return err
}
mistralProvider := mistral.New(mistral.Config{APIKey: os.Getenv("MISTRAL_API_KEY")})
```

Copilot 也可以从环境变量或 `gh auth token` 获取 token，但普通 GitHub token 不一定有权调用该服务。Mistral 适配器对接 Conversations API；如果要调用 Chat Completions 端点，应使用 `chatcompat`。

## 实现 Provider {#implement-a-provider}

自定义 Provider 需要实现 `Complete` 和 `Stream`，将服务响应转换为统一的文本、工具调用、内容块、用量和错误。流式输出结束时关闭通道，并通过 delta 返回终止错误；不能因为已经生成部分文本就忽略失败。

实现时应响应 context 取消，保持工具 ID 一致，并测试响应格式错误和流中途失败的情况。本站离线示例只是测试替身，不是完整的生产 HTTP 适配器。