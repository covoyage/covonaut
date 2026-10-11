# Quick Start

Run a small agent that calls a `greet` tool and returns a response.

## Prerequisites

- Go 1.25 or later.
- An API key for a Chat Completions-compatible service.
- A model available to your account that supports tool calling.

## Run the Example

From a checkout of the repository:

```sh
git clone https://github.com/covoyage/covonaut.git
cd covonaut
export OPENAI_API_KEY='your-api-key'
export AGENT_MODEL='your-tool-capable-model'
go run ./example/quickstart
```

For another compatible service, also set `OPENAI_BASE_URL` to its API base URL
(for example, `https://api.deepseek.com/v1`). Leave it unset for OpenAI.
The request uses your provider account and may incur charges.

::: warning Credentials
Keep keys in your local environment or secret manager. Never commit them or
enter them into a public documentation page.
:::

## Complete Source

This is the same source compiled by the documentation checks.

<<< @/../example/quickstart/main.go

## What Happens

`NewConfig` applies functional options. `WithTools` registers the tool's JSON
Schema and implementation, and `WithValidateArguments(true)` enables argument
validation. The provider receives the prompt and tool definition; if it requests
`greet`, the agent executes it and sends the result back to the model.

The run has a two-minute context deadline and a five-turn limit. `Close` releases
agent resources. Model behavior can vary, so the final response is not expected
to match an exact string.

## Use Your Own Project

In an existing Go module, install Covonaut and use the source above as a starting
point:

```sh
go get github.com/covoyage/covonaut/agentcore
```

Next, learn [configuration](./configuration), [tools and extensions](./tools),
and [provider compatibility](./providers).