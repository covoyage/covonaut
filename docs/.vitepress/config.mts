import { defineConfig, type DefaultTheme } from 'vitepress'
import { languageScript } from './language.mjs'

const config = defineConfig({
  title: 'Covonaut',
  description: 'Build agents, tools, and multi-agent workflows in Go.',
  base: '/covonaut/',
  cleanUrls: true,
  lastUpdated: true,
  head: [['meta', { name: 'theme-color', content: '#18794e' }]],
  sitemap: { hostname: 'https://covoyage.github.io/covonaut/' },
  themeConfig: {
    nav: [
      { text: 'Guide', link: '/guide/quickstart' },
      { text: 'Integrations', link: '/integrations/' },
      { text: 'Examples', link: '/examples' },
      { text: 'API', link: 'https://pkg.go.dev/github.com/covoyage/covonaut' }
    ],
    sidebar: [
      { text: 'Start Here', items: [
        { text: 'Overview', link: '/' },
        { text: 'Design Principles', link: '/guide/design' },
        { text: 'Capability Map', link: '/capabilities' },
        { text: 'Quick Start', link: '/guide/quickstart' },
        { text: 'Configuration', link: '/guide/configuration' }
      ] },
      { text: 'Agent Runtime', items: [
        { text: 'Agent Execution', link: '/guide/execution' },
        { text: 'Prompts, Rules & Profiles', link: '/guide/prompts' },
        { text: 'Skills', link: '/guide/skills' },
        { text: 'Providers', link: '/guide/providers' },
        { text: 'Messages & Multimodal', link: '/guide/messages' },
        { text: 'Structured Output', link: '/guide/structured-output' }
      ] },
      { text: 'Tools & Extensibility', items: [
        { text: 'Tool Execution', link: '/guide/tools' },
        { text: 'Built-in Tool Catalog', link: '/guide/builtin-tools' },
        { text: 'Extensions & Lifecycle', link: '/guide/extensions' }
      ] },
      { text: 'Composition', items: [
        { text: 'Orchestration Overview', link: '/guide/orchestration' },
        { text: 'Runnables & Streams', link: '/guide/runnables' },
        { text: 'Graph Execution', link: '/guide/graphs' },
        { text: 'Workflows & Handoffs', link: '/guide/workflows' }
      ] },
      { text: 'State & Reliability', items: [
        { text: 'Context & Compaction', link: '/guide/context' },
        { text: 'Sessions & Branches', link: '/guide/sessions' },
        { text: 'Checkpoints & Human Review', link: '/guide/recovery' },
        { text: 'Streaming & Reliability', link: '/guide/runtime' },
        { text: 'Events, Traces & Metrics', link: '/guide/observability' }
      ] },
      { text: 'Connect', items: [
        { text: 'Integration Overview', link: '/integrations/' },
        { text: 'HTTP API', link: '/integrations/http' },
        { text: 'MCP', link: '/integrations/mcp' },
        { text: 'A2A', link: '/integrations/a2a' },
        { text: 'ACP', link: '/integrations/acp' },
        { text: 'AG-UI', link: '/integrations/agui' },
        { text: 'A2UI', link: '/integrations/a2ui' }
      ] },
      { text: 'Application Components', items: [
        { text: 'Terminal UI', link: '/guide/tui' },
        { text: 'Retrieval Interfaces', link: '/guide/rag' },
        { text: 'Templates & Utilities', link: '/guide/utilities' }
      ] },
      { text: 'Reference', items: [
        { text: 'Examples', link: '/examples' },
        { text: 'Package Index', link: '/reference' },
        { text: 'Contributing to Docs', link: '/contributing' }
      ] },
      { text: 'Production', items: [
        { text: 'Production Checklist', link: '/production' },
        { text: 'Testing', link: '/testing' },
        { text: 'Troubleshooting', link: '/troubleshooting' }
      ] }
    ],
    outline: [2, 3],
    search: { provider: 'local' },
    socialLinks: [{ icon: 'github', link: 'https://github.com/covoyage/covonaut' }],
    editLink: {
      pattern: 'https://github.com/covoyage/covonaut/edit/main/docs/:path'
    },
    footer: {
      message: 'Released under the MIT License.'
    }
  }
})

