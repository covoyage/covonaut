# Covonaut

Build agents, tools, and multi-agent workflows in Go.

Covonaut provides an LLM-tool execution loop, provider adapters, persistent
conversations, graph orchestration, and protocol integrations in one Go module.

## Install

Requires **Go 1.25 or later**.

```sh
go get github.com/covoyage/covonaut/agentcore
```

Continue with the [Quick Start](./guide/quickstart) to run an agent with a real
tool, or explore [configuration](./guide/configuration) if you already have a
provider.

## Choose Your Starting Point

Read [design principles](./guide/design) for the architectural boundaries, or
use the [capability map](./capabilities) to find every major module family.

| Task | Packages |
| --- | --- |
| [Build an agent with custom tools](./guide/tools) | `agentcore`, `provider`, `tools` |
| [Coordinate agents and pipelines](./guide/orchestration) | `graph`, `workflow` |
| [Save and branch conversations](./guide/sessions) | `session`, `store` |
| [Connect tools and remote agents](./integrations/) | `mcp`, `a2a`, `acp` |
| [Deliver a web or terminal experience](./examples) | `server`, `agui`, `a2ui`, `tui` |

## Execution Model

An agent sends the conversation and tool definitions to a provider, executes
requested tools, appends their results, and repeats until the model returns a
final answer or an execution limit is reached. Extensions add tools and hooks;
graphs and workflows coordinate larger processes around this loop.

Use [streaming events](./guide/runtime) for live output and
[persistent sessions](./guide/sessions) for conversations across requests.

## API Reference

Use [pkg.go.dev](https://pkg.go.dev/github.com/covoyage/covonaut) for exported
types and functions. Documentation on this site follows the repository's `main`
branch; match your dependency version when consulting the API reference.

Before deploying, work through the [production checklist](./production) and
[testing guide](./testing). Use [troubleshooting](./troubleshooting) for common
configuration, streaming, persistence, and integration issues.