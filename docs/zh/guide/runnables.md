# Runnable 与流 {#runnables-streams}

`Runnable[I, O]` 是通用数据流接口，本身不会启动 Agent 循环。它让普通函数和流式组件能够组成同一条流水线，不必调用模型。

## 四种模式 {#four-modes}

| 模式 | 输入 | 输出 |
| --- | --- | --- |
| `Invoke` | 单个 `I` | 单个 `O` |
| `Stream` | 单个 `I` | `StreamReader[O]` |
| `Collect` | `StreamReader[I]` | 单个 `O` |
| `Transform` | `StreamReader[I]` | `StreamReader[O]` |

选择相应的构造器，提供一种调用方式的实现：`NewInvokeRunnable`、`NewStreamRunnable`、`NewCollectRunnable` 或 `NewTransformRunnable`。构造器会补齐其他调用方式，但需要注意下面的转换规则。

## 派生模式不自动拼接 {#derived-modes-are-not-automatic-concatenation}

- 以 `Invoke` 实现为基础时，`Stream` 只输出一个结果；`Collect` 读取输入流后，仅将最后一项传给函数。
- 以 `Stream` 实现为基础时，`Invoke` 读取全部输出，但只返回最后一项。
- 以 `Collect` 实现为基础时，单个输入或输出会被包装成只有一项的流。
- 以 `Transform` 实现为基础时，单个输入会被包装成流；需要返回单个结果时，只取输出流的最后一项。

如果每项都是新增的一小段文本，应使用 `CollectString` 拼接。如果每项都是完整快照，取最后一项可能正好符合需求。需要累计或合并全部输入时，应显式实现 `Collect`。

[离线示例](./graphs#offline-example)对比最后项 `"!"` 和拼接文本 `"hello!"`。

## 流生命周期 {#stream-lifecycle}

`Send` 返回 false 时，表示接收方不再需要数据，发送方应停止生成。发送完成后关闭流，发生错误时用 `SetError` 记录。接收方读完后应检查 `Err`；如果提前停止，则需要关闭流或取消调用。

```go
defer stream.Close()
for {
    item, ok := stream.Recv()
    if !ok {
        break
    }
    consume(item)
}
if err := stream.Err(); err != nil {
    return err
}
```

缓冲可以暂时缓解发送与接收速度不一致的问题，但容量有限。可能阻塞的操作应响应调用方 context 的取消信号。常见的数据处理可以使用 `Map`、`Pipe` 和 `Collect`。

## 适配 Step {#adapting-to-steps}

图和工作流的 `Step` 使用字符串作为输入和输出。将 Runnable 接入时，需要提供相应的转换函数。节点输入输出类型不同时可用 [TypedNode 的编码与解码](./graphs#typed-boundaries)；始终使用同一种类型时可用 `TypedGraph[T]`。