const labels: Record<string, string> = {
  'Start Here': '开始使用',
  'Overview': '概览',
  'Design Principles': '设计思想',
  'Capability Map': '功能概览',
  'Quick Start': '快速开始',
  'Configuration': '配置',
  'Agent Runtime': 'Agent 运行时',
  'Agent Execution': 'Agent 执行',
  'Prompts, Rules & Profiles': '提示词、规则与模型预设',
  'Skills': '技能',
  'Providers': '模型服务',
  'Messages & Multimodal': '消息与多模态',
  'Structured Output': '结构化输出',
  'Tools & Extensibility': '工具与扩展',
  'Tool Execution': '工具执行',
  'Built-in Tool Catalog': '内置工具目录',
  'Extensions & Lifecycle': '扩展与生命周期',
  'Composition': '组合与编排',
  'Orchestration Overview': '编排概览',
  'Runnables & Streams': 'Runnable 与流',
  'Graph Execution': '图执行',
  'Workflows & Handoffs': '工作流与交接',
  'State & Reliability': '状态与可靠性',
  'Context & Compaction': '上下文与压缩',
  'Sessions & Branches': '会话与分支',
  'Checkpoints & Human Review': '检查点与人工审核',
  'Streaming & Reliability': '流式输出与可靠性',
  'Events, Traces & Metrics': '事件、链路追踪与指标',
  'Connect': '协议集成',
  'Integration Overview': '集成概览',
  'HTTP API': 'HTTP API',
  'MCP': 'MCP',
  'A2A': 'A2A',
  'ACP': 'ACP',
  'AG-UI': 'AG-UI',
  'A2UI': 'A2UI',
  'Application Components': '应用组件',
  'Terminal UI': '终端界面',
  'Retrieval Interfaces': '检索接口',
  'Templates & Utilities': '模板与辅助包',
  'Reference': '参考',
  'Examples': '示例',
  'Package Index': '包索引',
  'Contributing to Docs': '维护文档',
  'Production': '生产运维',
  'Production Checklist': '生产检查清单',
  'Testing': '测试',
  'Troubleshooting': '问题排查'
}

function translateSidebar(items: DefaultTheme.SidebarItem[]): DefaultTheme.SidebarItem[] {
  return items.map(item => ({
    ...item,
    text: item.text ? labels[item.text] : undefined,
    link: item.link ? '/zh' + item.link : undefined,
    items: item.items ? translateSidebar(item.items) : undefined
  }))
}

const sidebar = config.themeConfig!.sidebar as DefaultTheme.SidebarItem[]
const chineseSidebar = translateSidebar(sidebar)

export default defineConfig({
  ...config,
  lang: 'en',
  head: [...(config.head ?? []), ['script', {}, languageScript('/covonaut/')]],
  locales: {
    root: { label: 'English', lang: 'en' },
    zh: {
      label: '简体中文',
      lang: 'zh-CN',
      description: '用 Go 构建 Agent、工具和多 Agent 工作流。',
      themeConfig: {
        nav: [
          { text: '指南', link: '/zh/guide/quickstart' },
          { text: '协议集成', link: '/zh/integrations/' },
          { text: '示例', link: '/zh/examples' },
          { text: 'API', link: 'https://pkg.go.dev/github.com/covoyage/covonaut' }
        ],
        sidebar: { '/zh/': chineseSidebar },
        outline: { level: [2, 3], label: '本页目录' },
        docFooter: { prev: '上一页', next: '下一页' },
        lastUpdated: { text: '最后更新' },
        editLink: {
          pattern: 'https://github.com/covoyage/covonaut/edit/main/docs/:path',
          text: '编辑此页'
        },
        footer: { message: '基于 MIT 许可证发布。' },
        sidebarMenuLabel: '目录',
        returnToTopLabel: '返回顶部',
        skipToContentLabel: '跳至正文',
        darkModeSwitchLabel: '主题',
        lightModeSwitchTitle: '切换到浅色主题',
        darkModeSwitchTitle: '切换到深色主题',
        notFound: {
          title: '页面未找到', quote: '请检查地址，或返回首页浏览文档。',
          linkLabel: '返回首页', linkText: '返回首页'
        }
      }
    }
  },
  themeConfig: {
    ...config.themeConfig,
    sidebar: { '/zh/': chineseSidebar, '/': sidebar },
    langMenuLabel: '语言 / Language',
    search: {
      provider: 'local',
      options: {
        locales: {
          zh: {
            translations: {
              button: { buttonText: '搜索文档', buttonAriaLabel: '搜索文档' },
              modal: {
                noResultsText: '没有找到相关内容',
                resetButtonTitle: '清除搜索',
                displayDetails: '显示详情',
                footer: { selectText: '选择', navigateText: '切换', closeText: '关闭' }
              }
            },
            miniSearch: {
              options: {
                tokenize: (text: string) => {
                  const segmenter = new Intl.Segmenter('zh', { granularity: 'word' })
                  return Array.from(segmenter.segment(text))
                    .filter(segment => segment.isWordLike).map(segment => segment.segment)
                }
              }
            }
          }
        }
      }
    }
  }
})