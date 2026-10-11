# MCP Tools, Resources & Prompts

MCP connects external capabilities to an agent. Use it when an existing server
already owns tools or data, rather than rewriting that integration in the agent.

## Stdio

```go
extension, err := mcp.NewStdioExtension(ctx, mcp.StdioConfig{
    Name: "filesystem",
    Command: "npx",
    Args: []string{"-y", "@modelcontextprotocol/server-filesystem", "/workspace"},
    ToolPrefix: "fs.",
})
if err != nil {
    return err
}
```

Construction launches the process, negotiates initialization, and lists tools.
The host needs the command installed and must trust the server package. Pin
external server versions in production; `npx -y` is a convenience, not a supply
chain policy. Set working directory and environment explicitly where required.

## HTTP

```go
extension, err := mcp.NewHTTPExtension(ctx, mcp.HTTPConfig{
    Name: "remote-tools",
    Endpoint: "https://tools.example.com/mcp",
    ToolPrefix: "remote.",
    Headers: map[string]string{"Authorization": "Bearer " + token},
    RequestTimeout: 30 * time.Second,
    EnableServerStream: true,
})
if err != nil {
    return err
}
```

Configure a trusted endpoint and appropriate client limits. HTTP sessions track
negotiated protocol/session identifiers. Optional server streams support incoming
notifications; applications still need to handle disconnects and server errors.

## Register and Dispose

Register the resulting extension with `WithExtensions(extension)` before agent
execution. Its tools enter the ordinary executor pipeline. Close the owning
agent when finished. If agent construction fails before registration owns the
extension, dispose the unowned extension yourself.

Use prefixes when connecting multiple servers. Prefixes avoid naming collisions
but do not restrict execution permissions. Handle startup failures separately
from runtime tool errors.

## Discovery Beyond Tools

`DiscoveryClient` exposes resources, resource templates, reads, subscriptions,
prompts, and prompt expansion. `NewDiscoveryExtension` can expose resource or
prompt operations as tools using `DiscoveryToolConfig`. At least one discovery
category must be enabled.

Discovery callbacks report resource updates and list changes. Servers must
advertise corresponding capabilities. Tool list-change notifications can refresh
the extension's registry; observe capability and refresh errors rather than
assuming a remote registry is static forever.

Resource content and prompt instructions are untrusted input unless your host
establishes otherwise. Apply content limits and do not treat a remote prompt as
authorization to run shell commands.

## Errors and Testing

A remote `isError` response is preserved as tool output for model correction;
a transport failure is different. A partially completed remote side effect may
need reconciliation before retries.

Test initialization, empty/changed tool lists, timeout, malformed JSON,
mid-stream disconnect, and disposal. Use deterministic server fixtures before
testing against a paid provider. See the
[MCP API](https://pkg.go.dev/github.com/covoyage/covonaut/mcp).