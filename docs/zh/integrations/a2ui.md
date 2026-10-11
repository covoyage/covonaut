# Covonaut 的 A2UI 协议支持 {#a2ui-protocol-support-for-covonaut}

`a2ui` 实现 [A2UI](https://a2ui.org) **0.9.1**。Agent 描述界面结构，客户端将其渲染为自己的控件。界面结构与数据模型分开传输，可以逐步更新，无需执行 Agent 提供的任意代码。

## 功能 {#features}

- 支持服务端的 createSurface、updateComponents、updateDataModel 和 deleteSurface 消息。
- 支持客户端 action、error 消息及能力交换。
- `Dynamic` 支持字面量、JSON Pointer 数据绑定和函数；`ChildList` 支持固定子组件列表和按模板生成的列表。
- 提供标准组件目录（catalog），也可以注册自定义组件。
- 按 RFC 6901 读取、设置和删除数据，支持插入或更新值、操作数组及通过 `-` 追加元素。
- `SurfaceStore` 保存界面状态、组件关系和客户端数据模型。
- 检查组件结构、根节点、不存在的引用和循环，并返回 `VALIDATION_FAILED` 错误。
- 提供 JSONL 编码与解码、Builder、组件构造器，以及 A2A 和 AG-UI 传输适配。

除 Covonaut 模块外没有额外外部依赖。

## 架构 {#architecture}

`dynamic` 和 `component` 定义数据绑定与组件，`message` 和 `client` 定义双向消息。`catalog` 管理组件类型，`datamodel` 按 JSON Pointer 操作数据，`surface` 保存界面状态，`validate` 检查结构。`stream` 处理 JSONL，`builder` 帮助构建界面，两个 binding 文件负责传输适配。

## 快速开始 {#quick-start}

### 生成界面：服务端／Agent {#generate-a-ui-server-agent-side}

程序创建名为 profile 的界面，将姓名控件绑定到数据模型，并以 JSON Lines 格式输出消息。

<!--@include: ../../../a2ui/README.md{59,91}-->

### 读取界面：客户端／渲染器 {#consume-a-ui-client-renderer-side}

Decoder 逐条解析消息，SurfaceStore 根据消息更新状态。渲染器需要从 `root` 开始，沿组件之间的引用遍历，并解析数据绑定。

<!--@include: ../../../a2ui/README.md{95,125}-->

### 呈现前验证 {#validate-before-rendering}

先按组件目录验证每条消息，再检查整棵组件树。消息的外层结构在协议中称为“信封”。验证错误可以按标准格式反馈给模型，请它修正。

<!--@include: ../../../a2ui/README.md{129,147}-->

## 传输绑定 {#transport-bindings}

A2UI 不要求使用特定传输协议，当前提供以下适配：

### A2A {#a2a}

每条 A2UI 消息封装为一个数据 Part，并标记为 `application/a2ui+json`。

<!--@include: ../../../a2ui/README.md{159,169}-->

### AG-UI {#ag-ui}

每条 A2UI 消息通过一个 CUSTOM 事件发送。

<!--@include: ../../../a2ui/README.md{175,185}-->

## 协议注意事项 {#protocol-notes}

- 组件以扁平列表传输，通过 ID 引用形成树，必须恰好有一个 `root`。组件定义可以乱序到达；根节点尚未出现时，客户端应先缓存消息。
- 每个界面的数据通过 JSON Pointer 定位。更新会按 path 插入或修改值，省略 value 表示删除。删除数组元素时，会将其设为 null 并保留数组长度；路径 `/` 用于替换整个模型。
- 开启 `sendDataModel` 后，客户端通过消息元数据中的 `a2uiClientDataModel` 附带数据模型，具体见 `ClientDataModel`。
- 当前支持 0.9.1，尚未实现 1.0 候选版本中的 actionResponse 和 action ID。

## 参考 {#references}

- [0.9.1 规范](https://a2ui.org/specification/v0.9.1-a2ui/)
- [消息参考](https://a2ui.org/reference/messages/)
- [传输](https://a2ui.org/concepts/transports/)
- [Go API](https://pkg.go.dev/github.com/covoyage/covonaut/a2ui)