# 模板与辅助包 {#templates-utility-packages}

这些小型包提供提示词处理、文件操作协调和文本匹配等辅助功能，可以独立用于应用中。

## 提示词模板 {#prompt-templates}

`prompt.New` 创建使用双花括号占位符的模板，`WithDefaults` 设置默认变量，`WithStrict` 在缺少变量时报错。`RenderMessages` 将渲染结果包装为系统消息。不开启严格模式时，没有对应变量的占位符会保留在文本中。

```go
template := prompt.New("You review {{language}} code for {{project}}.").
    WithDefaults(map[string]string{"language": "Go"}).
    WithStrict()
text, err := template.Render(map[string]string{"project": "Covonaut"})
if err != nil {
    return err
}
```

模板只做文字替换，不会转义、过滤内容或检查权限。不要将不可信输入直接插入系统指令。`Format` 不会严格检查变量是否缺失。

## 文件变更队列 {#file-mutation-queue}

`filequeue.New` 创建进程内的文件操作队列，同一路径上的操作依次执行。`WithFile` 用于提交修改，`WithFileResult` 用于需要返回结果的操作。包中的安全读写辅助函数也使用这套队列。

队列会尽可能解析已有符号链接，并释放不再使用的锁条目。它不是分布式锁，也不能保证多个文件的修改同时成功，更无法约束其他未使用队列的进程。应用仍需检查权限和允许访问的根目录；需要协调操作的代码应共用同一个队列实例。

## 模糊文本匹配 {#fuzzy-text}

`fuzzy.Find` 先尝试精确匹配，再规范化文本，以容忍部分标点、空白和行尾差异。`Replace` 只替换第一个匹配结果。`LevenshteinDistance` 则用于单独计算两个字符串的编辑距离。

这些函数适合辅助文本编辑，不会理解代码语义。应用修改前应检查目标位置和 diff；找到相似文本不代表选中了正确的代码。

## 环境辅助函数 {#environment-helpers}

`pkg/util` 提供 `ErrorString`、`DefaultString`、`FirstNonEmpty` 和 `EnvOrDefault`，但不会验证凭据、模型名称或 URL 是否有效。启动服务前仍需检查必填配置。

API：[prompt](https://pkg.go.dev/github.com/covoyage/covonaut/prompt)、[filequeue](https://pkg.go.dev/github.com/covoyage/covonaut/filequeue)、[fuzzy](https://pkg.go.dev/github.com/covoyage/covonaut/fuzzy)、[util](https://pkg.go.dev/github.com/covoyage/covonaut/pkg/util)。