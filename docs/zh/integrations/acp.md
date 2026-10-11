# ACP 编辑器会话 {#acp-editor-sessions}

ACP 通过标准输入输出传递逐行 JSON-RPC 消息，将编辑器与 Agent 连接起来，并管理会话、工作目录、模型和运行模式。

## 应用需要实现的接口 {#host-interfaces}

应用通过 `AgentFactory` 创建实例，并提供默认模型和模式。实例需要实现 `AgentInstance` 的 `Run`、`Core`、`Model` 和 `Mode` 方法，再将工厂接入会话管理器和服务：

```go
manager := acp.NewSessionManager(acp.SessionManagerConfig{
    AgentFactory: factory,
    SessionStore: metadataStore,
    HomeDir: stateDirectory,
})
protocolServer := acp.NewServer(acp.ServerConfig{
    SessionManager: manager,
    AgentInfo: acp.AgentInfo{Name: "my-agent", Version: "1.0.0"},
})
if err := protocolServer.Run(ctx); err != nil {
    return err
}
```

示例中的依赖由应用提供和管理。协议默认使用 stdin 和 stdout，调试信息必须写入 stderr 或日志系统，不能混入 stdout。服务退出时会清理会话。

## 可选能力 {#optional-capabilities}

| 接口 | 应用职责 |
| --- | --- |
| `PermissionAware` | 受保护操作前请求权限 |
| `FileSystemAware` | 通过客户端文件系统操作 |
| `Rebuildable` | 模型／模式改变时重建实际运行配置 |

编辑器中可能有尚未保存的内容，因此应根据客户端能力使用 `ACPFileSystem`，不能假设磁盘文件始终是最新版本。未实现 `Rebuildable` 时，模型或模式的变更可能只写入元数据，不改变实际运行配置。未实现 `PermissionAware` 时，仅声明支持权限机制也不会自动完成授权检查。

## 持久化与认证 {#persistence-and-authentication}

会话元数据与 Agent 历史分别存储。如果重启后需要恢复两者，就需要分别配置持久化。默认的空元数据存储不会保存持久会话。

`AuthProvider` 提供认证方式的信息，但不能代替网络访问控制或沙箱。应用需要保护进程启动、工作目录、客户端能力和工具执行。取消请求应传递给正在运行的 Agent，工具也需要响应取消。

见 [ACP API](https://pkg.go.dev/github.com/covoyage/covonaut/acp) 与[生命周期](../guide/execution)。