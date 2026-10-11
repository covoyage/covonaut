# covonaut

[English](README.md) | [简体中文](README.zh-CN.md)

[![CI](https://github.com/covoyage/covonaut/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/covoyage/covonaut/actions/workflows/ci.yml)

Production-ready Agent framework for Go.

[Documentation](https://covoyage.github.io/covonaut/) | [API Reference](https://pkg.go.dev/github.com/covoyage/covonaut)

## Documentation

The documentation site is hosted on GitHub Pages:

- [English](https://covoyage.github.io/covonaut/)
- [Chinese](https://covoyage.github.io/covonaut/zh/)

The site selects a language based on your browser preferences and remembers
manual language selections. Both versions cover setup, core concepts, tools,
providers, protocols, and runnable examples.

To preview the documentation locally, use Node.js 22 and Go 1.25+:

```bash
npm --prefix docs ci
npm --prefix docs run dev
```

Run `npm --prefix docs run check` to build the site and validate internal links,
bilingual page consistency, and Go examples without calling model providers.

The [Documentation workflow](.github/workflows/docs.yml) checks pull requests
and deploys updates from `main`. To enable deployment, set **Settings > Pages >
Build and deployment > Source** to **GitHub Actions** in the repository. The
generated site is published from `docs/.vitepress/dist`.

## Install

```bash
go get github.com/covoyage/covonaut/agentcore
```

Requires **Go 1.25+**.

## Quick Start

```go
package main

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "log"
    "os"
    "time"

    "github.com/covoyage/covonaut/agentcore"
    "github.com/covoyage/covonaut/provider/chatcompat"
)

func main() {
    if err := run(); err != nil {
        log.Fatal(err)
    }
}

func run() error {
    apiKey, model := os.Getenv("OPENAI_API_KEY"), os.Getenv("AGENT_MODEL")
    if apiKey == "" || model == "" {
        return errors.New("set OPENAI_API_KEY and AGENT_MODEL before running")
    }

    agent, err := agentcore.NewWithError(agentcore.NewConfig(
        agentcore.WithName("assistant"),
        agentcore.WithModel(model),
        agentcore.WithProvider(chatcompat.New(chatcompat.Config{
            APIKey: apiKey,
            BaseURL: os.Getenv("OPENAI_BASE_URL"),
        })),
        agentcore.WithSystemPrompt("Use the greet tool when asked to greet someone."),
        agentcore.WithMaxTurns(5),
        agentcore.WithValidateArguments(true),
        agentcore.WithTools(&agentcore.Tool{
            Name:        "greet",
            Description: "Greet a person by name",
            Parameters: map[string]any{
                "type": "object",
                "properties": map[string]any{
                    "name": map[string]any{"type": "string"},
                },
                "required": []string{"name"},
            },
            Func: func(ctx context.Context, args json.RawMessage) (any, error) {
                var input struct {
                    Name string `json:"name"`
                }
                if err := json.Unmarshal(args, &input); err != nil {
                    return nil, err
                }
                return "Hello, " + input.Name + "!", nil
            },
        }),
    ))
    if err != nil {
        return err
    }
    defer agent.Close()

    ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
    defer cancel()
    output, err := agent.Run(ctx, "Greet Ada by name.")
    if err != nil {
        return err
    }
    fmt.Println(output)
    return nil
}
```

Set `OPENAI_API_KEY` and `AGENT_MODEL` to a tool-capable model available to your account.
For a complete checked example, run `go run ./example/quickstart` from this repository.

Quick Start is a complete program. The sections below are feature fragments:
provide imports, a bounded `ctx`, and referenced application values. Fragments
using `return err` belong inside a function returning `error`. Configuration
fragments produce `cfg`; initialize it with `agentcore.NewWithError(cfg)`, check
the error, and defer `agent.Close()` as shown above. Schema validation does not
replace authorization. Providers and models differ in supported capabilities.

## Core Concepts

### Agent Loop

The central `Agent` executes an LLM-tool loop with configurable max turns, automatic context compaction, and retry with exponential backoff.

```go
cfg := agentcore.NewConfig(
    agentcore.WithName("coder"),
    agentcore.WithModel(model),
    agentcore.WithProvider(provider),
    agentcore.WithMaxTurns(20),
    agentcore.WithContextWindow(128000),
)
cfg.CompressionThreshold = 0.8
cfg.RetryConfig = &agentcore.RetryConfig{MaxRetries: 3, BaseDelayMs: 1000}
```

### Tools

Quick Start includes a complete tool with typed argument decoding and enabled
Schema validation. Attach `Tool.Before` and `Tool.After` hooks or global
middleware for auditing and authorization. Validate permitted paths and actions
inside the tool; a valid Schema is not an authorization decision.

### Built-in Tools Extension

`covonaut` ships a comprehensive `tools` package providing filesystem, shell, search, browser, and code execution tools — all as a single pluggable `Extension`.

```go
import "github.com/covoyage/covonaut/tools"

ext := tools.NewExtension(tools.ExtensionConfig{WorkingDir: "/path/to/project"})
cfg := agentcore.Config{
    ModelConfig: agentcore.ModelConfig{Model: model, Provider: provider},
    Extensions: []agentcore.Extension{ext},
}
```

Each tool supports pluggable operations for delegating to remote systems (e.g. SSH).

The `bash` tool supports optional `AllowList` and `BlockList` for command filtering — word-boundary prefix match, sub-command aware via `&&`/`||`/`;`/`|` splitting. For strong isolation, delegate `BashOperations` to a sandbox rather than relying on the list alone.

### MCP Tools

`covonaut` can bridge external MCP servers into first-class `agentcore.Tool`s.
Supports MCP `stdio` transport and HTTP/SSE transport, plus `tools/list` / `tools/call`.

```go
import "github.com/covoyage/covonaut/mcp"

ext, err := mcp.NewStdioExtension(ctx, mcp.StdioConfig{
    Name:       "filesystem",
    Command:    "npx",
    Args:       []string{"-y", "@modelcontextprotocol/server-filesystem", "."},
    ToolPrefix: "fs.",
})
if err != nil {
    return err
}
defer ext.Dispose()

agent, err := agentcore.NewWithError(agentcore.Config{
    ModelConfig: agentcore.ModelConfig{
        Name: "assistant", Model: model, Provider: provider,
    },
    Extensions: []agentcore.Extension{ext},
})
if err != nil {
    return err
}
defer agent.Close()
```

- `NewStdioExtension(...)` eagerly initializes the MCP client, lists remote tools, and exposes them through `Config.Extensions`
- `ToolPrefix` is optional but recommended when combining multiple MCP servers to avoid tool-name collisions
- MCP tool execution errors (`isError: true`) are preserved as tool output instead of transport failures so the model can self-correct

### Multi-Agent Handoff

Delegate or transfer between specialized agents — local or remote via A2A.

```go
cfg := agentcore.Config{
    Handoffs: []agentcore.HandoffConfig{
        {Name: "math-expert", Agent: mathCfg, Mode: agentcore.HandoffDelegate},
        {Name: "code-expert", Agent: codeCfg, Mode: agentcore.HandoffTransfer},
    },
}
```

### Observability

Distributed tracing via a backend-agnostic `Tracer` interface (OpenTelemetry
adapter included), and runtime metrics via the complementary `Metrics`
interface. Set both on `agentcore.Config`; each is optional and nil-safe.

```go
cfg := agentcore.Config{
    ModelConfig: agentcore.ModelConfig{Model: model, Provider: provider},
    Tracer:      agentcore.NewOtelTracer(tp.Tracer("my-agent")),
    Metrics:     agentcore.NewOtelMetrics(mp.Meter("my-agent")),
}
cfg.Provider = agentcore.NewModelMetricsMiddleware(cfg.Metrics)(cfg.Provider)
```

`Tracer` emits correlated `agent`/`model`/`tool` spans (with `run.id` /
`component` attributes) for every run path. `Metrics` records GenAI-standard
token usage and duration for every LLM call, plus `agentcore.errors` counts by
component (`model`/`tool`/`agent`) — tool errors are injected into the executor
chain and agent-run failures are recorded automatically, so hosts get error
telemetry with no extra wiring. Wrap `Config.Provider` with
`agentcore.NewModelMetricsMiddleware(cfg.Metrics)` to count the model layer
(mid-stream terminal errors, e.g. repetition loops, are counted too).

### Events

Type-safe event bus for real-time observability.

```go
agent.On(agentcore.EventMessageDelta, func(e agentcore.Event) {
    delta := e.(*agentcore.MessageDeltaEvent)
    fmt.Print(delta.Delta)
})

agent.On(agentcore.EventToolCallEnd, func(e agentcore.Event) {
    tc := e.(*agentcore.ToolCallEndEvent)
    fmt.Printf("Tool %s completed\n", tc.ToolName)
})
```

### Lifecycle Hooks

Intercept every stage of agent execution.

```go
cfg := agentcore.Config{
    ModelConfig: agentcore.ModelConfig{Model: model, Provider: provider},
    Lifecycle: agentcore.LifecycleChain{
        &agentcore.GuardrailHook{Validate: safetyCheck},
        &agentcore.AuditHook{OnEvent: logEvent},
        &agentcore.RateLimitHook{MaxTurnsPerMinute: 20},
    },
}
```

### Runnable (4-mode generic)

All components implement the same `Runnable[I, O]` interface with auto-derivation:

| Constructor | You implement | Auto-derived |
|---|---|---|
| `NewInvokeRunnable` | `Invoke` | Stream, Collect, Transform |
| `NewStreamRunnable` | `Stream` | Invoke, Collect, Transform |
| `NewCollectRunnable` | `Collect` | Invoke, Stream, Transform |
| `NewTransformRunnable` | `Transform` | Invoke, Stream, Collect |

```go
r := agentcore.NewStreamRunnable(func(ctx context.Context, input string) (*agentcore.StreamReader[string], error) {
    sr := agentcore.NewStreamReader[string](10)
    go func() {
        defer sr.Close()
        for _, word := range strings.Fields(input) {
            if ctx.Err() != nil || !sr.Send(word) {
                return
            }
        }
    }()
    return sr, nil
})

// Auto-derived: Invoke collects the stream and returns the last element
result, err := r.Invoke(ctx, "hello world")
if err != nil {
    return err
}
```

### Graph Engine

**DAG** — parallel execution of independent branches:

```go
g := graph.NewGraph()
if err := g.AddNode("parse", parseStep); err != nil { return err }
if err := g.AddNode("validate", validateStep); err != nil { return err }
if err := g.AddNode("transform", transformStep); err != nil { return err }
if err := g.AddEdge("parse", "validate"); err != nil { return err }
if err := g.AddEdge("parse", "transform"); err != nil { return err }

cg, err := g.Compile(graph.CompileOptions{EntryNode: "parse"})
if err != nil {
    return err
}
output, err := cg.Run(ctx, input)
if err != nil {
    return err
}
```

**Pregel** — cyclic state graphs with super-step iteration:

```go
pg := graph.NewPregelGraph()
if err := pg.AddNode("agent", agentNode); err != nil { return err }
if err := pg.AddNode("tools", toolsNode); err != nil { return err }
if err := pg.AddEdge("tools", "agent"); err != nil { return err }
if err := pg.SetConditionalEdgeWithTargets("agent", []string{"tools", graph.PregelEnd}, func(ctx context.Context, state graph.PregelState) []string {
    if state.GetString("done") == "true" {
        return []string{graph.PregelEnd}
    }
    return []string{"tools"}
}); err != nil { return err }

cpg, err := pg.Compile("agent")
if err != nil {
    return err
}
finalState, err := cpg.Run(ctx, graph.PregelState{"input": "solve x^2=4"})
if err != nil {
    return err
}
```

**Unified Runner** — one API for both modes:

```go
runner := graph.NewDAGRunner(compiledGraph)
if usePregel {
    runner = graph.NewPregelRunner(compiledPregelGraph, &state)
}

output, err := runner.Run(ctx, input)
if err != nil {
    return err
}
```

Compiled DAG and Pregel graphs expose deterministic introspection, diagnostics,
conditional targets, and Mermaid export. Typed nodes keep their internal
`Runnable[I, O]` boundary while using explicit codecs to remain compatible with
the string graph engine:

```go
info := compiledGraph.Info()
diagram := compiledGraph.Mermaid()

if err := graph.AddTypedNode(g, graph.TypedNode[int, Result]{
    Name:     "calculate",
    Runnable: calculationRunnable,
    Decode:   parseInteger,
    Encode:   graph.JSONEncode[Result],
}); err != nil { return err }
```

For homogeneous pipelines, `TypedGraph[T]` keeps values typed through nodes,
parallel branches, fan-in merges, and terminal outputs without string or JSON
conversion:

```go
typed := graph.NewTypedGraph[Document]()
if err := typed.AddNode("load", loadRunnable); err != nil { return err }
if err := typed.AddNode("enrich", enrichRunnable); err != nil { return err }
if err := typed.AddEdge("load", "enrich"); err != nil { return err }

compiled, err := typed.Compile(graph.TypedCompileOptions[Document]{
    EntryNode: "load",
})
if err != nil {
    return err
}
result, err := compiled.Run(ctx, inputDocument)
if err != nil {
    return err
}
```

### Runtime Reliability

Large tool registries can expose only relevant definitions per model request,
provider failures can move through an ordered model chain, and large tool
results can be offloaded to retrievable artifacts:

```go
artifactStore := agentcore.NewMemoryArtifactStore()
semanticSelector := &agentcore.EmbeddingToolSelector{
    Embed: embeddingService.Embed,
    MinScore: 0.6,
    Fallback: agentcore.KeywordToolSelector{},
}
cfg := agentcore.NewConfig(
    agentcore.WithModel(model),
    agentcore.WithProvider(provider),
    agentcore.WithToolSelection(&agentcore.ToolSelectionConfig{
        Selector: semanticSelector,
        MaxVisible: 12,
    }),
    agentcore.WithModelFailover(&agentcore.ModelFailoverConfig{
        Targets: []agentcore.ModelTarget{
            {Name: "backup", Model: "backup-model", Provider: backupProvider},
        },
    }),
    agentcore.WithArtifactOffload(&agentcore.ArtifactOffloadConfig{
        Store: artifactStore,
    }),
)
```

Before each provider call, incomplete tool-call history is normalized with
synthetic interrupted results. Streaming deltas carry an `AttemptID`; failed
attempts emit `message_reset` before retry or failover so UIs can retract stale
partial output. Checkpoints and ordinary `Store` snapshots share schema
migrations configured with `WithStateMigrators`. Agent, model, tool, graph, and
workflow executions propagate correlated `agentcore.RunInfo` through both
`context.Context`, EventBus events, SSE envelopes, and configured tracers.

### Session Management

Append-only JSONL tree with branching, compaction, labels, and version migration.

```go
store, err := session.NewFileStore("./sessions")
if err != nil {
    return err
}
mgr, err := store.Create(ctx, session.CreateOptions{Cwd: "/project"})
if err != nil {
    return err
}

// Append messages
if err := mgr.AppendMessage(ctx, agentcore.Message{Role: "user", Content: "hello"}); err != nil {
    return err
}
if err := mgr.AppendMessage(ctx, agentcore.Message{Role: "assistant", Content: "hi"}); err != nil {
    return err
}

// Branch from an earlier point
if err := mgr.Branch(earlierEntryID); err != nil {
    return err
}
if err := mgr.AppendMessage(ctx, agentcore.Message{Role: "user", Content: "different question"}); err != nil {
    return err
}

// Get messages along current path (handles compaction & branch summaries)
msgs := mgr.MessagesOnPath()

// Tree inspection
tree := mgr.GetTree()
stats := mgr.Stats()
leaves := mgr.Leaves()
```

### Workflow Orchestration

```go
pipeline := &workflow.Pipeline{
    Steps: []workflow.Step{parseStep, validateStep, transformStep},
}

parallel := &workflow.Parallel{
    Steps: []workflow.Step{fetchA, fetchB, fetchC},
    Merge: func(results []string) string { return strings.Join(results, "\n") },
}

router := &workflow.Router{
    Route: func(ctx context.Context, input string) string {
        if strings.Contains(input, "code") { return "coder" }
        return "chat"
    },
    Branches: map[string]workflow.Step{"coder": coderStep, "chat": chatStep},
}
```

### Providers

```go
// OpenAI / Chat Completions protocol (also works with DeepSeek, Qwen, Moonshot, Groq, Together...)
p := chatcompat.New(chatcompat.Config{
    APIKey: os.Getenv("OPENAI_API_KEY"),
)

// DeepSeek (OpenAI-compatible)
p := chatcompat.New(chatcompat.Config{
    APIKey:  os.Getenv("DEEPSEEK_API_KEY"),
    BaseURL: "https://api.deepseek.com/v1",
})

// Qwen / Tongyi Qianwen (OpenAI-compatible)
p := chatcompat.New(chatcompat.Config{
    APIKey:  os.Getenv("QWEN_API_KEY"),
    BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1",
})

// Moonshot / Kimi (OpenAI-compatible)
p := chatcompat.New(chatcompat.Config{
    APIKey:  os.Getenv("MOONSHOT_API_KEY"),
    BaseURL: "https://api.moonshot.cn/v1",
})

// Anthropic
p := anthropic.New(anthropic.Config{
    APIKey: os.Getenv("ANTHROPIC_API_KEY"),
})

// Gemini (native REST API)
p := gemini.New(gemini.Config{
    APIKey: os.Getenv("GEMINI_API_KEY"),
    Model:  "gemini-2.5-flash",
})

// AWS Bedrock
p := bedrock.New(bedrock.Config{
    Region:  "us-east-1",
    ModelID: "anthropic.claude-sonnet-4-20250514",
})

// GCP Vertex AI
p := vertex.New(vertex.Config{
    ProjectID: "my-project",
    Location:  "us-central1",
    Model:     "gemini-2.5-flash",
})

// GitHub Copilot
p := copilot.New(copilot.Config{
    Token: os.Getenv("GITHUB_TOKEN"),
})

// Mistral AI
p := mistral.New(mistral.Config{
    APIKey: os.Getenv("MISTRAL_API_KEY"),
})
```

The provider configurations above are independent alternatives: choose one,
rather than declaring `p` repeatedly in the same scope. Providers support
streaming and parse usage when the service reports it. Bedrock uses AWS event
streams rather than SSE. Tool and multimodal support vary by implementation;
the current Bedrock adapter sends text messages only. Gemini uses the native
`generateContent` / `streamGenerateContent` endpoints with `functionCall` /
`functionResponse` parts.

### Structured Output

`agentcore.Config` supports first-class structured output requests via `ResponseFormat`.
All providers accept the same high-level request shape.

```go
agent, err := agentcore.NewWithError(agentcore.Config{
    ModelConfig: agentcore.ModelConfig{
        Model: model,
        Provider: provider,
        ResponseFormat: agentcore.NewJSONSchemaResponseFormat("answer", map[string]any{
            "type": "object",
            "properties": map[string]any{
                "answer": map[string]any{"type": "string"},
            },
            "required": []string{"answer"},
        }),
    },
})
if err != nil {
    return err
}
defer agent.Close()

out, err := agent.Run(ctx, "Reply with JSON only")
if err != nil {
    return err
}
// out will be the raw JSON string, e.g. {"answer":"ok"}
```

For provider-level use, `ProviderRequest.ResponseFormat` is forwarded directly to providers, and
responses populate `ProviderResponse.Structured` when the returned content is valid JSON.

- `chatcompat`: uses native `response_format`
- `gemini`: uses native `generationConfig.responseMimeType` / `responseSchema`
- `anthropic`: currently uses a schema-aware system instruction fallback and extracts valid JSON from the model text response
- `bedrock`: currently does not forward `ResponseFormat`; do not rely on native schema enforcement through this adapter

You can also decode directly into a typed result:

```go
type Answer struct {
    Answer string `json:"answer"`
}

result, err := agentcore.RunStructured[Answer](ctx, agent, "Reply with JSON only")
if err != nil {
    return err
}
```

### Provider Thinking Controls

You can request provider-native reasoning / thought summaries through `agentcore.Config.Thinking`:

```go
cfg := agentcore.Config{
    ModelConfig: agentcore.ModelConfig{
        Name: "reasoner", Model: "gemini-2.5-flash", Provider: provider,
        Thinking: &agentcore.ThinkingConfig{
            IncludeThoughts: true,
            Display:         agentcore.ThinkingDisplaySummarized,
            Effort:          agentcore.ThinkingEffortMedium,
            Budget:          2048,
        },
    },
}
```

- `Display` controls whether visible reasoning summaries are returned (`summarized` / `omitted`)
- `Effort` expresses reasoning depth when the provider supports it
- `Budget` caps reasoning tokens; for Gemini, `-1` means dynamic budget
- `gemini`: maps to `generationConfig.thinkingConfig`
- `anthropic`: maps to `thinking: { type: "adaptive", display: ..., effort: ... }`
- `chatcompat`: currently ignores this setting

### Richer Message Blocks

`agentcore.Message` blocks support image inputs in addition to text and thinking segments.
Supported providers send these blocks as native multimodal content; the current
Bedrock adapter sends text only.

```go
msg := agentcore.Message{
    Role:    agentcore.RoleUser,
    Content: "What is in this image?",
}.AppendImageURLBlock("https://example.com/cat.png")
```

- `chatcompat`: sends image blocks as `image_url` multipart content
- `anthropic`: sends `data:` URLs as native `image` blocks with base64 source payloads
- `gemini`: sends `data:` URLs as `inlineData` and `gs://` / `file://` URIs as `fileData`

Provider responses preserve block structure on output via `ProviderResponse.Blocks`,
including streaming aggregation, and the agent persists assistant blocks into `Message.Blocks`
alongside the legacy `Content` string.

## A2A Protocol (Agent-to-Agent)

Interoperate with any A2A-compliant agent — including Google ADK agents.

```go
// Expose your agent as an A2A server
handler := a2a.NewDefaultAgentHandler(card, agent, agent.Config())
server := a2a.NewServer(handler)
return server.ListenAndServe(":8080")
```

```go
// Call a remote A2A agent
client := a2a.NewClient("http://remote-agent.example.com")
task, err := client.SendTask(ctx, a2a.SendTaskRequest{
    ID: "task-123",
    Message: a2a.Message{Role: "user", Parts: []a2a.Part{a2a.NewTextPart("Hello")}},
})
if err != nil {
    return err
}
```

```go
// Register remote A2A agents as handoff targets
ext := a2a.NewRemoteHandoffExtension([]a2a.RemoteHandoffConfig{{
    Name: "math-expert", URL: "http://math-agent.example.com",
}})
cfg := agentcore.Config{
    ModelConfig: agentcore.ModelConfig{Model: model, Provider: provider},
    Extensions: []agentcore.Extension{ext},
}
```

Features:
- Agent Card discovery (`/.well-known/agent.json`)
- Full task lifecycle (submitted → working → completed/failed/canceled)
- Synchronous & streaming (SSE) modes
- Multi-modal content: text, file, structured data parts
- Push notification webhooks
- WebSocket transport

## ACP (Agent Communication Protocol)

JSON-RPC based protocol for agent-to-agent communication, providing:
- `AgentFactory` / `AgentInstance` interfaces for creating and running agents by session
- Session-based agent lifecycle management
- Extensible auth provider support

## AGUI (Agent GUI Events)

SSE-based event protocol for streaming agent execution to web UIs:

```go
handler := agui.NewHandler(config)
http.Handle("/events", handler)
```

Event types cover: run lifecycle, step progress, text deltas, thinking blocks, tool calls, state snapshots, and custom events.

## A2UI (Agent-to-UI)

Declarative UI protocol for agents to render and update surfaces (forms, dashboards, live feeds) on the client side, with built-in data binding, validation, and transport over A2A or AG-UI.

```go
envs := a2ui.NewSurface("profile", a2ui.BasicCatalogID).
    Add(a2ui.Column("root", "name", "title")).
    Add(a2ui.Text("name", a2ui.Bind("/user/name"))).
    Add(a2ui.Text("title", "Mathematician")).
    Data("/user/name", "Ada").
    Build()

enc := a2ui.NewEncoder(os.Stdout)
if err := enc.EncodeAll(envs); err != nil {
    return err
}
```

Full A2UI protocol support: surfaces, components, data model (JSON Pointer), validation, surface store, and both A2A and AG-UI transport bindings.

## HTTP Server with Threads

The HTTP server can keep conversations continuous across requests when you provide a `thread_id`.
If `Config.Store` is configured, `/api/chat` automatically restores the saved state for that thread before
running and saves the updated state afterwards. If `Config.Checkpoint` is also configured, the same
`thread_id` is used for automatic checkpoints.

```json
{
  "message": "Summarize what we decided so far",
  "thread_id": "thread-123",
  "stream": false,
  "model": "gemini-2.5-flash",
  "response_format": {
    "type": "json_object"
  },
  "thinking": {
    "display": "summarized",
    "effort": "medium",
    "budget": 2048
  }
}
```

Calls without `thread_id` remain stateless unless a session-backed store creates
a new thread as described below.
When `model`, `response_format`, or `thinking` are omitted, the server falls back to its default
`agentcore.Config` values. When present, request-level fields override the server defaults for that
call. For session-backed threads, the effective precedence is: server default < persisted thread
config < request override.

When `Config.Store` is backed by `session.NewAgentStore(...)`, `POST /api/chat` will automatically
create a new thread and return its `thread_id` when the client omits one.

You can back `Config.Store` with either snapshot files or JSONL sessions:

```go
// Snapshot store
snapshots, err := store.NewFileStore("./states")
if err != nil {
    return err
}

// Session-backed store
sessions, err := session.NewFileStore("./sessions")
if err != nil {
    return err
}
threadStore := session.NewAgentStore(sessions, "/project")

srv := server.New(agentcore.Config{
    ModelConfig: agentcore.ModelConfig{Model: model, Provider: provider},
    Store:    threadStore,
})
```

When `Config.Store` is backed by `session.NewAgentStore(...)`, the HTTP server also exposes
thread-oriented endpoints:

- `POST /api/threads` create an empty thread
- `GET /api/threads` list persisted threads
- `GET /api/threads/{key}` fetch thread metadata and transcript, including per-message `entry_id`
- `GET /api/threads/{key}/config` fetch the persisted thread-level call config
- `PUT /api/threads/{key}/config` persist or clear a thread-level config via `{ "config": ... }`
- `GET /api/threads/{key}/thinking` fetch the persisted thread-level thinking config
- `PUT /api/threads/{key}/thinking` persist or clear a thread-level thinking config via `{ "thinking": ... }`
- `POST /api/threads/{key}/branch` create a new branch from the current leaf, or from a specific historical entry by passing `{ "entry_id": "..." }`
- `DELETE /api/threads/{key}` delete a thread

Example thread-level config override:

```json
{
  "config": {
    "model": "gemini-2.5-flash",
    "response_format": {
      "type": "json_object"
    },
    "thinking": {
      "display": "summarized",
      "effort": "high",
      "budget": 2048
    }
  }
}
```

Example thread-level thinking-only override:

```json
{
  "thinking": {
    "display": "summarized",
    "effort": "high",
    "budget": 2048
  }
}
```

The generic state endpoints (`/api/states`, `/api/states/{key}`) remain available for all
`agentcore.Store` implementations.

### Example CLI Threads

`example/cli-chat` uses the same conversation model as the HTTP server: one active thread is reused
across turns, `/clear` and `/new` start a fresh conversation, and `/branch` forks the current
thread when the store is backed by `session.NewAgentStore(...)`.

You can also enable provider-native thinking in `example/cli-chat` with environment variables such as
`THINKING_DISPLAY=summarized`, `THINKING_EFFORT=medium`, `THINKING_BUDGET=2048`, and
`THINKING_INCLUDE_THOUGHTS=true`.

At runtime, `example/cli-chat` also supports `/thinking` commands such as `/thinking`, `/thinking summarized`,
`/thinking omitted`, `/thinking effort medium`, `/thinking budget -1`, and `/thinking reset`.

## TUI (Terminal UI)

A fully-layered terminal UI with Elm-style architecture, differential rendering, and no external TUI framework dependency.

```
tui/
├── core/              Foundation: Component interfaces, rune utilities, SpinnerStyle
├── terminal/          Terminal I/O, key parsing (Kitty protocol), termios (macOS/Linux)
├── theme/             ANSI Style, semantic palette, JSON hot-reload
├── component/         UI components: Editor, Markdown, Input, SelectList, Loader, Box, etc.
├── chat/              Chat application with scrollable transcript
├── stdio/             Procedural stdout/stdin tools (Spinner, Renderer, ProgressBar)
├── agentadapter/      Agentcore → chat event bridge
└── tui.go             TUI engine: event loop, overlay system, diff renderer
```

Key design:
- **Layer isolation**: higher layers import lower layers, never the reverse
- **Agent-decoupled chat**: `tui/chat` uses its own event types and `Subscriber` interfaces — no direct `agentcore` dependency
- **Dual rendering**: TUI engine (differential, Elm-architecture) + stdio layer (procedural `\r` overwriting)

```go
app := tui.NewChatApp(chat.ChatAppConfig{Title: "Assistant"})
if err := app.Start(); err != nil {
    return err
}
defer app.Stop()
select {
case <-ctx.Done():
    return ctx.Err()
case <-app.Done():
    return nil
}
```

For this fragment, import `github.com/covoyage/covonaut/tui/chat` as `chat`.
Attach application submit handlers or an event subscriber before starting the
app; this fragment only demonstrates terminal lifecycle management.

## Model Profiles

Register model-specific defaults (system-prompt suffix, excluded tools, temperature, max turns) that apply automatically when `Config.Model` matches.

```go
agentcore.RegisterProfile(agentcore.ModelProfile{
    Name:               "claude-sonnet-4-6",
    SystemPromptSuffix: "Use parallel tool calls when possible.",
    ExcludedTools:      []string{"computer_use"},
})

cfg := agentcore.Config{
    ModelConfig: agentcore.ModelConfig{Model: "claude-sonnet-4-6", Provider: provider},
}
```

User-set fields always win; `SystemPromptSuffix` is additive. Profiles apply once during initialization; runtime model changes (e.g. via `ApplyCallConfig`) do not re-apply.

## Prompt Caching

Automatically annotate messages with `cache_control` breakpoints so supported
Anthropic-compatible providers can cache stable prefixes. Actual savings depend
on service pricing, cache hits, and request contents.

```go
cfg := agentcore.Config{
    ModelConfig: agentcore.ModelConfig{Model: model, Provider: provider},
    Extensions: []agentcore.Extension{
        agentcore.NewPromptCachingExtension(),
    },
}
```

Defaults: 4 breakpoints max, one on the system prompt, one near the conversation tail. Tunable via `WithMaxBreakpoints`, `WithCacheSystemPrompt`, `WithCacheLastN`. A zero-value `PromptCachingExtension{}` is a safe no-op.

## Project Rules

Load project rule files (AGENTS.md, .cursorrules, or any caller-specified file) into the system prompt so the agent picks up project-specific guidance without hard-coding it.

```go
ext := agentcore.NewRulesExtension() // defaults to AGENTS.md
// or: agentcore.NewRulesExtension(agentcore.WithRulesPaths("AGENTS.md", ".cursorrules"))
cfg := agentcore.Config{
    ModelConfig: agentcore.ModelConfig{Model: model, Provider: provider},
    Extensions: []agentcore.Extension{ext},
}
```

Missing files are silently skipped. Inject a custom `FileReader` via `WithRulesReader` for sandboxed or embedded rule sources.

## Extensions

Plug in tools, hooks, middleware, and system prompts via a single interface.

```go
type MyExtension struct {
    agentcore.BaseExtension
}

func (e *MyExtension) Init(ctx context.Context, agent *agentcore.Agent) error { return nil }

func (e *MyExtension) Tools() []*agentcore.Tool {
    return []*agentcore.Tool{myTool}
}

cfg := agentcore.Config{
    ModelConfig: agentcore.ModelConfig{Model: model, Provider: provider},
    Extensions: []agentcore.Extension{&MyExtension{}},
}
```

## License

[MIT](LICENSE)
