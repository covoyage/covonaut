# 图与工作流 {#graph-workflow}

先看任务的执行流程，再选择合适的组件。多步骤任务不一定需要多个 Agent，普通函数、工作流或图也可以完成。

| 需求 | 抽象 |
| --- | --- |
| 顺序、并行、路由 | `workflow.Pipeline`、`Parallel`、`Router` |
| 无环依赖 | DAG `graph.Graph` |
| 循环和共享状态迭代 | `graph.PregelGraph` |
| 同一种类型流经节点 | `graph.TypedGraph[T]` |
| 委托或转移控制 | `agentcore.HandoffConfig` |

具体语义和示例见 [Runnable](./runnables)、[图执行](./graphs)、[工作流与交接](./workflows)。

## 工作流 {#workflows}

`Pipeline` 按顺序执行步骤，`Parallel` 并行执行并合并结果，`Router` 根据输入选择分支。接口与选项见 [workflow API](https://pkg.go.dev/github.com/covoyage/covonaut/workflow)。

## 图 {#graphs}

构建 DAG 时，先添加节点和边，再指定入口并编译。Pregel 适合需要反复更新共享状态的流程，每一轮等待当前节点完成后再进入下一轮。编译结果包含诊断和结构信息，也可以导出为 Mermaid 图。

`Runnable[I, O]` 支持 `Invoke`、`Stream`、`Collect` 和 `Transform` 四种调用方式。`TypedNode` 通过编码和解码函数接入使用字符串的图；所有节点使用同一种类型时，可以用 `TypedGraph[T]`，不必转换为字符串。

见 [graph API](https://pkg.go.dev/github.com/covoyage/covonaut/graph) 和 [Runnable API](https://pkg.go.dev/github.com/covoyage/covonaut/agentcore#Runnable)。

## Agent 交接 {#agent-handoffs}

通过 `AgentConfig` 配置负责不同任务的本地 Agent。`HandoffDelegate` 将任务交给另一个 Agent，并取得其结果；`HandoffTransfer` 则将后续控制权转交给它。调用远程 Agent 时可用 [A2A](../integrations/a2a)。[CLI 示例](../examples#cli-chat)展示了协调者如何与天气、数学 Agent 配合。