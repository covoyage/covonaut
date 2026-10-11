# Capability Map

This map covers the repository's public module families and links to practical
guides. It distinguishes runtime mechanisms from application responsibilities.

| Capability | Guide | Host still owns |
| --- | --- | --- |
| Agent loop and execution entry points | [Execution](./guide/execution) | Run lifetime and user isolation |
| Configuration groups and options | [Configuration](./guide/configuration) | Model-specific budgets |
| Tools, hooks, concurrency, dual output | [Tools](./guide/tools) | Authorization and side effects |
| Files, shell, Git, web, browser, terminals | [Built-in catalog](./guide/builtin-tools) | Sandbox and external dependencies |
| Extensions and lifecycle composition | [Extensions](./guide/extensions) | Resource ownership and ordering |
| System prompts, rules, profiles, caching | [Prompts](./guide/prompts) | Trust and provider caching policy |
| Skills and progressive disclosure | [Skills](./guide/skills) | Trusted packages and administration |
| Messages, images, thinking, history conversion | [Messages](./guide/messages) | Data policy and provider compatibility |
| Typed JSON output | [Structured output](./guide/structured-output) | Semantic validation |
| Provider adapters | [Providers](./guide/providers) | Credentials and model entitlement |
| Retry, failover, selection, artifacts | [Reliability](./guide/runtime) | Budgets, retention, safe replay |
| Context engines and summaries | [Compaction](./guide/context) | Useful budgets and information loss |
| Events and OpenTelemetry | [Observability](./guide/observability) | Exporters, redaction, reconciliation |
| Four-mode execution and streams | [Runnables](./guide/runnables) | Correct aggregation/cancellation |
| DAG, Pregel, typed graphs, graph-as-tool | [Graphs](./guide/graphs) | Merge semantics and termination |
| Pipelines, routing, handoffs, task tools | [Workflows](./guide/workflows) | Specialist boundaries and cleanup |
| Snapshots, JSONL trees, branches | [Sessions](./guide/sessions) | Durability, permissions, retention |
| Checkpoints, interruption, restoration | [Recovery](./guide/recovery) | Human approval and exactly-once policy |
| HTTP/SSE and thread operations | [HTTP](./integrations/http) | Authentication and deployment |
| MCP tools, resources, prompts | [MCP](./integrations/mcp) | Trusted server and transport policy |
| A2A tasks and remote agents | [A2A](./integrations/a2a) | Remote auth and compatibility |
| ACP editor sessions | [ACP](./integrations/acp) | Factory and capability implementation |
| AG-UI execution events | [AG-UI](./integrations/agui) | Web renderer and state reconciliation |
| A2UI declarative surfaces | [A2UI](./integrations/a2ui) | Catalog-aware client renderer |
| Terminal UI and event adapter | [TUI](./guide/tui) | Terminal capabilities and run lifetime |
| RAG Loader/Embedder/Indexer/Retriever | [Retrieval](./guide/rag) | Concrete backends and document policy |
| Templates, file queue, fuzzy matching, util | [Utilities](./guide/utilities) | Validation and mutation safety |

## Reading Paths

- First agent: Quick Start, Configuration, Tools, Providers.
- Interactive application: Execution, Messages, Observability, HTTP or TUI.
- Multi-step application: Runnables, Graphs, Workflows, Recovery.
- Production deployment: Reliability, Sessions, Production Checklist, Testing.

The [package index](./reference) remains the symbol-level entry point. This site
follows `main`, and the map is not a claim that every external service or model
supports every listed feature.