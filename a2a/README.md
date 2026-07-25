# A2A Protocol Support for Covonaut

This package implements the [Agent2Agent (A2A) Protocol](https://a2a-protocol.org/) for the Covonaut agent framework, enabling interoperability with other A2A-compliant agents.

## Features

- **Agent Card Discovery**: A2A 1.0 metadata at `/.well-known/agent-card.json` with a legacy alias at `/.well-known/agent.json`
- **A2A 1.0 JSON-RPC**: All 11 standard methods, including push configuration CRUD and extended Agent Cards
- **Public V1 Client**: Standard methods through `client.V1()` with typed request, response, and stream models
- **Security & Extensions**: Agent Card security declarations, authentication, `A2A-Version`, and required `A2A-Extensions` negotiation
- **Legacy Compatibility**: Existing `tasks/*` methods and Go task APIs remain available
- **Task Management**: Full task lifecycle (submitted → working → completed/failed/canceled)
- **Synchronous & Streaming**: Support both request/response and SSE streaming modes
- **Multi-modal Content**: Text, file, and structured data parts
- **Push Notifications**: Webhook-based async updates
- **Handoff Integration**: Seamlessly connect remote A2A agents to local handoff system

## Architecture

```
a2a/
├── types.go      # Core A2A types (AgentCard, Task, Message, Part, Artifact)
├── server.go     # A2A HTTP server with JSON-RPC endpoints
├── client.go     # A2A client for calling remote agents
└── handoff.go    # Integration with agentcore handoff mechanism
```

## Quick Start

### 1. Expose Your Agent as an A2A Server

```go
package main

import (
    "context"
    "log"

    "github.com/covoyage/covonaut/agentcore"
    "github.com/covoyage/covonaut/a2a"
    "github.com/covoyage/covonaut/provider/chatcompat"
)

func main() {
    // Create your agent
    agent := agentcore.New(agentcore.Config{
        Name:         "weather-agent",
        SystemPrompt: "You are a weather assistant.",
        Provider:     chatcompat.New(chatcompat.Config{APIKey: "sk-..."}),
    })

    // Define your agent card
    card := a2a.AgentCard{
        Name:        "weather-agent",
        Description: "Provides weather information for any location",
        URL:         "http://localhost:8080",
        Version:     "1.0.0",
        Capabilities: a2a.AgentCapabilities{
            Streaming: true,
        },
        Skills: []a2a.AgentSkill{
            {
                ID:          "get-weather",
                Name:        "Get Weather",
                Description: "Get current weather for a location",
                Tags:        []string{"weather", "forecast"},
            },
        },
    }

    // Create A2A server
    handler := a2a.NewDefaultAgentHandler(card, agent, agent.Config())
    server := a2a.NewServer(handler)

    log.Println("A2A server listening on :8080")
    log.Fatal(server.ListenAndServe(":8080"))
}
```

### 2. Call a Remote A2A 1.0 Agent

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/covoyage/covonaut/a2a"
)

func main() {
    ctx := context.Background()
    client := a2a.NewClient("http://remote-agent.example.com")

    // Discover agent capabilities
    card, err := client.GetAgentCard(ctx)
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("Connected to: %s\n", card.Name)

    // Use the standards-based client facade.
    response, err := client.V1().SendMessage(ctx, a2a.V1SendMessageRequest{
        Message: a2a.V1Message{
            MessageID: "message-123",
            Role:      "ROLE_USER",
            Parts:     []a2a.V1Part{a2a.NewV1TextPart("What's the weather in Tokyo?")},
        },
    })
    if err != nil {
        log.Fatal(err)
    }

    if response.Message != nil {
        fmt.Printf("Direct response: %+v\n", response.Message)
    } else {
        fmt.Printf("Task state: %s\n", response.Task.Status.State)
    }
}
```

### 3. Streaming Task Updates

```go
stream, err := client.V1().SendStreamingMessage(ctx, a2a.V1SendMessageRequest{
    Message: a2a.V1Message{
        MessageID: "message-456",
        Role:      "ROLE_USER",
        Parts:     []a2a.V1Part{a2a.NewV1TextPart("Write a long story")},
    },
})
if err != nil {
    log.Fatal(err)
}
defer stream.Close()

for {
    ev, err := stream.Recv()
    if errors.Is(err, io.EOF) {
        break
    }
    if err != nil {
        log.Fatal(err)
    }
    if ev.Task != nil {
        fmt.Printf("Initial state: %s\n", ev.Task.Status.State)
    }
    if ev.StatusUpdate != nil {
        fmt.Printf("State: %s\n", ev.StatusUpdate.Status.State)
    }
}
```

### 4. Integrate with Handoff System

Register remote A2A agents as handoff targets:

```go
import (
    "github.com/covoyage/covonaut/agentcore"
    "github.com/covoyage/covonaut/a2a"
)

