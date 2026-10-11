# 工具与扩展 {#tools-extensions}

工具让模型能够调用应用中的函数。每个工具需要提供名称、用途说明、参数的 JSON Schema，以及接收 `json.RawMessage` 的执行函数。

## 自定义工具 {#custom-tools}

[快速开始](./quickstart#complete-source)提供了完整示例。工具函数应将参数解码到 Go 结构体中，返回执行错误，并在网络或进程操作中响应 context 的取消信号。

`WithValidateArguments(true)` 按工具的参数定义验证输入。但参数格式正确不代表操作被允许：文件路径、网络地址和删除等操作仍需单独检查权限。

## 内置工具 {#built-in-tools}

```go
ext := tools.NewExtension(tools.ExtensionConfig{
    WorkingDir: "/path/to/project",
})
cfg := agentcore.NewConfig(
    agentcore.WithModel(model),
    agentcore.WithProvider(provider),
    agentcore.WithExtensions(ext),
)
```

`tools` 包提供文件、Shell、搜索、浏览器和代码执行等工具。通过替换底层操作接口，也可以让工具在远程系统或沙箱中执行。哪些工具默认启用、哪些需要额外配置，见[工具目录](./builtin-tools)。

文件查询按路径的字典序输出；grep 先按完整路径排序，再按行号排序，格式化结果时在排序后应用数量上限。
ripgrep 后端会在输出匹配项前按路径排序。fd 后端需要收集全部匹配路径，再排序和截断，
因此在大型目录中会增加扫描时间和内存开销。自定义 `Glob` 后端如果在内部截断结果，
必须自行保证所选结果集合稳定；只对已截断的结果排序，不能保证选中的文件集合一致。

::: warning 执行边界
命令的允许或拒绝列表不能代替沙箱。应只注册任务需要的工具，在应用中检查权限，并隔离不可信的代码和命令。
:::

## 扩展和钩子 {#extensions-and-hooks}

### 执行顺序 {#execution-order}

`Agent.InvokeTool(ctx, name, args)` 通过工具执行器调用工具，而不是直接调用 `Tool.Func`。如果程序发起的调用也需要经过审计和权限钩子，应使用这个方法。

1. 工具专用 before 钩子。
2. 全局 before 钩子。
3. 包装实际执行的中间件。
4. 全局 after 钩子。
5. 工具专用 after 钩子。

Before 钩子返回错误时，调用会被拒绝；after 钩子可以检查执行结果和错误。未知工具、参数验证失败或被钩子拒绝，都不算成功执行。直接调用 `GetTool(...).Func` 会绕过这些检查。

```go
result, err := agent.InvokeTool(ctx, "greet", json.RawMessage(`{"name":"Ada"}`))
if err != nil {
    return err
}
fmt.Println(result)
```

### 中间件与副作用 {#middleware-and-side-effects}

`TimeoutMiddleware` 为工具设置执行超时，但工具本身需要响应取消信号。`RetryMiddleware` 可能重复执行同一个工具，因此只适合可安全重试的操作。付款或写入等操作需要幂等键或其他去重措施。

日志可以只记录工具名称和参数大小，不必保存完整参数。自定义审计日志应过滤密钥等敏感信息。

### 注册 {#registration}

扩展可以通过可选接口提供工具、提示词、钩子和中间件。实现基本生命周期方法及所需接口后，通过 `Config.Extensions` 或 `WithExtensions(...)` 注册。

工具的 `Before` 和 `After` 分别在执行前后运行。全局钩子和中间件作用于所有工具，生命周期钩子则在 Agent 的不同阶段运行。内置扩展还包括缓存、项目规则、技能和 [MCP](../integrations/mcp)。

可以用 `RegisterTools`、`UnregisterTools`、`ToolNames` 和 `GetTool` 管理当前注册的工具。注册工具不等于允许任何用户执行它，权限仍需单独检查。完整示例见[扩展指南](./extensions)。

## 执行模式 {#execution-modes}

| 模式 | 调度 |
| --- | --- |
| `ModeSerial` | 串行执行 |
| `ModeParallel` | 未显式声明不安全的调用进入并行池 |
| `ModeMixed` | 仅显式声明安全的调用进入并行池 |

`ConcurrencySafe` 是 `*bool`，nil 表示没有声明是否并发安全。Parallel 模式允许这类工具并行，Mixed 模式只允许明确标记为安全的工具并行。不安全的工具会单独执行，不与其他调用重叠。

尚未检查所有工具的并发安全性时，建议使用串行或 Mixed 模式。并发上限设为正数可以限制同时执行的数量；执行器中零表示不限制。

## 分别向模型和用户提供结果 {#model-and-user-results}

`DualToolOutput` 可以通过 `ForLLM` 向模型提供精简结果，通过 `ForUser` 向用户展示详细内容。`SilentResult` 将结果提供给模型但不显示；`BlockCarrier` 可以在模型服务支持时附带多模态内容块。

这些设置只控制结果如何呈现，不能保证内容保密。界面仍需检查访问权限。大型结果可通过 `WithArtifactOffload` 保存到 `ArtifactStore`，在上下文中仅保留读取引用。

见 [Tool](https://pkg.go.dev/github.com/covoyage/covonaut/agentcore#Tool)、[Extension](https://pkg.go.dev/github.com/covoyage/covonaut/agentcore#Extension) 和 [tools](https://pkg.go.dev/github.com/covoyage/covonaut/tools)。