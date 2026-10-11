# 维护文档 {#contributing-to-docs}

## 本地开发 {#local-development}

使用 Node.js 22 LTS 和 Go 1.25 或更高版本，从仓库根目录运行：

```sh
npm --prefix docs ci
npm --prefix docs run dev
npm --prefix docs run check
npm --prefix docs run preview
```

文档地址位于 `/covonaut/` 下。`check` 会测试语言选择逻辑、构建站点、检查内部链接和锚点，并验证双语页面对应关系及完整 Go 程序。四个 A2UI 短片段也会补齐上下文后编译、离线运行，并检查界面不存在时的错误处理；两个工作流片段会验证子 Agent 是否关闭。它不会调用付费模型或检查外部网站，其余短片段不会单独编译。

## 文档与源码位置 {#content-ownership}

- 英文文档位于 `docs`，中文对应页面位于 `docs/zh`，两种语言使用相同的相对路径。
- 完整可运行程序位于 `example`，两种语言引用同一份源码。
- 英文 A2A 和 A2UI 页面引用各包的 README；中文页面提供中文说明，代码片段仍引用同一份 README。
- 移动被引用的 Markdown 代码块后，需要更新引用的行范围。检查会编译完整程序，并比较双语页面的代码。
- 导出函数的签名和类型定义由 Go 文档及 pkg.go.dev 维护。

增加页面时，需要更新侧栏并补齐另一种语言。中文标题保留对应英文标题的显式锚点，这样切换语言后仍能定位到同一段落。介绍模型能力时，应区分服务原生支持与通过提示词实现的替代方案。

## 语言版本 {#languages}

英文使用原有路径，中文使用对应的 `/zh/` 路径。站点统一处理自动语言选择、手动选择的记忆，以及切换后保留当前页面和段落。

首次访问时，浏览器主语言以 `zh` 开头则显示中文，其他语言显示英文。通过语言菜单手动选择后，站点会在本机记住偏好；清除站点 localStorage 可以恢复自动选择。浏览器不允许存储时，站点通过 `lang` 查询参数保留选择。

## GitHub Pages 部署 {#github-pages-deployment}

在 Settings > Pages 中将 Source 设为 GitHub Actions。工作流会检查 PR，并部署 main 分支上通过检查的构建，也支持手动触发。

部署地址为 `https://covoyage.github.io/covonaut/`。更改域名或仓库名后，需要同步更新 VitePress base、sitemap、语言路由和检查脚本中的 base。当前没有配置自定义域名或多版本文档。

部署需要相应的仓库权限，并确保仓库可以使用 Pages。如果 `github-pages` 环境配置了保护规则，还可能需要人工批准。