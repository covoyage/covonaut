# Package Index

Use pkg.go.dev for exported signatures, constructors, and type documentation.
The site follows `main`; choose the appropriate version on pkg.go.dev for your
installed dependency.

## Runtime and Orchestration

| Package | Responsibility |
| --- | --- |
| [agentcore](https://pkg.go.dev/github.com/covoyage/covonaut/agentcore) | Agent loop, tools, configuration, events, hooks, state, runnables |
| [graph](https://pkg.go.dev/github.com/covoyage/covonaut/graph) | DAG, Pregel, typed graphs |
| [workflow](https://pkg.go.dev/github.com/covoyage/covonaut/workflow) | Pipelines, parallel execution, routing |
| [components](https://pkg.go.dev/github.com/covoyage/covonaut/components) | RAG component contracts |
| [tools](https://pkg.go.dev/github.com/covoyage/covonaut/tools) | Built-in tools and operation adapters |
| [skill](https://pkg.go.dev/github.com/covoyage/covonaut/skill) | Skill loading and metadata |
| [prompt](https://pkg.go.dev/github.com/covoyage/covonaut/prompt) | Prompt utilities |
| [filequeue](https://pkg.go.dev/github.com/covoyage/covonaut/filequeue) | Process-local path mutation serialization |
| [fuzzy](https://pkg.go.dev/github.com/covoyage/covonaut/fuzzy) | Normalized text matching and replacement |
| [pkg/util](https://pkg.go.dev/github.com/covoyage/covonaut/pkg/util) | Environment/string/error helpers |

## Persistence and Interfaces

| Package | Responsibility |
| --- | --- |
| [session](https://pkg.go.dev/github.com/covoyage/covonaut/session) | JSONL conversation trees and agent-store adapter |
| [store](https://pkg.go.dev/github.com/covoyage/covonaut/store) | State storage implementations |
| [server](https://pkg.go.dev/github.com/covoyage/covonaut/server) | HTTP/SSE and thread endpoints |
| [tui](https://pkg.go.dev/github.com/covoyage/covonaut/tui) | Terminal UI engine |
| [mcp](https://pkg.go.dev/github.com/covoyage/covonaut/mcp) | MCP clients and tool extensions |
| [a2a](https://pkg.go.dev/github.com/covoyage/covonaut/a2a) | Remote agent tasks and discovery |
| [ard](https://pkg.go.dev/github.com/covoyage/covonaut/ard) | ARD resource publishing, discovery, and search |
| [acp](https://pkg.go.dev/github.com/covoyage/covonaut/acp) | Session-based communication |
| [agui](https://pkg.go.dev/github.com/covoyage/covonaut/agui) | UI execution events |
| [a2ui](https://pkg.go.dev/github.com/covoyage/covonaut/a2ui) | Declarative UI surfaces and data binding |

## Providers

- [chatcompat](https://pkg.go.dev/github.com/covoyage/covonaut/provider/chatcompat)
- [anthropic](https://pkg.go.dev/github.com/covoyage/covonaut/provider/anthropic)
- [gemini](https://pkg.go.dev/github.com/covoyage/covonaut/provider/gemini)
- [bedrock](https://pkg.go.dev/github.com/covoyage/covonaut/provider/bedrock)
- [vertex](https://pkg.go.dev/github.com/covoyage/covonaut/provider/vertex)
- [copilot](https://pkg.go.dev/github.com/covoyage/covonaut/provider/copilot)
- [mistral](https://pkg.go.dev/github.com/covoyage/covonaut/provider/mistral)