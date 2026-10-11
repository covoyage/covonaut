# Troubleshooting

## Configuration Does Not Compile

Promoted fields cannot be keys in `Config` literals. Use nested `ModelConfig`,
`ExecutionConfig`, `CompactionConfig`, and `SkillConfig`, or functional options.
Current APIs use `Router.Steps`, `HandoffConfig.AgentConfig`, explicit Bedrock
credentials, and `vertex.NewGemini` / `NewAnthropic`; older snippets may differ.

## Tools Do Not Execute

Check registration, exact names, model tool support, argument validation, before
hooks, and permissions. Schema selection may hide a required definition without
unregistering the tool. `InvokeTool` exercises the executor without asking the
model to choose a tool.

For unknown names, `UnknownToolHandler` can return guidance to the model. Do not
map unknown names to privileged operations as a generic fallback.

## Repeated or Duplicate Output

Track attempt IDs and handle `message_reset`; do not concatenate failed and
successful attempts. Check event-handler leaks on reused agents and repeated
TUI adapter binding. Distinguish text deltas from whole-output snapshots.

Repeated tool/text loops can trigger bounded recovery. Inspect repeated call
arguments, tool errors, and whether the model actually received useful results.

## Context Still Overflows

Confirm the model's real window, tool schema size, response reservation, and
retained head/tail. Compression is lossy and cannot eliminate protected content.
Offloaded artifacts can become large again when read in full. Consider fewer
tools, smaller outputs, retrieval, or different budgets.

## Conversation Is Missing

Ensure the same thread/key and correct store are used. Replaying every JSONL
entry ignores branches; use `MessagesOnPath`. Memory savers do not survive
restarts. Check schema/version and migration errors before creating new history.

## Resume Fails or Does Nothing Expected

`Resume` requires a restored interrupt reason, not merely an unfinished-looking
transcript. It does not re-execute an approved tool automatically. AG-UI resume
input is not a drop-in replacement for a host-controlled `Agent.Resume` workflow.

## Streams Stop or Hang

Check terminal errors and consumer cancellation. Producers must stop when
`Send` returns false and close on completion. Avoid blocking/reentrant event
handlers. Preserve `http.Flusher` and disable unsuitable proxy buffering.

## Cloud Authentication Fails

Pass required adapter credentials explicitly. Confirm endpoint entitlement,
region, model ID, and temporary-token lifetime. A generic authentication error
does not mean the adapter implements every SDK credential discovery mechanism.

## Pages Works Locally but Not Online

Check `/covonaut/` base paths, repository Pages source, workflow permissions,
deployment approval, and successful artifact upload. GitHub Pages cannot run
the Go server. See [documentation deployment](./contributing#github-pages-deployment).