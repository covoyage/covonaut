# Built-in Tool Catalog

The `tools` extension is a convenient local capability bundle. A production
agent should expose the subset it needs, not automatically receive a shell,
filesystem mutation, and web access because those tools are available.

## Default and Optional Families

| Family | Tools or responsibility | Included by `BuildTools` |
| --- | --- | --- |
| Files | `read`, `edit`, `write_file`, `patch`, `view`, `delete`, `move` | Yes |
| Discovery | `ls`, `grep`, `find`, `glob` | Yes |
| Shell/process | `bash`, `process` | Yes |
| Git | Status, diff, log | Yes |
| Vision | `vision_analyze` | Yes; configure analysis dependencies |
| Web | `web_search`, `web_fetch` | Yes |
| Browser | Browser operations | Only with `Browser` configuration |
| Code execution | `execute_code` | Only with `ExecuteCode` configuration |
| Persistent terminal | Open/send/read/signal/close/list | Only with `Terminal` configuration |
| Desktop | `computer_use` | Only when enabled; macOS capability |

The table describes actual registration behavior, not a promise that every
external binary, search service, browser, or model is installed and usable.
Check package-specific configuration and operation implementations.

## Select Tools Explicitly

```go
cfg := agentcore.NewConfig(
    agentcore.WithModel(model),
    agentcore.WithProvider(provider),
    agentcore.WithTools(
        tools.NewReadTool(workspace, nil),
        tools.NewLsTool(workspace, nil),
    ),
    agentcore.WithValidateArguments(true),
)
```

This example limits registration, unlike tool selection, which only filters
model-visible schemas. `WorkingDir` resolves relative paths; it is not a chroot,
an operating-system sandbox, or an authorization rule for absolute paths.

## Remote Operations

Tool-specific operation interfaces allow the host to replace local filesystem,
shell, or other operations. This is the right boundary for SSH, containers,
remote workspaces, or controlled services. Preserve cancellation, errors,
output limits, and tool semantics when adapting operations.

Read/edit/write/patch have different semantics. Check whether an operation
creates files, requires an exact target, supports normalization, or replaces
multiple occurrences. Inspect a diff and use revision checks before applying a
model-generated edit to shared files.

## Shell and Long-running Work

Bash command allow/block lists are filters, not shell isolation. Complex shell
expansion, indirect executables, network calls, and delegated programs require
host policy. Use positive authorization and a sandbox for untrusted workloads.

Persistent terminal operations retain state and processes between requests.
Track ownership and explicitly close unused terminals. Output truncation and
artifact offloading protect context budgets, not the machine from an unsafe
command.

## Web, Browser, and Vision

Apply destination policy and response limits to fetch/search/browser tools.
Avoid credentials in URLs, arbitrary internal-network access, and unreviewed
downloads. Browser state may include cookies and sessions; isolate it per user.

Vision analysis needs an appropriate model or supplied operations. A tool being
registered does not guarantee image support from the agent's current provider.
Desktop control has OS-specific permissions and a much broader effect boundary
than text generation; enable it deliberately.

See [tool execution](./tools) and the
[tools API](https://pkg.go.dev/github.com/covoyage/covonaut/tools).