func setupAgent() *agentcore.Agent {
    // Create remote handoff extension
    remoteAgents := []a2a.RemoteHandoffConfig{
        {
            Name:        "math-expert",
            Description: "Expert in mathematics and calculations",
            URL:         "http://math-agent.example.com",
        },
        {
            Name:        "code-reviewer",
            Description: "Reviews code and suggests improvements",
            URL:         "http://code-agent.example.com",
        },
    }

    ext := a2a.NewRemoteHandoffExtension(remoteAgents)

    agent := agentcore.New(agentcore.Config{
        Name:         "coordinator",
        SystemPrompt: "You coordinate tasks between specialized agents.",
        Extensions:   []agentcore.Extension{ext},
    })

    return agent
}
```

Now the LLM can use tools like `transfer_to_math-expert` and `transfer_to_code-reviewer` to delegate tasks to remote A2A agents.

### 5. Advanced Adapter with Callbacks

```go
adapter := a2a.NewAgentAdapter(card, agent, cfg, &a2a.AdapterCallbacks{
    BeforeRun: func(ctx context.Context, taskID, input string) (string, error) {
        log.Printf("Processing task %s: %s", taskID, input)
        return input, nil
    },
    AfterRun: func(ctx context.Context, taskID, output string, err error) (*a2a.Task, error) {
        if err != nil {
            log.Printf("Task %s failed: %v", taskID, err)
        } else {
            log.Printf("Task %s completed", taskID)
        }
        return nil, nil // use default result
    },
    OnStatusChange: func(taskID string, state a2a.TaskState) {
        log.Printf("Task %s status changed to %s", taskID, state)
    },
})

server := a2a.NewServer(adapter)
```

## API Reference

### Server Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/.well-known/agent-card.json` | A2A 1.0 Agent Card discovery |
| GET | `/.well-known/agent.json` | Legacy Agent Card discovery |
| POST | `/` | JSON-RPC endpoint |

### A2A 1.0 JSON-RPC Methods

| Method | Description |
|--------|-------------|
| `SendMessage` | Send or continue a message |
| `SendStreamingMessage` | Send a message with standard JSON-RPC SSE responses |
| `GetTask` | Get task state with `historyLength` support |
| `ListTasks` | List tasks with cursor pagination and field controls |
| `CancelTask` | Cancel a task |
| `SubscribeToTask` | Subscribe to a non-terminal task |
| `CreateTaskPushNotificationConfig` | Create a task webhook configuration |
| `GetTaskPushNotificationConfig` | Get one webhook configuration |
| `ListTaskPushNotificationConfigs` | List webhook configurations |
| `DeleteTaskPushNotificationConfig` | Delete a webhook configuration |
| `GetExtendedAgentCard` | Retrieve the authenticated extended card |

The server accepts `A2A-Version: 1.0`. Empty version headers and `0.3` retain legacy behavior; unsupported versions return `VersionNotSupportedError`. Configure required extensions with `WithA2AExtensions` and an authenticated extended card with `WithExtendedAgentCard` plus `WithAuth`.

### Legacy JSON-RPC Methods

| Method | Params | Description |
|--------|--------|-------------|
| `tasks/send` | `SendTaskRequest` | Send task synchronously |
| `tasks/sendSubscribe` | `SendTaskRequest` | Send task with SSE streaming |
| `tasks/get` | `GetTaskRequest` | Get task state |
| `tasks/cancel` | `CancelTaskRequest` | Cancel a task |
| `tasks/pushNotification/set` | `SetPushNotificationRequest` | Set webhook |
| `tasks/pushNotification/get` | `{id}` | Get webhook config |

### Part Types

```go
// Text
part := a2a.NewTextPart("Hello world")

// Structured data
part := a2a.NewDataPart(map[string]any{"key": "value"})

// File (base64)
part := a2a.NewFilePartBytes("report.pdf", "application/pdf", base64Data)

// File (URI)
part := a2a.NewFilePartURI("image.png", "image/png", "http://example.com/image.png")
```

## Task States

```
submitted → working → completed
                ↓
            input-required → working → completed
                ↓
            canceled / failed
```

## Testing

```bash
go test ./a2a/ -v
```

## References

- [A2A Protocol Specification](https://a2a-protocol.org/v0.2.6/specification/)
- [A2A Protocol GitHub](https://github.com/google/A2A/)
