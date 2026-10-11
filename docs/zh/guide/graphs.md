# 图执行 {#graph-execution}

## DAG {#dag}

`Graph` 用有向无环图描述各个命名步骤之间的依赖。添加节点和边时，需要检查 `AddNode` 和 `AddEdge` 的错误，再指定入口并编译。编译会检查依赖和执行顺序，同一层中互不依赖的活动节点可以并行执行。

`AddConditionalEdge` 根据节点输出选择下一节点，添加时需要声明所有可能的目标。同一个来源节点不能同时使用静态出边和条件路由。完整声明目标后，编译器才能检查结构并生成可视化结果。

`CompileOptions.StateFn` 创建运行状态，节点可以通过 context 取得它。状态锁只保护状态自身的方法，不会保护其中引用的外部对象。运行期间不要修改图的构建器；需要改变节点或依赖时，应构建新图。

## 类型边界 {#typed-boundaries}

`TypedNode[I, O]` 通过 `Decode` 和 `Encode` 在 Go 类型与字符串之间转换，从而接入普通图。JSON 数据可以使用 `JSONDecode[T]` 和 `JSONEncode[T]`。转换失败应返回执行错误，不能悄悄用零值继续。

`TypedGraph[T]` 的所有节点使用同一种类型。当节点有多个前驱，或图产生多个最终结果时，需要通过 `TypedCompileOptions[T].Merge` 指定合并方式。不要让业务结果取决于分支碰巧以什么顺序完成。

按值传递结构体不会深拷贝其中的 slice、map 或指针。并行节点不应修改共享输入；确实需要修改时，应先复制相应数据。

## 离线示例 {#offline-example}

```sh
go run ./example/orchestration
```

这个离线示例先用类型图整理文档并统计其长度，再通过工作流选择发布方式，最后展示如何收集流式结果。运行时无需模型凭据。

<<< @/../example/orchestration/main.go

## Pregel {#pregel}

`PregelGraph` 支持反复更新共享状态的循环。每一轮（super-step）先向活动节点提供状态快照，等待它们完成后合并更新，再调度下一轮。节点不是直接并发修改同一个 map。

通过 `SetConditionalEdgeWithTargets` 声明路由目标，用 `PregelEnd` 结束执行，并设置轮数上限。应避免多个节点同时更新同一个 key。快照复制支持常见的 map、slice 和消息，但不保证任意外部对象或复杂指针都已深拷贝。

## 统一 Runner 与流式 {#unified-runner-and-streaming}

`NewDAGRunner` 和 `NewPregelRunner` 提供统一调用入口、链路追踪和可选检查点，但两种图的调度和触发规则仍然不同。

`Runner.RunStream` 当前只支持 DAG，不支持根据输出选择路径的条件边。普通节点接入流式执行时，会先收集输入，再将结果包装成流；节点内部的阻塞操作不会因此变成逐段输出。

## 结构检查与恢复 {#introspection-and-recovery}

编译后的图和 Runner 可以通过 `AsTool(graph.ToolConfig{...})` 转成工具，让 Agent 使用 `input` 参数调用固定流程。Pregel 与字符串输入输出之间的适配使用状态中的 `"input"` 和 `"output"` key，节点需要按此约定读取和写入。

上线前可以通过 `Info`、诊断信息和 Mermaid 图，检查节点是否可达、路由目标是否完整。

图使用 `CheckpointStore` 和 `InterruptableGraph`，与 Agent 的 `CheckpointSaver` 不是同一套接口。可以在指定节点前后暂停，再按检查点 ID 恢复。应用需要测试自己的图结构、路由和重启流程；恢复不能保证外部操作只执行一次。