# 终端界面 {#terminal-ui}

## 分层架构 {#layered-architecture}

| 层 | 职责 |
| --- | --- |
| `tui/core` | 组件、消息、命令、布局基础 |
| `tui/terminal` | I/O、按键解码、能力、raw mode |
| `tui/theme` | 样式和语义配色 |
| `tui/component` | 编辑器、Markdown、输入、表格、菜单、图片 |
| `tui/chat` | 历史、输入区、聊天事件、应用宿主 |
| `tui/agentadapter` | Agent 到聊天事件转换 |
| `tui/stdio` | 过程式渲染、进度、spinner、行输入 |

聊天层按统一的事件接口更新界面，不依赖某一种 Agent 运行时，因此可以接收模拟数据、远程服务或本地 Agent 的事件。

## 无模型聊天应用 {#a-chat-app-without-a-model}

下面的完整程序会回显用户输入，无需模型密钥，但需要在交互式终端中运行。输入 `/quit` 可以退出。

<!--@include: ../../guide/tui.md{24,58}-->

## 连接 Agent {#connect-an-agent}

`agentadapter.BindAgent(subscriber, agent)` 将生命周期、文本、思考、重置、工具、交接、压缩和重试事件转换为聊天界面事件。每个实例只应绑定一次；重复绑定会增加处理函数，而当前适配器没有提供单次绑定的退订句柄。

应用负责实现 `OnSubmit`、取消操作、模型配置、持久化和 `Agent.Close`。模型调用应使用 ChatApp 的生命周期 context，并在界面事件循环之外执行，避免阻塞界面。停止按钮应真正取消运行，而不只是隐藏输出。

## 组件与渲染 {#components-and-rendering}

`Component` 按终端可见列宽生成显示行，并通过 Invalidate 请求重新渲染。`Updatable` 接收消息并返回异步 Cmd，`Focusable` 管理焦点和终端光标。

中文、emoji 和带 ANSI 控制码的文本不能按字节数计算显示宽度。窗口大小或主题变化后，需要使布局缓存失效；光标标记可以帮助输入法定位候选窗口。界面引擎按差异更新屏幕，stdio 则适合按步骤输出。两者同时使用时，需要明确谁负责控制终端，避免互相覆盖。

## 历史与终端能力 {#transcript-and-capabilities}

默认模式只渲染应用视口内可见的历史。Scrollback 模式将已经完成的内容写入终端原生滚动历史，同时保留尚未完成内容的实时显示。可以根据历史长度和交互需求选择。

Kitty 键盘协议、鼠标、粘贴、图片显示和能力探测的支持情况，取决于终端及 tmux 等终端复用器。应用应为不支持的功能提供替代方式。未配置 `ReasoningRenderer` 时，默认不显示思考内容。

可先运行[终端示例](../examples#terminal-ui)检查能力，再集成完整 Agent。