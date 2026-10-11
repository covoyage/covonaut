# 扩展与生命周期钩子 {#extensions-lifecycle-hooks}

## 扩展接口 {#extension-contract}

扩展需要实现 `Name() string`、`Init(context.Context, *Agent) error` 和 `Dispose() error`，当前 API 没有 `BaseExtension` 类型。除此之外，还可以按需实现以下接口，为 Agent 添加功能。

| 可选接口 | 提供的能力 |
| --- | --- |
| `ToolProvider` | 注册工具 |
| `HookProvider` | 全局 before／after 工具钩子 |
| `MiddlewareProvider` | 工具执行包装 |
| `SystemPromptProvider` | 系统提示词后缀 |
| `TransformContextProvider` | Provider 消息转换 |
| `LifecycleProvider` | Agent 循环生命周期 |
| `EventSnapshotProvider` | 新监听者的当前状态 |

Agent 先初始化扩展，再注册它提供的工具、钩子等功能。使用 `NewWithError` 可以在创建时发现初始化错误。`Agent.Close` 按注册顺序的逆序释放扩展；如果初始化中途失败，扩展应自行清理已经分配的资源。

## 完整工具扩展示例 {#complete-tool-extension}

此程序直接调用注册工具，不请求模型，也无需凭据。代码与英文版共用。

<!--@include: ../../guide/extensions.md{30,79}-->

## 组合顺序 {#composition-order}

扩展按传入顺序注册。钩子和中间件依次加入配置，提示词后缀追加到已有内容，上下文转换也按顺序执行。如果一个扩展需要使用另一个扩展的结果，应据此安排注册顺序。

不要让多个 Agent 共用同一个有状态扩展，除非它明确支持这种用法。`Init` 接收当前 Agent，一些扩展会保存这个引用。

## 生命周期钩子 {#lifecycle-hooks}

可以嵌入 `BaseLifecycleHook`，只实现关心的阶段。Before 钩子返回错误时会停止相应阶段。模型钩子可以检查请求和响应，消息持久化钩子可以检查即将保存的消息。

`LifecycleChain` 按配置顺序执行 before，按相反顺序执行 after。针对整批工具的生命周期钩子与逐个工具的执行钩子不同；`InvokeTool` 只经过工具执行器，不触发完整 Agent 生命周期。

`Run` 会调用 `BeforeAgentRun` 和 `AfterAgentRun`，但 `Continue` 与 `Resume` 不经过这组外层钩子。所有入口都必须遵守的策略，不应只放在这两个钩子中。

## 内置生命周期策略 {#built-in-lifecycle-policies}

`GuardrailHook.Validate` 检查模型响应。被拒绝的内容可以反馈给模型纠正，但这不能代替工具的权限检查。`AuditHook` 记录各阶段的信息，日志需要脱敏。限流钩子也不能代替应用统一管理的用户配额和跨进程限流。

压缩会整体替换部分历史，不会逐条触发消息追加钩子。因此，不能只靠追加事件记录所有历史变化；需要完整审计时，应使用独立的持久化审计存储。