# 快速开始 {#quick-start}

本节运行一个简单的 Agent：模型调用 `greet` 工具，再根据工具结果回答用户。

## 前提条件 {#prerequisites}

- Go 1.25 或更高版本。
- Chat Completions 兼容服务的 API 密钥。
- 账户可访问且支持工具调用的模型。

## 运行示例 {#run-the-example}

```sh
git clone https://github.com/covoyage/covonaut.git
cd covonaut
export OPENAI_API_KEY='your-api-key'
export AGENT_MODEL='your-tool-capable-model'
go run ./example/quickstart
```

其他兼容服务还需设置 `OPENAI_BASE_URL`，例如 `https://api.deepseek.com/v1`；使用 OpenAI 时可以不设置。请求使用你的服务账户，可能产生费用。

::: warning 凭据安全
将密钥保存在本地环境或密钥管理服务中，不要提交到仓库或输入公开文档页面。
:::

## 完整源码 {#complete-source}

以下是示例的完整源码。文档检查会编译这段程序，但不会调用外部模型服务。

<<< @/../example/quickstart/main.go

## 执行过程 {#what-happens}

`NewConfig` 根据传入的选项创建配置。`WithTools` 注册工具的参数定义和执行函数，`WithValidateArguments(true)` 开启参数验证。Agent 将提示词和工具定义发送给模型；模型请求调用 `greet` 时，Agent 执行工具，再将结果发送给模型生成回答。

示例通过 context 将运行时间限制为两分钟，并设置五轮上限。运行结束后，`Close` 释放 Agent 资源。模型每次生成的回答可能不同，测试时不应要求回答与某个字符串完全一致。

## 在自己的项目中使用 {#use-your-own-project}

在已有 Go 模块中安装依赖，并以上面的源码为起点：

```sh
go get github.com/covoyage/covonaut/agentcore
```

接下来阅读[配置](./configuration)、[工具与扩展](./tools)和[模型服务兼容性](./providers)。