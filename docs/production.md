# Production Checklist

## Execution Boundary

- Expose only needed tools and enforce authorization in the host/tool.
- Set context deadlines, finite turns/steps, and bounded tool concurrency.
- Prefer serial or mixed execution unless concurrent tools are explicitly safe.
- Use a sandbox for untrusted shell, code, browser, or desktop operations.
- Make retries and recovery safe for side effects through idempotency or reconciliation.

## Provider Boundary

- Verify model tool-calling, image, thinking, and structured-output support.
- Handle startup, transport, malformed-response, and mid-stream errors separately.
- Budget retries and failover calls; instrument every provider in the chain.
- Manage credential rotation and temporary-token expiry explicitly.
- Test replayed history when switching provider families.

## State Boundary

- Authorize access to every thread, snapshot, checkpoint, and artifact.
- Use durable storage where process restarts must be survivable.
- Choose retention, backup, deletion, and schema migration policies.
- Keep stable message/tool-call/entry IDs through branches and transforms.
- Serialize concurrent work for one conversation unless your host implements a safe policy.

## Interface Boundary

- Authenticate API routes; CORS does not provide authentication.
- Disable unused administrative skill APIs and protect reload operations.
- Apply body, rate, model-use, and output limits to every mounted handler.
- Preserve SSE flushing and configure proxy buffering/timeouts.
- Reconcile reconnecting clients from persisted snapshots rather than guaranteed deltas.
- Retract failed-attempt output and unsubscribe request-scoped handlers.

## Operational Boundary

- Test graceful shutdown with active requests and tools.
- Close agents, MCP clients, terminals, and UI resources at their owning scope.
- Redact sensitive information in logs, traces, stored history, and artifacts.
- Track model usage, tool failures, retries, compaction, and outstanding review work.
- Validate restore paths and upgrades against realistic persisted fixtures.

The library provides mechanisms for these boundaries, not a complete hosted
security or multi-tenant platform. Use [design principles](./guide/design) and
[testing](./testing) to turn this list into application-specific acceptance tests.