# Examples

Run these commands from the repository root. Examples requiring a model use
your own provider credentials and may incur charges.

## Quick Start

```sh
go run ./example/quickstart
```

Set `OPENAI_API_KEY` and `AGENT_MODEL`. The [full tutorial](./guide/quickstart)
explains optional compatible-service configuration.

## CLI Chat

```sh
go run ./example/cli-chat
```

The CLI demonstrates tools, specialist handoffs, streaming, model selection,
skills, and conversation management. Inspect the
[example source](https://github.com/covoyage/covonaut/tree/main/example/cli-chat)
for provider-specific environment variables and optional persistence settings.

## A2A Server and Client

Run the server in one terminal and the client in another:

```sh
go run ./example/a2a-server
```

```sh
go run ./example/a2a-client
```

The server uses a stub provider, so it needs no model key. It listens on port
8080 by default; `PORT` overrides the server port. See the client source for its
target URL options when changing the port.

::: details Complete server source
<<< @/../example/a2a-server/main.go
:::

## Terminal UI

```sh
go run ./example/tui-demo
go run ./example/tui-demo2
go run ./example/tui-demo3
```

Run one demo at a time in an interactive terminal. The TUI is layered around
terminal I/O, themes, components, chat state, and an agent adapter. It is not a
browser UI. Refer to each
[demo source](https://github.com/covoyage/covonaut/tree/main/example)
for its configuration and behavior.

## Additional Complete Examples

| Command | What it demonstrates | Credentials |
| --- | --- | --- |
| `go run ./example/recovery` | Interrupt, checkpoint, restore, resume | None |
| `go run ./example/orchestration` | Typed graph, pipeline/router, stream aggregation | None |
| `go run ./example/http-server` | Authenticated local HTTP chat with persisted threads | Provider key/model and local API token |

See [recovery](./guide/recovery), [graphs](./guide/graphs), and
[HTTP API](./integrations/http) for full source and expected behavior.

## Compatibility Suites

`example/provider-compat` contains provider compatibility tests, and `example/itk`
implements an A2A integration test-kit agent. These are validation tools rather
than recommended first examples.