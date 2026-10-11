# MCP 工具、资源与提示词 {#mcp-tools-resources-prompts}

已有服务提供工具或数据时，可以通过 MCP 连接而不重新实现。

## Stdio {#stdio}

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

构造函数会启动进程、完成协议初始化，再获取工具列表。运行前需要安装相关命令，并确认所用软件包可信。`npx -y` 方便演示，但生产环境应固定包版本，并明确设置工作目录和环境变量。

## HTTP {#http}

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

应连接可信端点，并设置客户端的超时等限制。HTTP 客户端会跟踪协议版本和会话标识；开启 server stream 后，还可以接收服务通知。应用仍需处理断连和服务错误。

## 注册与释放 {#register-and-dispose}

运行前通过 `WithExtensions` 注册扩展，远程工具也会经过普通工具执行器。使用结束后关闭所属 Agent，由它释放扩展。如果扩展已创建，但尚未交给 Agent 管理就发生错误，需要自行调用 `Dispose`。

工具前缀只用于避免名称冲突，不会限制权限。处理错误时，应区分服务启动失败和运行中的调用失败。

## 工具之外的发现 {#discovery-beyond-tools}

`DiscoveryClient` 支持列出资源和资源模板、读取或订阅资源，以及获取并展开提示词。`NewDiscoveryExtension` 根据 `DiscoveryToolConfig` 将这些操作暴露为工具，至少需要启用一类发现功能。

服务声明支持相关能力后，回调可以报告资源更新和列表变化。工具变更通知也可以触发注册表刷新，应用需要处理协议协商和刷新失败。远程内容和提示词应按不可信输入处理，限制大小；其中出现命令并不代表允许执行。

## 错误与测试 {#errors-and-testing}

远程服务返回的 `isError` 会保留为工具输出，与传输失败不同。调用失败前可能已经完成部分操作，重试前应核对外部状态。建议先用返回固定结果的测试服务，覆盖初始化、空列表、列表变化、超时、JSON 格式错误、流断开和资源释放。见 [MCP API](https://pkg.go.dev/github.com/covoyage/covonaut/mcp)。