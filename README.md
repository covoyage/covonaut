# covonaut

Production-ready Agent framework for Go. Zero external dependencies, 82k+ lines of pure Go.

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
    "fmt"

    "github.com/covoyage/covonaut/agentcore"
    "github.com/covoyage/covonaut/provider/openai"
)

func main() {
    agent := agentcore.New(agentcore.Config{
        Name:         "assistant",
        SystemPrompt: "You are a helpful assistant.",
        Provider:     openai.New(openai.Config{APIKey: "sk-..."}),
        Tools: []*agentcore.Tool{{
            Name:        "greet",
            Description: "Say hello",
            Parameters:  map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}},
            Func: func(ctx context.Context, args json.RawMessage) (any, error) {
                return "Hello!", nil
            },
        }},
    })

    output, err := agent.Run(context.Background(), "Hi there")
    if err != nil {
        panic(err)
    }
    fmt.Println(output)
}
```

## Core Concepts

### Agent Loop

The central `Agent` executes an LLM-tool loop with configurable max turns, automatic context compaction, and retry with exponential backoff.

```go
agent := agentcore.New(agentcore.Config{
    Name:         "coder",
    Provider:     provider,
    MaxTurns:     20,
    ContextWindow: &agentcore.ContextWindowConfig{MaxTokens: 128000, CompactionThreshold: 0.8},
    RetryConfig:  &agentcore.RetryConfig{MaxRetries: 3, InitialDelay: time.Second},
})
```

### Tools

Register tools with JSON Schema validation, per-tool hooks, and global middleware.

```go
tool := &agentcore.Tool{
    Name:        "read_file",
    Description: "Read a file from disk",
    Parameters:  map[string]any{...},
    Func: func(ctx context.Context, args json.RawMessage) (any, error) {
        // ...
    },
    Before: []agentcore.BeforeHook{authCheck},
    After:  []agentcore.AfterHook{auditLog},
}
```

### Built-in Tools Extension

`covonaut` ships a comprehensive `tools` package providing filesystem, shell, search, browser, and code execution tools — all as a single pluggable `Extension`.

```go
import "github.com/covoyage/covonaut/tools"

ext := tools.NewExtension(tools.ExtensionConfig{WorkingDir: "/path/to/project"})
agent := agentcore.New(agentcore.Config{
    Extensions: []agentcore.Extension{ext},
})
```

Each tool supports pluggable operations for delegating to remote systems (e.g. SSH).

### MCP Tools

`covonaut` can bridge external MCP servers into first-class `agentcore.Tool`s.
Supports MCP `stdio` transport and HTTP/SSE transport, plus `tools/list` / `tools/call`.

```go
import "github.com/covoyage/covonaut/mcp"

ctx := context.Background()
ext, err := mcp.NewStdioExtension(ctx, mcp.StdioConfig{
    Name:       "filesystem",
    Command:    "npx",
    Args:       []string{"-y", "@modelcontextprotocol/server-filesystem", "."},
    ToolPrefix: "fs.",
})
if err != nil {
    panic(err)
}

agent := agentcore.New(agentcore.Config{
    Name:       "assistant",
    Model:      "gpt-4o-mini",
    Provider:   provider,
    Extensions: []agentcore.Extension{ext},
})
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

### Events

Type-safe event bus for real-time observability.

```go
agent.On(agentcore.EventMessageDelta, func(e agentcore.Event) {
    delta := e.(*agentcore.MessageDeltaEvent)
    fmt.Print(delta.Content)
})

agent.On(agentcore.EventToolCallEnd, func(e agentcore.Event) {
    tc := e.(*agentcore.ToolCallEndEvent)
    fmt.Printf("Tool %s completed\n", tc.Name)
})
```

### Lifecycle Hooks

Intercept every stage of agent execution.

```go
agent := agentcore.New(agentcore.Config{
    Lifecycle: agentcore.LifecycleChain{
        &agentcore.GuardrailHook{Check: safetyCheck},
        &agentcore.AuditHook{OnEvent: logEvent},
        &agentcore.RateLimitHook{Limiter: limiter},
    },
})
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
            sr.Send(word)
        }
    }()
    return sr, nil
})

// Auto-derived: Invoke collects the stream and returns the last element
result, _ := r.Invoke(ctx, "hello world")
```

### Graph Engine

**DAG** — parallel execution of independent branches:

```go
g := graph.NewGraph()
g.AddNode("parse", parseStep)
g.AddNode("validate", validateStep)
g.AddNode("transform", transformStep)
g.AddEdge("parse", "validate")
g.AddEdge("parse", "transform")

cg, _ := g.Compile(graph.CompileOptions{EntryNode: "parse"})
output, _ := cg.Run(ctx, input)
```

**Pregel** — cyclic state graphs with super-step iteration:

```go
pg := graph.NewPregelGraph()
pg.AddNode("agent", agentNode)
pg.AddNode("tools", toolsNode)
pg.AddEdge("tools", "agent")
pg.SetConditionalEdge("agent", func(ctx context.Context, state graph.PregelState) []string {
    if state.GetString("done") == "true" {
        return []string{graph.PregelEnd}
    }
    return []string{"tools"}
})

cpg, _ := pg.Compile("agent")
finalState, _ := cpg.Run(ctx, graph.PregelState{"input": "solve x^2=4"})
```

**Unified Runner** — one API for both modes:

```go
runner := graph.NewDAGRunner(compiledGraph)
// or
runner := graph.NewPregelRunner(compiledPregelGraph, &state)

output, _ := runner.Run(ctx, input)
```

### Session Management

Append-only JSONL tree with branching, compaction, labels, and version migration.

