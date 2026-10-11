# Providers & Output

## Select an Adapter

| Package | API family |
| --- | --- |
| `provider/chatcompat` | OpenAI Chat Completions or Responses, selected with `Protocol` |
| `provider/anthropic` | Anthropic Messages |
| `provider/gemini` | Gemini native REST |
| `provider/bedrock` | AWS Bedrock Converse / ConverseStream |
| `provider/vertex` | Gemini or Anthropic on Google Cloud Vertex AI |
| `provider/copilot` | OAuth-authenticated Copilot chat |
| `provider/mistral` | Mistral Conversations |

Adapters share the `agentcore.Provider` interface, but authentication, model
availability, and supported features differ. Consult each package's constructors;
some return errors and require cloud-specific configuration.

Choose by the API protocol exposed by your service, not just its vendor name.
The setup fragments below are independent alternatives. Import the selected
package from `github.com/covoyage/covonaut/provider/<name>` and `os`.

## Chat Completions Compatibility

```go
provider := chatcompat.New(chatcompat.Config{
    APIKey:   os.Getenv("OPENAI_API_KEY"),
    BaseURL:  os.Getenv("OPENAI_BASE_URL"),
    Protocol: chatcompat.APIProtocolChat,
})
```

An empty base URL selects OpenAI. For compatible services such as DeepSeek,
Qwen or Moonshot, supply their API key and base URL. This protocol uses
`/chat/completions`; changing `BaseURL` does not switch API protocols.

Service-specific adjustments include `ExtraHeaders`, `EndpointPath`,
`PrepareMessages`, `BuildExtraBody`, and `DisableSystemPrompt`. Protocol
compatibility alone does not guarantee support for every parameter or model.

## OpenAI Responses API

```go
provider := chatcompat.New(chatcompat.Config{
    APIKey:   os.Getenv("OPENAI_API_KEY"),
    BaseURL:  os.Getenv("OPENAI_BASE_URL"),
    Protocol: chatcompat.APIProtocolResponses,
})
```

This selects `/responses` and its request, response and streaming event formats.
The service must support the Responses API; Chat Completions compatibility
alone is insufficient. `EndpointPath` customizations apply only to Chat Completions.

## Run with the Selected Provider

After choosing one setup fragment, pass `provider` to the Agent. Import
`agentcore`, `fmt` and `os`, and run this fragment inside an error-returning
function with a bounded `ctx`. Set `MODEL` to a model ID your service supports;
for Bedrock, use the model or inference profile ID.

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

The Agent uses the same interface for every protocol. Each adapter translates
messages, tools and streaming responses into the service's wire format.

## Structured Output and Thinking

See [structured output](./structured-output) for a complete typed example and
[messages](./messages) for multimodal history and conversion rules.

Use `WithResponseFormat(...)` with `NewJSONSchemaResponseFormat` to request a
schema-shaped response. `RunStructured[T]` decodes output into a typed result;
always check its error before using the value.

| Adapter | Structured output | Thinking configuration |
| --- | --- | --- |
| Chat Completions | Native `response_format`, subject to service/model support | `Thinking` is not mapped by the Chat Completions adapter |
| Anthropic | Schema-aware prompt fallback and JSON extraction | Mapped to provider thinking settings; model restrictions apply |
| Gemini | Native response MIME type/schema | Mapped to `thinkingConfig`; model restrictions apply |

This is a focused matrix, not a guarantee across all adapters. For other adapters
and the Responses API, check their implementation and compatibility tests before
depending on a feature. Prompt-based JSON generation is not equivalent to native
schema enforcement.

## Multimodal Messages

Message blocks preserve text, images, and thinking segments. Image URLs and data
URLs are translated by the adapter into provider-specific payloads. Accepted URI
schemes, file sizes, and model capabilities are provider-dependent.

See the [provider package index](../reference#providers) and
[compatibility tests](https://github.com/covoyage/covonaut/tree/main/example/provider-compat).

## Native Adapter Setup

The following fragments assume the corresponding provider package and `os` are
imported. Set the model in `agentcore.Config` for each adapter.

### Anthropic and Gemini

```go
anthropicProvider := anthropic.New(anthropic.Config{
    APIKey: os.Getenv("ANTHROPIC_API_KEY"),
})
geminiProvider := gemini.New(gemini.Config{
    APIKey: os.Getenv("GEMINI_API_KEY"),
})
```

Use each service's model identifiers. Thinking effort/budget and output tokens
are subject to model restrictions. A provider object does not verify account
access at construction; handle errors from the actual call.

### AWS Bedrock

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

This adapter takes explicit credentials; do not assume the AWS SDK's entire
credential-provider chain is active. Use a region/model your account can access
and refresh temporary credentials in your application when required.

### Vertex AI

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

For Anthropic Messages on Vertex, choose this alternative instead:

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

`NewGemini` uses Gemini's native content API; `NewAnthropic` uses Vertex's
raw prediction endpoints with Anthropic Messages payloads. Choose a region
where the selected model is available. Both use bearer-token authentication.
Current token resolution uses the
explicit token or invokes `gcloud auth print-access-token`. Supply the token
explicitly for controlled deployments; do not infer automatic ADC loading or
automatic token refresh from a generic authentication error message.

### Copilot and Mistral

```go
copilotProvider, err := copilot.New(copilot.Config{
    Token: os.Getenv("COPILOT_GITHUB_TOKEN"),
})
if err != nil {
    return err
}
mistralProvider := mistral.New(mistral.Config{
    APIKey: os.Getenv("MISTRAL_API_KEY"),
})
```

Copilot can also resolve a token from environment or `gh auth token`. Having a
GitHub token does not by itself guarantee endpoint entitlement. Check service
permissions and usage requirements. Mistral uses its Conversations adapter;
if targeting a Chat Completions endpoint instead, choose `chatcompat` explicitly.

## Implement a Provider

Implement `Complete` and `Stream` from `agentcore.Provider`. Return normalized
content, tool calls, blocks, usage, and errors. A streaming channel must close
on completion and expose terminal failures through deltas; do not discard a
failure just because earlier text was emitted.

Respect context cancellation, translate tool IDs consistently, and test malformed
responses and mid-stream errors. The offline examples on this site are minimal
test doubles, not templates for a production HTTP adapter.