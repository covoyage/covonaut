# HTTP API

## Complete Server

The example mounts `server.Handler` in a host-owned HTTP server, protects all
routes with a bearer token, disables skill administration, limits chat bodies,
and persists threads using JSONL sessions. It binds loopback only.

```sh
export OPENAI_API_KEY='your-provider-key'
export AGENT_MODEL='your-model'
export CHAT_API_TOKEN='your-local-access-token'
go run ./example/http-server
```

`SESSION_DIR` selects the storage directory; it defaults to `./sessions`.
`OPENAI_BASE_URL` selects a compatible provider endpoint if needed. Keep tokens
out of shell history and source control in real deployments.

<<< @/../example/http-server/main.go

## Chat Requests

```sh
curl http://127.0.0.1:8080/api/chat \
  -H "Authorization: Bearer $CHAT_API_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"message":"Hello","stream":false}'
```

A session-backed store creates a thread when none is supplied. Keep the returned
`thread_id` and send it with subsequent requests to continue that conversation.
For streaming, set `stream` to true and use a client that parses SSE; `curl -N`
is useful for local inspection.

`model`, `response_format`, `thinking`, and `skills` are per-request settings.
The effective precedence is server defaults, then saved thread configuration,
then supplied request overrides. An omitted setting is not necessarily a request
to clear a saved setting.

## Threads, Branches, and State

[Sessions and HTTP](../guide/sessions#thread-endpoints) lists thread endpoints.
Branch a historical message using its returned `entry_id`, not an assumed array
position. Thread config and thinking endpoints can persist or clear their
overrides using their documented request envelope.

Generic `/api/states` endpoints are separate from the session-backed thread
view. The `/agui/{path}` mount uses [AG-UI request semantics](./agui), not the
`ChatRequest` JSON above.

## Security and Lifetime

The framework server is not a complete authentication service. The example's
shared token is suitable for a local single-user demo, not tenant isolation.
Production hosts must authorize every thread/state operation, bind ownership to
an authenticated user, restrict allowed model overrides, and enforce quotas.

CORS is a browser policy, not authentication. Configure explicit origins when
needed; do not expose unrestricted state endpoints behind a permissive proxy.
`SetMaxRequestBodyBytes` limits the server's chat/admin decoding paths, but do
not assume it also limits a separately mounted AG-UI handler. Apply host-level
body limits and rate limits to every route.

When mounting `Handler`, the host owns HTTP shutdown; close the application
after draining requests. Long-lived SSE needs appropriate proxy buffering and
timeout configuration. Bound model/tool execution separately: HTTP read timeouts
alone do not bound reasoning duration.