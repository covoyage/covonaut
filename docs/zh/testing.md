# 测试 Agent 应用 {#testing-agent-applications}

## 从离线开始 {#start-offline}

先实现一个测试 Provider，让它返回预设文本、工具调用和错误。这样可以稳定复现行为，也不会产生模型费用。[恢复](./guide/recovery)和[结构化输出](./guide/structured-output)提供了最小示例。

先单独测试工具函数，再通过 `InvokeTool` 验证执行器钩子、参数检查、取消和错误处理。涉及外部系统时，可以使用测试适配器或本地协议服务。

## 测行为，不只测文本 {#test-behavior-not-just-text}

| 领域 | 断言 |
| --- | --- |
| 工具 | 授权、拒绝、参数转换／验证、钩子顺序、副作用 |
| 循环 | 执行预算、后续输入、引导消息、中断及恢复方法 |
| 流 | 终止错误、输出重置、接收方取消及快照同步 |
| 状态 | 保存加载、分支、迁移、恢复 |
| 图 | 图结构、路由目标、多个前驱结果的合并及步骤上限 |
| 协议 | 初始化、能力、认证、通知、释放 |
| UI | 缩放、长输出、Unicode 宽度、焦点、重连、取消 |

模型的最终措辞可能变化，逐字匹配容易让测试不稳定。更适合验证的是应用要求的行为，例如返回字段、选择的工具、操作次数、历史记录、权限检查结果和最终状态。

## 本地命令 {#local-commands}

从仓库根目录运行：

```sh
go vet ./...
go build ./...
go test ./...
go run ./example/recovery
go run ./example/orchestration
npm --prefix docs run check
```

只修改少量代码时，先运行相关包的测试。涉及共享状态或并行编排时，可以使用竞态检查：

```sh
go test -race ./agentcore ./graph ./workflow ./session
```

## Provider 兼容性 {#provider-compatibility}

`provider-compat` 需要手动启用，运行前应检查配置。真实服务测试应与离线 CI 分开，明确凭据、调用预算、模型名称及测试后的清理方式。

## 文档检查 {#documentation-checks}

站点检查会验证链接、资源和锚点，并编译完整 Go 程序。四个 A2UI 短片段会补齐上下文后编译、离线运行，并检查空输入和界面不存在时的错误处理；两个工作流片段会验证子 Agent 是否关闭。其余短片段不会单独编译。其他重要的离线示例还应实际运行，验证执行结果。

编译通过不代表外部账户和部署权限有效，也不能验证 token 刷新或第三方协议互通。