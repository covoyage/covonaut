# ARD {#ard}

Covonaut 可选接入 [Agentic Resource Discovery v0.91](https://agenticresourcediscovery.org/spec)。
`ard` 包用于发现资源描述；执行仍使用 MCP 或 A2A。它不提供 Registry 服务端。

## 支持范围 {#scope}

| 接口 | 支持内容 |
| --- | --- |
| 条目与清单 | 开放的媒体类型、语义校验、未知字段保留 |
| JSON-LD 条目展开 | 基础／远程／导入／保护／作用域上下文、图与反向属性、语言及索引／ID／类型容器；使用标准库实现 |
| 发布 | 不可变 GET/HEAD 清单 Handler、ETag、条件请求 |
| 网页发现 | `/.well-known/ard.json`、HTML `rel="ard"` 链接与 base、内联别名／图／included 节点 |
| Registry 搜索 | 请求与响应、过滤条件、联邦模式、不透明分页游标 |
| 转介 | 显式有界遍历、循环检测、按 Registry 保留响应 |
| Agent 集成 | 只读 `discover_resources` 工具，Registry 由应用配置 |
| 资源使用 | 应用授权后的 A2A 1.0/MCP HTTP 连接；Skill 只读取描述文本 |

不包含 Registry 索引或数据摄取、Explore/List、robots/DNS 发现、旧发现地址、信任签名验证、
stdio 启动、Skill 安装或统一搜索排名。v0.91 仍是提案；这里不宣称获得兼容性认证，也不保证兼容所有 Registry 实现。

ARD 实现不新增外部依赖。内置处理器支持 JSON-LD 条目展开：有序上下文、前缀与别名、类型／JSON 强制转换、
导入与保护上下文、属性和类型作用域及传播规则、图、反向与嵌套属性、语言及方向、索引／ID／类型映射，以及 set/list 容器。
远程上下文仍受加载预算和网络策略约束；重置上下文时保留 ARD 要求的初始上下文。
原始条目保留未知字段，opaque JSON 中的大整数不会被舍入。

`ExpandDocument` 接受数组和图文档，`Expand`、`Core` 用于单个条目。
这是条目展开 API，不提供 RDF 规范化、framing 或签名工具。测试覆盖相应协议行为，但不宣称获得独立的 JSON-LD 或 ARD 一致性认证。

## 一致性验证 {#conformance}

固定版本的官方 W3C 展开清单已离线运行：368 个适用用例全部通过，其中 273 个为正例、95 个为必须拒绝的反例；
18 个被官方标记为仅适用 JSON-LD 1.0 的用例明确跳过。全部 95 个反例均精确比对标准 JSON-LD 错误码，
不会把不相关的错误当作通过。应用可通过 `errors.As` 获取 `JSONLDError.Code`，并通过 `Unwrap` 保留底层错误链；
本地回归还覆盖作用域错误嵌套、保护的 `@type` 定义和取消语义。

官方 ARD 验证也已实际执行：基础上下文比对、对发布 Handler 的 URL 和内联条目执行严格 Schema CLI 校验、
21 个独立 Draft 2020-12 结构正反例，以及针对未改写 CDDL 的 7 个用例。
这些检查通过环境变量显式启用；变量缺失时显示跳过，验证器未运行或跳过 Schema 校验时测试失败。
测试工具和官方数据位于仓库外，不新增运行时依赖。

[一致性证据与规范对照表](https://github.com/covoyage/covonaut/blob/main/ard/CONFORMANCE.md)
包含固定版本、完整复跑命令、应用与 Registry 的职责边界，以及已验证的上游差异。
其中 CDDL 会接受同时携带 `url` 和 `data` 的条目，Schema 把 `trustManifest` 写成了大写开头；
仅通过某一份结构文件不能视为完整 ARD 认证。本实现仍拒绝双重交付，并要求信任封套携带 identity。

## 发布清单 {#publishing}

```go
handler, err := ard.NewManifestHandler(ard.Manifest{Entries: []ard.Entry{entry}})
if err != nil {
    return err
}
mux.Handle(ard.ManifestPath, handler)
```

Handler 会保存清单快照，在不访问网络的前提下校验 JSON-LD 语义，因此发布时应使用基础上下文或内联上下文。
需要远程上下文或经过应用证明的身份映射时，可用 `NewManifestHandlerWithContext` 传入配置好的处理器。
响应设置 `Cache-Control: public, max-age=300`，只适合公开描述。HTTP 服务、认证，以及需要其他缓存策略的发布端点由应用提供。

`ValidateEntry` 校验普通规范字段；代表性查询建议提供 2-5 条，不满足时返回警告。
带自定义上下文的条目应使用 `ContextProcessor.Validate`，用 `Core` 获取展开后的规范字段，用 `Expand` 查看 IRI 展开值。
原始条目保留未知字段和 opaque JSON，后者内部的字段不会继续展开。

`@id` 与 `identifier` 相同时不需要外部证明；冲突的 `urn:air:` 标识及非绝对 ID 会被拒绝。
若使用另一个绝对资源 IRI，消费者要求提供 `ContextProcessor.VerifyIdentity` 或 `ClientConfig.VerifyIdentity`，
由应用证明两个名字指向同一资源；缺少证明或证明失败时拒绝条目。
单独使用 `ValidateEntry` 只做结构校验，并对未证明的 IRI 映射发出警告。

## 发现与搜索 {#discovery-and-search}

```go
client := ard.NewClient(ard.ClientConfig{})
defer client.Close()
manifest, err := client.Discover(ctx, "https://publisher.example.com")
if err != nil {
    return err
}
response, err := client.Search(ctx, "https://registry.example.com/api", ard.SearchRequest{
    Query: ard.Query{Text: "an agent that summarizes financial reports"},
    Federation: ard.FederationNone,
    PageSize: 10,
})
if err != nil {
    return err
}
```

`Discover` 使用站点根路径，即使已有 well-known 清单，仍会读取网页中的发现链接。
内联条目按展开后的 ARD 术语识别，而非字面字段名，也支持 `@graph` 与 `@included` 中的节点。
无关标记会被跳过，opaque JSON 不会被遍历为资源节点。
`FetchManifest` 读取指定清单地址。远程上下文沿用相同的网络策略、响应大小限制和加载预算。

`Search` 在 Registry 基础地址后追加 `/search`；`SearchEndpoint` 直接使用转介中的完整地址，不再追加路径。
查询上下文和过滤条件中的单值或数组会保留，由 Registry 按 IRI 匹配。
`null` 和混合类型数组会在发出网络请求前被拒绝。
响应的 `PageToken` 只能传回同一个 Registry。稀疏搜索结果不是完整条目，不能直接用于资源接入。

`Search` 本身不会跟随转介；省略联邦模式时，由 Registry 按协议执行默认的 `auto` 行为。
`SearchFederated` 显式请求 `referrals`，默认最多遍历两跳、八个 Registry，分别保留各站点评分和游标。
转介站点失败会记录在 `RegistryResults.Error`；首个 Registry 失败直接返回错误。
客户端限制响应大小、条目数、重定向次数和整个操作的耗时。

## Agent 扩展 {#agent-extension}

```go
extension, err := ard.NewDiscoveryExtension(ard.ExtensionConfig{
    Client: client,
    Registries: map[string]string{"public": "https://registry.example.com"},
})
if err != nil {
    return err
}
cfg.Extensions = append(cfg.Extensions, extension)
```

工具接受查询、已配置的 Registry 名称，以及可选媒体类型、页大小和游标，使用 `federation: none`。
它不能接受任意 Registry URL、连接搜索结果、注册执行工具或安装 Skill。
客户端由调用方管理，在所有 Agent 和连接结束后关闭；Extension 不会关闭共享客户端。

## 授权与安全 {#authorization}

默认只允许 HTTPS 和公网地址。客户端禁用代理，在实际拨号时检查 DNS 地址并连接已检查的 IP，逐次校验重定向。
凭据通过 `ClientConfig.HeadersByOrigin` 配置，键为 `scheme://host[:port]`，不会把原站凭据带到另一个站点。
`AllowedHosts` 是精确主机名白名单，不代表信任；开发或内网部署须显式开启 `AllowHTTP`、`AllowPrivate`。

`ResolveResource` 读取 Card 前要求应用提供 `AuthorizeFunc`。返回的 `TrustStatus` 始终为 `unverified`；
评分只表示相关性，不表示可信程度。结构化身份校验不能替代签名验证、DID 解析或域名所有权验证，授权前应由应用完成这些判断。

默认解析器读取 A2A 1.0 `supportedInterfaces`，支持 `JSONRPC` 和 `HTTP+JSON`。
ARD 条目的 URL 指向 Card，未必是调用地址。其他 Card 格式须提供 `ResolveConfig.Resolver`；
MCP 解析器应返回 Card 文档规定的 HTTP 地址，并设置 `Binding: "MCP_HTTP"`。
接口缺失或不兼容时返回错误，不猜测端点，也不启动 stdio 命令。

`ConnectA2A`、`ConnectMCP` 需要另行批准 `connect`，仍使用受控传输和响应大小限制。
MCP 连接会初始化会话，但不会自动向 Agent 导入工具。凭据应配置在已批准的源站。
Card、Skill 下载和执行发生重定向时，新地址也须经过应用批准。
`DescribeSkill` 经 `describe` 授权后，只返回 URL 指向的 Skill 文本，并限制读取大小；
不能把文本当作可信指令，也不应自动安装或执行。

## 离线示例 {#offline-example}

```sh
go run ./example/ard
go test ./ard -race
```

示例使用临时本机 HTTP 测试服务和固定搜索响应，不是生产 Registry，无需模型密钥，也不会执行远程资源。

::: details 完整离线示例
<<< @/../example/ard/main.go
:::