```go
store, _ := session.NewFileStore("./sessions")
mgr, _ := store.Create(ctx, session.CreateOptions{Cwd: "/project"})

// Append messages
mgr.AppendMessage(ctx, agentcore.Message{Role: "user", Content: "hello"})
mgr.AppendMessage(ctx, agentcore.Message{Role: "assistant", Content: "hi"})

// Branch from an earlier point
mgr.Branch(earlierEntryID)
mgr.AppendMessage(ctx, agentcore.Message{Role: "user", Content: "different question"})

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
// OpenAI
p := openai.New(openai.Config{
    APIKey: os.Getenv("OPENAI_API_KEY"),
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

All providers support streaming (SSE) and report token usage. Gemini uses the native `generateContent` / `streamGenerateContent` endpoints with `functionCall` / `functionResponse` parts.

### Structured Output

`agentcore.Config` supports first-class structured output requests via `ResponseFormat`.
All providers accept the same high-level request shape.

```go
agent := agentcore.New(agentcore.Config{
    Model:    "gpt-4o-mini",
    Provider: openai.New(openai.Config{APIKey: os.Getenv("OPENAI_API_KEY")}),
    ResponseFormat: agentcore.NewJSONSchemaResponseFormat("answer", map[string]any{
        "type": "object",
        "properties": map[string]any{
            "answer": map[string]any{"type": "string"},
        },
        "required": []string{"answer"},
    }),
})

out, err := agent.Run(ctx, "Reply with JSON only")
// out will be the raw JSON string, e.g. {"answer":"ok"}
```

For provider-level use, `ProviderRequest.ResponseFormat` is forwarded directly to providers, and
responses populate `ProviderResponse.Structured` when the returned content is valid JSON.

- `openai`: uses native `response_format`
- `gemini`: uses native `generationConfig.responseMimeType` / `responseSchema`
- `anthropic`: currently uses a schema-aware system instruction fallback and extracts valid JSON from the model text response
- `bedrock`: uses native Converse API `toolConfig` / `confluenceConfig`

You can also decode directly into a typed result:

```go
type Answer struct {
    Answer string `json:"answer"`
}

result, err := agentcore.RunStructured[Answer](ctx, agent, "Reply with JSON only")
```

### Provider Thinking Controls

You can request provider-native reasoning / thought summaries through `agentcore.Config.Thinking`:

```go
agent := agentcore.New(agentcore.Config{
    Name:     "reasoner",
    Model:    "gemini-2.5-flash",
    Provider: provider,
    Thinking: &agentcore.ThinkingConfig{
        IncludeThoughts: true,
        Display:         agentcore.ThinkingDisplaySummarized,
        Effort:          agentcore.ThinkingEffortMedium,
        Budget:          2048,
    },
})
```

- `Display` controls whether visible reasoning summaries are returned (`summarized` / `omitted`)
- `Effort` expresses reasoning depth when the provider supports it
- `Budget` caps reasoning tokens; for Gemini, `-1` means dynamic budget
- `gemini`: maps to `generationConfig.thinkingConfig`
- `anthropic`: maps to `thinking: { type: "adaptive", display: ..., effort: ... }`
- `openai`: currently ignores this setting

### Richer Message Blocks

`agentcore.Message` blocks support image inputs in addition to text and thinking segments.
All providers send these blocks as native multimodal content.

```go
msg := agentcore.Message{
    Role:    agentcore.RoleUser,
    Content: "What is in this image?",
}.AppendImageURLBlock("https://example.com/cat.png")
```

- `openai`: sends image blocks as `image_url` multipart content
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
log.Fatal(server.ListenAndServe(":8080"))
```

```go
// Call a remote A2A agent
client := a2a.NewClient("http://remote-agent.example.com")
task, err := client.SendTask(ctx, a2a.SendTaskRequest{
    ID: "task-123",
    Message: a2a.Message{Role: "user", Parts: []a2a.Part{a2a.NewTextPart("Hello")}},
})
```

```go
// Register remote A2A agents as handoff targets
ext := a2a.NewRemoteHandoffExtension([]a2a.RemoteHandoffConfig{{
    Name: "math-expert", URL: "http://math-agent.example.com",
}})
agent := agentcore.New(agentcore.Config{Extensions: []agentcore.Extension{ext}})
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
env := a2ui.NewSurface("profile", a2ui.BasicCatalogID).
    Add(a2ui.Column("root", "name", "title")).
    Add(a2ui.Text("name", a2ui.Bind("/user/name"))).
    Add(a2ui.Text("title", "Mathematician"))

enc := a2ui.NewEncoder(os.Stdout)
enc.Encode(env) // → JSONL to the client
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

Calls without `thread_id` remain stateless.
When `model`, `response_format`, or `thinking` are omitted, the server falls back to its default
`agentcore.Config` values. When present, request-level fields override the server defaults for that
call. For session-backed threads, the effective precedence is: server default < persisted thread
config < request override.

When `Config.Store` is backed by `session.NewAgentStore(...)`, `POST /api/chat` will automatically
create a new thread and return its `thread_id` when the client omits one.

You can back `Config.Store` with either snapshot files or JSONL sessions:

```go
// Snapshot store
snapshots, _ := store.NewFileStore("./states")

// Session-backed store
sessions, _ := session.NewFileStore("./sessions")
threadStore := session.NewAgentStore(sessions, "/project")

srv := server.New(agentcore.Config{
    Provider: provider,
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
app := tui.NewChatApp(tui.ChatAppConfig{
    Chat: chatConfig,
    TUI:  tuiConfig,
})
app.Run(ctx)
```

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

agent := agentcore.New(agentcore.Config{
    Extensions: []agentcore.Extension{&MyExtension{}},
})
```

## License

MIT
