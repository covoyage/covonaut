# Messages & Multimodal Content

## Message Structure

`Message` keeps roles, content, tool calls, tool-call IDs, metadata, cache markers,
blocks, and optional invocation IDs. `Content` remains useful for text-only
applications; `Blocks` preserve richer segments rather than flattening them into
an ambiguous transcript string.

| Field | Purpose |
| --- | --- |
| `Role` | System, user, assistant, or tool |
| `Content` | Legacy text content |
| `ToolCalls` | Assistant-requested functions with stable call IDs |
| `ToolCallID` | Result-to-call correlation |
| `Blocks` | Text, images, thinking segments, or tool-call content |
| `Type` | Standard, compaction, branch summary, or custom variant |
| `ID` | Optional stable identity for state merge/replacement |

`Message.Clone` copies supported message fields and common nested metadata.
Do not assume arbitrary objects embedded in metadata become independent deep
copies; avoid sharing mutable host objects through messages.

## Build Image Inputs

```go
message := agentcore.Message{
    Role: agentcore.RoleUser,
    Content: "Describe the architecture in this image.",
}.AppendImageURLBlock("https://example.com/architecture.png")
agent.State().AddMessage(message)
output, err := agent.Continue(ctx)
```

This fragment assumes an initialized agent with a model supporting images. The
manual state append does not itself execute the agent's message-persistence
lifecycle hooks. `Run` accepts text; use an intentional message/state boundary
or provider request when building a multimodal input workflow.

Data URLs can carry supported encoded images; providers differ in how they
accept HTTP URLs, inline data, and cloud/file URIs. Never assume a URL that works
with one adapter is accepted by another. Apply source allowlists, size limits,
and sensitive-data policy before creating blocks.

## Display and Provider Conversion

`MessageTextBody` formats content for inspection, including representations of
thinking, images, and tool calls. It is not a canonical provider payload.
`MessageCollapseForLLM` and `DefaultConvertToLLM` prepare provider-facing content
while retaining supported rich blocks and avoiding mirrored-text duplication.

Default conversion skips `MessageTypeCustom` and converts compaction/branch
summaries into standard content. Use `Config.ConvertToLLM` when custom message
types need a different policy.

`TransformContext` runs before conversion and tool-history normalization. Keep
transformations side-effect-free where possible, and avoid modifying stored
history accidentally through shared slices or maps.

## Tool Pairing

Providers commonly require each assistant tool call to have a corresponding
tool result. Before requests, `NormalizeToolCallHistory` inserts synthetic
interrupted results for missing pairs. This repairs protocol structure, not the
external operation itself; it cannot determine whether a remote write occurred.

Keep call IDs and result IDs intact through storage, branches, and transforms.
If migrating an older transcript, test the next provider request, not just its
appearance in a chat window.

## Thinking Blocks

Thinking-related fields are provider-native data and may include signatures or
summaries. Decide what your UI displays and stores. Do not treat every thinking
block as ordinary assistant text, expose it unintentionally in telemetry, or
assume all providers accept it when replaying history across model families.