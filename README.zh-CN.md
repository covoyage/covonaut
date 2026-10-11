# covonaut

[English](README.md) | [简体中文](README.zh-CN.md)

[![CI](https://github.com/covoyage/covonaut/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/covoyage/covonaut/actions/workflows/ci.yml) [![Release](https://img.shields.io/github/v/release/covoyage/covonaut)](https://github.com/covoyage/covonaut/releases/latest) [![Website](https://img.shields.io/badge/website-covonaut-18794e)](https://covoyage.github.io/covonaut/zh/) [![License](https://img.shields.io/github/license/covoyage/covonaut)](LICENSE)

可用于生产环境的 Go Agent 框架。

[使用文档](https://covoyage.github.io/covonaut/zh/) | [API 参考](https://pkg.go.dev/github.com/covoyage/covonaut)

## 文档

文档站部署在 GitHub Pages：

- [英文版](https://covoyage.github.io/covonaut/)
- [中文版](https://covoyage.github.io/covonaut/zh/)

首次访问时，网站会根据浏览器语言打开中文或英文页面。你也可以手动切换，网站会记住你的选择。两个版本的内容一致，包含入门指南、核心概念、工具和模型服务的接入方式，以及协议说明和可运行示例。

本地预览需要 Node.js 22 和 Go 1.25+：

```bash
npm --prefix docs ci
npm --prefix docs run dev
```

修改文档后，运行 `npm --prefix docs run check`。它会构建网站，检查链接和中英文页面是否对应，并验证 Go 示例；整个过程不需要连接模型服务。

[文档发布工作流](.github/workflows/docs.yml)会检查 Pull Request；更新合并到 `main` 后，文档会自动发布。
首次启用时，请在仓库的 **Settings > Pages > Build and deployment > Source** 中选择 **GitHub Actions**。
网站构建完成后，生成的文件保存在 `docs/.vitepress/dist`。

## 安装

```bash
go get github.com/covoyage/covonaut/agentcore
```

需要 **Go 1.25+**。

## 快速开始

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

运行前，设置 `OPENAI_API_KEY`，并在 `AGENT_MODEL` 中填写你的账号有权使用、且支持工具调用的模型名称。
也可以在仓库目录运行 `go run ./example/quickstart`，体验完整示例。
实际项目建议用 `agentcore.NewWithError` 创建 Agent，这样能在初始化时发现并处理扩展加载错误。仓库中的完整示例使用的就是这种方式。

快速开始是完整程序，下面的代码是功能片段，需要补齐导入、带超时或取消机制的 `ctx` 和应用变量。
包含 `return err` 的片段应放在返回 `error` 的函数中。只生成 `cfg` 的片段用于说明配置，
使用时按快速开始的方式调用 `agentcore.NewWithError(cfg)`、检查错误，并通过 `defer agent.Close()` 释放资源。
参数验证不能代替权限检查；模型和 Provider 的能力也不完全相同。

## 核心概念

### Agent 如何执行任务

`Agent` 先调用模型；如果模型要求使用工具，就执行工具并把结果交回模型，直到模型给出最终答复。你可以限制执行轮数，在上下文过长时自动压缩历史，并为失败的请求设置重试次数和退避时间。

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

### 工具

快速开始包含完整工具实现：将参数解码到 Go 结构体，并启用 Schema 验证。
可通过 `Tool.Before`、`Tool.After` 或全局中间件接入审计和权限检查。
文件路径和允许的操作仍需在工具中验证，参数符合 Schema 不代表操作已获授权。

### 内置工具扩展

`tools` 包包含文件读写、Shell 命令、搜索、浏览器操作和代码执行等工具。创建工具扩展后，将它加入 Agent 的 `Extensions` 即可使用。

```go
import "github.com/covoyage/covonaut/tools"

ext := tools.NewExtension(tools.ExtensionConfig{WorkingDir: "/path/to/project"})
cfg := agentcore.Config{
    ModelConfig: agentcore.ModelConfig{Model: model, Provider: provider},
    Extensions: []agentcore.Extension{ext},
}
```

这些工具的底层操作接口可以替换。例如，你可以让文件工具通过 SSH 读写远程文件，而不必改动模型看到的工具定义。

`bash` 工具可以通过 `AllowList` 和 `BlockList` 限制命令。匹配时会检查单词边界，并拆分由 `&&`、`||`、`;`、`|` 连接的子命令。
但命令过滤不能代替沙箱。执行不可信命令时，应让 `BashOperations` 在沙箱中运行，并由应用负责权限检查。

### MCP 工具

接入 MCP 服务后，远程工具就能像本地 `agentcore.Tool` 一样被 Agent 调用。客户端支持 `stdio` 和 HTTP/SSE 连接，通过 `tools/list` 获取工具列表，通过 `tools/call` 执行工具。

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

- `NewStdioExtension(...)` 会在创建时连接服务并获取工具列表。将扩展加入 `Config.Extensions` 后，Agent 就能使用这些工具。
- 接入多个 MCP 服务时，建议设置 `ToolPrefix`，避免不同服务的工具重名。
- 如果 MCP 工具返回 `isError: true`，框架会把错误作为工具结果交给模型，而不是当作连接失败，让模型有机会修正调用。

### 多 Agent 协作

一个 Agent 可以把任务交给另一个擅长该任务的 Agent。委派模式下，结果会返回原 Agent；转交模式下，则由目标 Agent 接手后续任务。目标既可以在本地，也可以通过 A2A 连接到远程服务。

```go
cfg := agentcore.Config{
    Handoffs: []agentcore.HandoffConfig{
        {Name: "math-expert", Agent: mathCfg, Mode: agentcore.HandoffDelegate},
        {Name: "code-expert", Agent: codeCfg, Mode: agentcore.HandoffTransfer},
    },
}
```

### 可观测性

用 `Tracer` 查看一次任务中模型和工具的调用过程，用 `Metrics` 统计用量、耗时和错误。框架提供 OpenTelemetry 适配器，也允许接入其他后端。这两项都可在 `agentcore.Config` 中设置，不需要时留空即可。

```go
cfg := agentcore.Config{
    ModelConfig: agentcore.ModelConfig{Model: model, Provider: provider},
    Tracer:      agentcore.NewOtelTracer(tp.Tracer("my-agent")),
    Metrics:     agentcore.NewOtelMetrics(mp.Meter("my-agent")),
}
cfg.Provider = agentcore.NewModelMetricsMiddleware(cfg.Metrics)(cfg.Provider)
```

追踪记录会把同一次任务中的 Agent、模型和工具调用关联起来，并附带 `run.id`、`component` 等属性。
配置 `Metrics` 后，工具错误和 Agent 执行失败会自动计入 `agentcore.errors`。
如果还要统计模型的 token 用量、耗时和错误，需要用 `agentcore.NewModelMetricsMiddleware(cfg.Metrics)` 包装 Provider；仅设置 `Config.Metrics` 并不足够。这个中间件也会记录流式输出过程中发生的终止错误。

### 事件

订阅事件可以实时接收模型输出和工具执行结果，适合更新聊天界面或记录任务进度。不同事件有各自的 Go 类型：

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

### 生命周期钩子

如果需要在任务开始、模型调用或工具执行等阶段运行自己的逻辑，可以使用生命周期钩子。例如：

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

### Runnable 的四种模式

`Runnable[I, O]` 让不同组件使用相同的调用接口。它支持普通输入输出、流式输出、流式输入，以及流式输入到流式输出的转换。通常只需实现其中一种，框架就能补齐其他调用方式。

| 构造函数 | 需要实现 | 自动派生 |
|---|---|---|
| `NewInvokeRunnable` | `Invoke` | Stream、Collect、Transform |
| `NewStreamRunnable` | `Stream` | Invoke、Collect、Transform |
| `NewCollectRunnable` | `Collect` | Invoke、Stream、Transform |
| `NewTransformRunnable` | `Transform` | Invoke、Stream、Collect |

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

这个例子只实现了 `Stream`，但仍然可以调用 `Invoke`：框架会收集流中的数据，并返回最后一个元素。

### 图执行引擎

如果步骤之间没有循环依赖，可以用 **DAG** 描述执行顺序。互不依赖的分支会并行运行：

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

如果流程需要反复执行，例如模型调用工具后再次调用模型，可以使用 **Pregel**。它按超级步推进状态图，直到满足结束条件：

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

两种图都可以通过 **Runner** 调用，上层代码不必区分它们的执行方式：

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

图编译完成后，可以查看节点和边、检查诊断信息，并导出 Mermaid 图。条件分支的目标也可以事先声明和检查。
如果节点使用的不是字符串，可以保留它的 `Runnable[I, O]` 接口，再提供解码和编码函数，将它接入字符串图引擎：

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

如果整条流水线使用同一种数据类型，可以直接选择 `TypedGraph[T]`。数据经过节点、并行分支和汇合时都保持原来的 Go 类型，无需来回转换为字符串或 JSON：

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

### 工具筛选、模型切换与大结果处理

注册的工具很多时，不必把所有定义都发送给模型，可以先筛选本轮可能用到的工具。模型服务失败时，框架可以按配置顺序尝试备用模型。工具结果太大时，也可以先存入产物存储，让 Agent 按需读取，而不是把整份结果塞进上下文。

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

调用 Provider 前，框架会检查工具调用记录。如果某次调用没有结果，就补上一条中断说明，避免模型服务因历史消息不完整而拒绝请求。
流式输出中的 `AttemptID` 用于区分每次尝试；重试或切换模型前会发送 `message_reset`，界面可以据此撤回上一次未完成的输出。
检查点和普通 `Store` 快照使用同一套版本迁移规则，通过 `WithStateMigrators` 配置。
各层执行还会传递 `agentcore.RunInfo`，让上下文、事件、SSE 消息和追踪记录能够关联到同一次任务。

### 会话管理

会话记录保存为 JSONL 文件，新记录只追加，不覆盖已有记录。你可以从历史消息创建分支、压缩较长的对话、添加标签，也可以迁移旧版本的记录格式。

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

### 工作流编排

将步骤放入 `Pipeline` 可以依次执行；使用 `Parallel` 可以同时执行多个步骤；`Router` 则根据输入选择分支：

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

### 模型服务

接入时应根据服务提供的 API 协议选择 Provider，而不只是看厂商名称：

| API 协议 | Provider | 配置方式 |
| --- | --- | --- |
| OpenAI Chat Completions | `chatcompat` | `APIProtocolChat`（默认），可设置兼容服务的 `BaseURL` |
| OpenAI Responses | `chatcompat` | `APIProtocolResponses` |
| Anthropic Messages | `anthropic` | Anthropic API Key |
| Gemini `generateContent` | `gemini` | Gemini API Key |
| AWS Bedrock Converse | `bedrock` | AWS 凭证和区域 |
| Vertex AI 上的 Gemini / Anthropic | `vertex` | `NewGemini` / `NewAnthropic`，项目、区域和访问令牌 |

按需导入 `github.com/covoyage/covonaut/provider/<name>` 中的包。
下面的配置分别对应不同的接入方式，选择其中一项即可，不要在同一作用域中全部粘贴。

```go
// OpenAI / Chat Completions protocol (also works with DeepSeek, Qwen, Moonshot, Groq, Together...)
p := chatcompat.New(chatcompat.Config{
    APIKey:   os.Getenv("OPENAI_API_KEY"),
    Protocol: chatcompat.APIProtocolChat,
})

// OpenAI Responses API (requires a service that supports /responses)
p := chatcompat.New(chatcompat.Config{
    APIKey:   os.Getenv("OPENAI_API_KEY"),
    Protocol: chatcompat.APIProtocolResponses,
})

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
p, err := bedrock.New(bedrock.Config{
    AccessKey:    os.Getenv("AWS_ACCESS_KEY_ID"),
    SecretKey:    os.Getenv("AWS_SECRET_ACCESS_KEY"),
    SessionToken: os.Getenv("AWS_SESSION_TOKEN"),
    Region:       "us-east-1",
})
if err != nil {
    return err
}

// Gemini on GCP Vertex AI
p, err := vertex.NewGemini(vertex.Config{
    Project:     "my-project",
    Region:      "us-central1",
    AccessToken: os.Getenv("VERTEX_ACCESS_TOKEN"),
})
if err != nil {
    return err
}

// Anthropic Messages on GCP Vertex AI
p, err := vertex.NewAnthropic(vertex.Config{
    Project:     "my-project",
    Region:      "us-east5",
    AccessToken: os.Getenv("VERTEX_ACCESS_TOKEN"),
})
if err != nil {
    return err
}

// GitHub Copilot
p, err := copilot.New(copilot.Config{
    Token: os.Getenv("GITHUB_TOKEN"),
})
if err != nil {
    return err
}

// Mistral AI
p := mistral.New(mistral.Config{
    APIKey: os.Getenv("MISTRAL_API_KEY"),
})
```

选好 Provider 后，将它传给 Agent。把 `MODEL` 设置为该服务支持的模型 ID；使用 Bedrock 时，填写模型或推理配置文件的 ID：

```go
agent, err := agentcore.NewWithError(agentcore.NewConfig(
    agentcore.WithModel(os.Getenv("MODEL")),
    agentcore.WithProvider(p),
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

Mistral 和 Copilot 使用兼容 Chat Completions 的接口。修改 `BaseURL` 不会切换协议；
接入 Responses 服务时，需要显式设置 `APIProtocolResponses`。
Vertex 使用 Google 访问令牌认证；不填写 `AccessToken` 时，适配器会尝试执行 `gcloud auth print-access-token`。
模型名称和访问权限以你的账号实际可用的内容为准。
这些 Provider 支持流式输出，并读取服务返回的 token 用量。如果服务没有返回用量或缓存明细，框架无法补出准确的统计数据。
Bedrock 使用 AWS 事件流而不是 SSE。工具调用和多模态能力取决于具体实现，当前 Bedrock 适配器只发送文本消息。
Gemini 使用自己的 `generateContent`、`streamGenerateContent` 接口，工具调用和结果分别通过 `functionCall`、`functionResponse` 传递。

### 结构化输出

需要模型返回 JSON 时，可以设置 `agentcore.Config.ResponseFormat`。调用方使用同一种配置方式，Provider 负责转换为对应服务的请求格式。

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

也可以直接在 `ProviderRequest.ResponseFormat` 中设置格式，不经过 Agent 调用。返回内容是合法 JSON 时，可以从 `ProviderResponse.Structured` 读取。

- `chatcompat` 使用原生 `response_format`。
- `gemini` 使用 `generationConfig.responseMimeType` 和 `responseSchema`。
- `anthropic` 当前将 Schema 写入系统指令，引导模型按指定格式回答，再从回复中提取合法 JSON。
- `bedrock` 当前不转发 `ResponseFormat`，不能依赖该适配器进行原生 Schema 约束。

如果已经定义了结果结构体，可以直接将回复解码为 Go 值：

```go
type Answer struct {
    Answer string `json:"answer"`
}

result, err := agentcore.RunStructured[Answer](ctx, agent, "Reply with JSON only")
if err != nil {
    return err
}
```

### 模型思考配置

模型支持思考配置时，可以通过 `agentcore.Config.Thinking` 调整推理深度、预算，以及是否返回思考摘要：

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

- `Display` 决定是否返回思考摘要：`summarized` 表示返回摘要，`omitted` 表示省略。
- `Effort` 用于设置推理深度，是否生效取决于 Provider 和模型。
- `Budget` 限制用于思考的 token 数量；在 Gemini 中，`-1` 表示由模型动态分配预算。
- `gemini` 映射为 `generationConfig.thinkingConfig`。
- `anthropic` 映射为 `thinking: { type: "adaptive", display: ..., effort: ... }`。
- `chatcompat` 当前忽略这项配置。

### 多模态消息块

除了文本和思考片段，`agentcore.Message` 还可以包含图片。支持图片的 Provider 会将这些内容转换为模型服务支持的格式；当前 Bedrock 适配器只发送文本：

```go
msg := agentcore.Message{
    Role:    agentcore.RoleUser,
    Content: "What is in this image?",
}.AppendImageURLBlock("https://example.com/cat.png")
```

- `chatcompat` 将图片放入消息的 `image_url` 内容项。
- `anthropic` 将 `data:` URL 转换为带 base64 数据源的原生 `image` 块。
- `gemini` 将 `data:` URL 转换为 `inlineData`，将 `gs://`、`file://` URI 转换为 `fileData`。

模型返回的内容块保存在 `ProviderResponse.Blocks` 中，流式回复也会按同样的结构汇总。
Agent 会将它们写入助手消息的 `Message.Blocks`，同时保留 `Content` 字符串，兼容只处理文本的代码。

## A2A 协议（Agent-to-Agent）

通过 A2A，可以把本地 Agent 发布为服务，也可以调用远程 Agent，包括使用 Google ADK 构建的 Agent。

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

支持的能力包括：

- 通过 Agent Card 获取服务信息。示例路径为 `/.well-known/agent.json`，其他协议版本的路径见 A2A 文档。
- 任务生命周期：提交、执行、完成、失败和取消。
- 同步调用及 SSE 流式输出。
- 文本、文件和结构化数据等多模态消息。
- Webhook 推送通知。
- WebSocket 传输。

## ACP（Agent Communication Protocol）

ACP 使用 JSON-RPC 通信，按会话管理 Agent 的创建和执行：

- 用 `AgentFactory` 创建实例，用 `AgentInstance` 执行任务。
- 管理会话中 Agent 实例的生命周期。
- 通过认证 Provider 接入自己的认证方式。

## AGUI（Agent GUI Events）

如果要在 Web 界面展示 Agent 的执行过程，可以通过 AGUI 的 SSE 接口接收事件：

```go
handler := agui.NewHandler(config)
http.Handle("/events", handler)
```

界面可以接收任务开始和结束、步骤进度、逐段输出的文本、思考块、工具调用、状态快照，以及应用自定义的事件。

## A2UI（Agent-to-UI）

A2UI 让 Agent 用结构化消息描述界面，由客户端负责渲染。它适合生成和更新表单、仪表板、实时信息列表等界面，支持数据绑定和验证，消息可以通过 A2A 或 AG-UI 传输。

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

你可以创建界面、组合组件，用 JSON Pointer 绑定数据，并验证和保存界面状态。
上面的片段包含绑定数据初始化和编码错误处理；多条消息的生成与消费见文档中的完整示例。

## HTTP 服务与多轮对话

在请求中传入同一个 `thread_id`，就可以继续之前的对话。
配置 `Config.Store` 后，`/api/chat` 会先读取该线程保存的状态，完成本轮调用后再保存更新。
如果还配置了 `Config.Checkpoint`，检查点也会使用这个 `thread_id`。

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

通常，不传 `thread_id` 就不会保存对话线程。不过，使用 `session.NewAgentStore(...)` 作为 `Config.Store` 时，`POST /api/chat` 会自动创建新线程，并在响应中返回 `thread_id`。

请求中没有指定 `model`、`response_format` 或 `thinking` 时，会沿用已有配置。
使用会话存储时，先读取服务默认值，再应用线程中保存的配置，最后以本次请求指定的值为准。

状态既可以保存为快照文件，也可以接入 JSONL 会话存储：

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

接入 `session.NewAgentStore(...)` 后，还可以通过以下接口管理线程：

- `POST /api/threads`：创建空线程。
- `GET /api/threads`：列出已保存的线程。
- `GET /api/threads/{key}`：读取元数据和对话记录，每条消息包含 `entry_id`。
- `GET /api/threads/{key}/config`：读取线程级调用配置。
- `PUT /api/threads/{key}/config`：通过 `{ "config": ... }` 保存或清除线程级配置。
- `GET /api/threads/{key}/thinking`：读取线程级思考配置。
- `PUT /api/threads/{key}/thinking`：通过 `{ "thinking": ... }` 保存或清除思考配置。
- `POST /api/threads/{key}/branch`：从当前叶节点创建分支；传入 `{ "entry_id": "..." }` 可从历史记录创建分支。
- `DELETE /api/threads/{key}`：删除线程。

线程级配置示例：

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

如果只想修改该线程的思考设置，可以发送：

```json
{
  "thinking": {
    "display": "summarized",
    "effort": "high",
    "budget": 2048
  }
}
```

无论使用哪种 `agentcore.Store`，都可以通过 `/api/states` 和 `/api/states/{key}` 管理状态。

### 命令行中的多轮对话

`example/cli-chat` 也会在多轮对话中沿用当前线程。输入 `/clear` 或 `/new` 可以开始新对话；使用 `session.NewAgentStore(...)` 时，还可以用 `/branch` 创建分支。

启动前，可以通过环境变量设置思考选项，例如 `THINKING_DISPLAY=summarized`、`THINKING_EFFORT=medium`、
`THINKING_BUDGET=2048` 和 `THINKING_INCLUDE_THOUGHTS=true`。

对话过程中也能调整：用 `/thinking` 查看当前设置，用 `/thinking summarized` 或 `/thinking omitted` 切换摘要显示，
用 `/thinking effort medium`、`/thinking budget -1` 修改深度和预算，用 `/thinking reset` 恢复默认值。

## TUI（终端界面）

项目自带终端界面，不依赖外部 TUI 框架。界面采用 Elm 风格的事件处理方式，渲染时只更新发生变化的部分。各层职责如下：

```text
tui/
├── core/              基础接口、字符工具、SpinnerStyle
├── terminal/          终端输入输出、按键解析（Kitty 协议）、termios（macOS/Linux）
├── theme/             ANSI 样式、语义配色、JSON 热重载
├── component/         Editor、Markdown、Input、SelectList、Loader、Box 等组件
├── chat/              支持滚动对话记录的聊天应用
├── stdio/             Spinner、Renderer、ProgressBar 等标准输入输出工具
├── agentadapter/      agentcore 到聊天事件的适配
└── tui.go             事件循环、浮层系统、差分渲染器
```

各层之间遵循以下约定：

- 上层可以依赖下层，但下层不反向引用上层。
- `tui/chat` 使用自己的事件类型和 `Subscriber` 接口，不直接依赖 `agentcore`，因此也能接入其他消息来源。
- 完整 TUI 使用差分渲染；简单的 stdio 工具则通过 `\r` 更新当前行。

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

这个片段需要将 `github.com/covoyage/covonaut/tui/chat` 导入为 `chat`。
实际应用应在启动前设置提交处理函数或订阅事件；这里仅展示终端界面的启动、等待和清理。

## 模型预设

有些模型需要不同的提示词或工具设置。你可以为模型注册一份预设，设置提示词后缀、不启用的工具、温度和最大轮数；创建 Agent 时，框架会根据 `Config.Model` 自动选择。

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

你在配置中明确指定的值不会被预设覆盖，`SystemPromptSuffix` 会追加到原有系统提示词之后。
预设只在创建 Agent 时应用一次。如果运行中通过 `ApplyCallConfig` 更换模型，需要自行调整相关设置。

## 提示词缓存

多次请求包含相同的提示词前缀时，可以让支持缓存的 Anthropic 兼容服务复用这部分内容。缓存扩展会自动在消息中添加 `cache_control` 断点。
实际能节省多少费用，取决于模型价格、缓存命中率和请求内容。

```go
cfg := agentcore.Config{
    ModelConfig: agentcore.ModelConfig{Model: model, Provider: provider},
    Extensions: []agentcore.Extension{
        agentcore.NewPromptCachingExtension(),
    },
}
```

默认最多设置四个断点，其中会考虑系统提示词和对话尾部附近的位置。
需要调整时，可以使用 `WithMaxBreakpoints`、`WithCacheSystemPrompt` 和 `WithCacheLastN`。
直接使用零值 `PromptCachingExtension{}` 不会添加断点。

## 项目规则

如果项目已经在 `AGENTS.md` 或 `.cursorrules` 中记录了开发约定，可以用规则扩展将这些内容加入系统提示词。也可以指定其他文件，不必把规则写进应用代码。

```go
ext := agentcore.NewRulesExtension() // defaults to AGENTS.md
// or: agentcore.NewRulesExtension(agentcore.WithRulesPaths("AGENTS.md", ".cursorrules"))
cfg := agentcore.Config{
    ModelConfig: agentcore.ModelConfig{Model: model, Provider: provider},
    Extensions: []agentcore.Extension{ext},
}
```

文件不存在时会跳过。如果规则保存在沙箱或嵌入资源中，可以通过 `WithRulesReader` 提供自己的 `FileReader`。

## 扩展

如果一组工具、钩子、中间件和提示词需要一起使用，可以将它们封装为扩展，再统一加入 Agent 配置：

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

## 许可证

[MIT](LICENSE)