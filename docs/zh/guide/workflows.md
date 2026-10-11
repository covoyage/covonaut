# 工作流与交接 {#workflows-handoffs}

## 顺序流水线 {#sequential-pipeline}

`Pipeline` 将每一步的输出作为下一步的输入，遇到错误立即停止。通过 `NewFuncStep` 可以接入普通 Go 函数，数据验证、格式转换和数据库访问不必专门创建 Agent。

## 并行工作 {#parallel-work}

`Parallel` 将同一个输入传给各步骤，等待全部完成后合并结果。结果按步骤的配置顺序排列，不按完成先后排列；默认合并方式是用分隔符拼接。

分支中的错误会在等待所有步骤结束后返回，已经完成的外部操作不会自动撤销。需要限制单个分支的执行时间或处理部分成功时，应自行设置超时、事务或补偿操作。

## 路由 {#router}

`Router.Route` 返回要执行的步骤名称，该名称必须存在于 `Router.Steps` 中，否则会报错。需要默认分支时，应显式配置。

```go
router := &workflow.Router{
    Route: func(ctx context.Context, input string) string {
        if strings.Contains(input, "code") {
            return "coder"
        }
        return "general"
    },
    Steps: map[string]workflow.Step{"coder": coderStep, "general": generalStep},
}
```

示例中的步骤需要由应用提供。由普通函数组成的完整流水线见[离线示例](./graphs#offline-example)。

## Agent 作为 Step {#agents-as-steps}

`NewAgentStep` 为每次调用创建 Agent，不会自动保留一个长期共享的会话。如果扩展需要释放连接等资源，建议使用自己的函数 Step：创建 Agent 后安排 `defer Close`，再调用 `Run`。

```go
step := workflow.NewFuncStep(func(ctx context.Context, input string) (string, error) {
    child, err := agentcore.NewWithError(reviewConfig)
    if err != nil {
        return "", err
    }
    defer child.Close()
    return child.Run(ctx, input)
})
```

`RunSequentialAgents` 接受已有 Agent，将前一个的最终输出传给下一个。调用方负责管理这些实例的历史和关闭。不要并发使用同一个 Agent 处理互不相关的会话。

## 委托或转移 {#delegate-or-transfer}

`HandoffConfig` 为目标 Agent 指定名称和 `AgentConfig`。Delegate 执行后将结果返回给协调者，Transfer 则转交后续控制权。拆分 Agent 时应考虑各自的任务、上下文、工具和权限，而不只是为角色取不同名称。委托也不会增加用户原有的权限。

[CLI](../examples#cli-chat)示范本地专家；远程目标用 [A2A](../integrations/a2a)。

## Agent-as-Tool 与任务路由 {#agent-as-tool-and-task-routing}

`AgentAsTool` 将 Agent 包装成接受 `input` 参数的工具。需要与父 Agent 共用事件总线时，使用 `AgentAsToolWithEventBus`；普通构造器不会自动转发全部事件。`TaskTool` 根据名称选择 `TaskOption`，再将输入交给相应工具。

```go
managedAgentTool := func(cfg agentcore.Config) *agentcore.Tool {
    tool := agentcore.AgentAsTool(cfg)
    tool.Func = func(ctx context.Context, args json.RawMessage) (any, error) {
        var params struct {
            Input string `json:"input"`
        }
        if err := json.Unmarshal(args, &params); err != nil {
            return nil, err
        }
        if params.Input == "" {
            return nil, errors.New("input must not be empty")
        }
        child, err := agentcore.NewWithError(cfg)
        if err != nil {
            return nil, err
        }
        defer child.Close()
        return child.Run(ctx, params.Input)
    }
    return tool
}
delegate := agentcore.TaskTool("delegate", []agentcore.TaskOption{
    {Name: "reviewer", Description: "Review code", Tool: managedAgentTool(reviewConfig)},
    {Name: "writer", Description: "Write a draft", Tool: managedAgentTool(writeConfig)},
})
```

包装器保留 `AgentAsTool` 的工具描述和参数定义，但替换执行函数，在创建时检查错误，并在每次调用结束后关闭子 Agent。片段应放在应用函数中，配置由应用提供，按需导入 `context`、`encoding/json` 和 `errors`。委托前仍需在外层工具或应用中检查权限。

`TaskTool` 直接调用所选工具的函数，不会再经过该工具的执行器钩子。必要的权限检查应放在外层工具、子 Agent 或应用中，也可以改为显式使用 `InvokeTool`。

当前这些便利适配器创建子 Agent 后，不会在每次调用结束时显式调用 `Close`。如果扩展持有需要释放的资源，应编写自己的包装器，负责创建、运行和关闭实例。

## 消息总线交付 {#message-bus-delivery}

`MessageBus` 按 topic 分发消息，不会共享 Agent 状态。发布不会等待订阅者，缓冲区满时消息会丢失，因此它不能代替持久队列，也不保证事务性投递。只在允许丢消息的场景使用，并及时退订和关闭发布方。