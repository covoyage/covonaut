# 内置工具目录 {#built-in-tool-catalog}

`tools` 扩展提供常见的本地操作工具。生产环境应只注册任务需要的工具，不要默认开放 Shell、文件修改和网络访问。

## 默认与可选工具 {#default-and-optional-families}

| 类别 | 能力 | `BuildTools` 默认注册 |
| --- | --- | --- |
| 文件 | read、edit、write_file、patch、view、delete、move | 是 |
| 发现 | ls、grep、find、glob | 是 |
| Shell／进程 | bash、process | 是 |
| Git | status、diff、log | 是 |
| 视觉 | vision_analyze | 是，需分析依赖 |
| 网络 | web_search、web_fetch | 是 |
| 浏览器 | 浏览器操作 | 仅配置 `Browser` 后 |
| 代码执行 | execute_code | 仅配置 `ExecuteCode` 后 |
| 持久终端 | 打开、发送、读取、信号、关闭、列出 | 仅配置 `Terminal` 后 |
| 桌面 | computer_use | 显式启用，macOS 能力 |

工具注册成功不代表相关程序、搜索服务、浏览器或模型已经可用。使用前需要检查每个工具的配置及其依赖。

## 显式选择工具 {#select-tools-explicitly}

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

这里直接限制注册的工具，而工具选择器只过滤发送给模型的定义。`WorkingDir` 仅用于解析相对路径，不能代替 chroot、沙箱或路径权限检查。

## 远程操作 {#remote-operations}

工具的 operation 接口允许替换底层文件、Shell 等操作，例如接入 SSH、容器、远程工作区或受控服务。自定义实现需要响应取消信号、正确返回错误、限制输出大小，并保持工具原有的行为。

读取、编辑、写入和补丁工具对文件创建、文本匹配、规范化和替换的处理不同，使用前应查看各自说明。将模型提出的修改应用到共享文件之前，需要检查 diff 和文件版本。

## Shell 与长期工作 {#shell-and-long-running-work}

命令的允许或拒绝列表只是过滤规则，不能隔离命令间接启动的程序、Shell 展开或网络访问。执行不可信任务时，应只授权明确允许的操作，并使用沙箱。

持久终端会保留进程和状态，应用需要记录所属用户，并关闭不再使用的终端。截断输出或将结果保存为产物只能控制上下文大小，不能阻止危险命令损坏系统。

## 网络、浏览器与视觉 {#web-browser-and-vision}

网络工具应限制目标地址和响应大小，避免在 URL 中携带凭据，并阻止未经授权的内网访问和下载。浏览器可能保留 cookie 和登录状态，需要按用户隔离。

视觉工具需要支持图片的模型或相应的底层实现，不能只根据注册成功判断它可用。桌面控制还需要操作系统权限，启用前应评估它可能影响的应用和数据。

见[工具执行](./tools)和 [tools API](https://pkg.go.dev/github.com/covoyage/covonaut/tools)。