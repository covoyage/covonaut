# Structured Output

## Request, Decode, Validate

Three operations are distinct:

1. Request JSON or a JSON Schema response through `ResponseFormat`.
2. Decode the returned JSON into Go values.
3. Validate schema and application-specific rules before using those values.

`NewJSONObjectResponseFormat` requests a JSON object. `NewJSONSchemaResponseFormat`
adds the schema contract. Actual enforcement depends on the provider: a native
schema feature is stronger than a prompt instruction, but does not replace your
application's semantic validation.

`RunStructured[T]` calls `Run` and JSON-decodes its final text. It does not
automatically derive a schema from `T` or perform business validation. Go's JSON
decoder can leave missing fields at their zero value; inspect the resulting
values deliberately.

## Complete Offline Example

This provider returns fixed JSON so the example can be compiled without any
external account. It demonstrates API composition, not native schema enforcement
by a remote model.

```go
package main

import (
    "context"
    "errors"
    "fmt"
    "log"

    "github.com/covoyage/covonaut/agentcore"
)

type answer struct {
    Value string `json:"value"`
}

type jsonProvider struct{}

func (jsonProvider) Complete(context.Context, *agentcore.ProviderRequest) (*agentcore.ProviderResponse, error) {
    return &agentcore.ProviderResponse{Content: `{"value":"ready"}`}, nil
}
func (jsonProvider) Stream(context.Context, *agentcore.ProviderRequest) (<-chan agentcore.StreamDelta, error) {
    return nil, errors.New("streaming is not used in this example")
}

func main() {
    if err := run(); err != nil {
        log.Fatal(err)
    }
}

func run() error {
    agent, err := agentcore.NewWithError(agentcore.NewConfig(
        agentcore.WithModel("offline-json"),
        agentcore.WithProvider(jsonProvider{}),
        agentcore.WithResponseFormat(agentcore.NewJSONSchemaResponseFormat("answer", map[string]any{
            "type": "object",
            "properties": map[string]any{"value": map[string]any{"type": "string"}},
            "required": []string{"value"},
        })),
    ))
    if err != nil {
        return err
    }
    defer agent.Close()
    result, err := agentcore.RunStructured[answer](context.Background(), agent, "Return a status.")
    if err != nil {
        return err
    }
    if result.Value == "" {
        return errors.New("status must not be empty")
    }
    fmt.Println(result.Value)
    return nil
}
```

## Keep Raw Output When Needed

`RunStructuredInto` returns raw output while decoding into a destination.
`ContinueStructured` and `ContinueStructuredInto` continue existing history.
`DecodeStructured` and `DecodeStructuredInto` decode a response obtained through
another path. Preserve raw output only when your data-retention policy permits.

For provider-level calls, `ProviderRequest.ResponseFormat` supplies the request
and `ProviderResponse.Structured` can contain valid parsed JSON. It is not
proof that all your application rules passed.

## Failure Handling

Malformed or empty JSON is a decoding error. An interrupted run can return no
final JSON and should be handled as an interrupted workflow, not as an ordinary
answer. Retries or corrective prompts need finite budgets and should not repeat
side-effecting tools unintentionally.

See [provider compatibility](./providers#structured-output-and-thinking) and
[human review](./recovery).