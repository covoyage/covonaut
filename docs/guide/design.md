# Design Principles

Covonaut is a library for building agent applications, not a hosted agent
service. Your application owns provider credentials, persistence, authorization,
deployment, and the user interface.

## Separate Reasoning from Execution

The model proposes tool calls; Go functions execute them. A tool's JSON Schema
describes its inputs, but the tool and its host remain responsible for
permissions and side effects. A prompt is not a security boundary.

This separation lets you change providers without replacing your tool
implementations. It also lets you delegate filesystem, shell, and browser
operations to controlled execution environments.

## Make Control Flow Explicit

An agent loop is useful when the next action depends on a model response. A
workflow is useful when stages and routes are known. A graph makes dependencies
or cyclic state transitions explicit. These abstractions can be composed;
they do not require every step to be an LLM call.

Start with the smallest appropriate abstraction:

| Situation | Start with |
| --- | --- |
| One prompt with optional tools | `agentcore.Agent` |
| An application calling a tool directly | `Agent.InvokeTool` |
| Fixed stages, parallel work, or routing | `workflow` |
| Acyclic dependencies or iterative shared state | `graph` |
| A specialist with a separate responsibility | Agent handoff |
| A remotely hosted specialist | A2A |

## Keep Integration Boundaries Small

`Provider` separates model transport from the agent runtime. `Tool` separates a
model-visible contract from implementation. `Extension` groups integrations.
`Runnable[I, O]` gives components a common invocation and streaming vocabulary.
Stores and telemetry interfaces let the host choose its infrastructure.

Protocol adapters also have separate responsibilities: MCP imports tools, A2A
communicates with agents, AG-UI streams execution events, and A2UI describes UI
surfaces. A terminal interface can consume agent events without becoming part of
the agent's reasoning loop.

## Treat History as Application State

Conversation history is not just a display transcript. Tool-call/result pairing,
multimodal blocks, compaction, and branching affect future model requests.
Persistence therefore needs more structure than concatenating strings.

Snapshots and append-only session trees solve different problems. Choose
snapshots for keyed agent state and sessions when history, branches, and stable
entry IDs matter. Checkpoints support execution recovery rather than merely
rendering previous messages.

## Reliability Is Observable

Retries, failover, tool errors, context compression, and partial streaming output
are visible runtime behavior. A UI must retract failed-attempt output when it
receives a reset event. A host must budget repeated provider calls and make
side-effecting tools idempotent where needed.

Events support live consumers; tracing and metrics support operational analysis.
Neither replaces authorization or durable storage.

## Read the Contracts

Examples show composition, while exported Go documentation defines signatures.
This site follows `main`; use the matching pkg.go.dev version for a pinned module.
Provider-native capabilities remain model-dependent, even behind a common Go
interface.

Continue with [agent execution](./execution), [tool execution](./tools), and
[orchestration](./orchestration).