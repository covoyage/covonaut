# 技能 {#skills}

技能是一组针对特定任务的指令，可以补充系统提示词。它本身不提供工具实现，也不授予执行权限。

## 加载与选择 {#load-and-select}

`skill.Load` 从目录或 `SKILL.md` 文件加载技能，返回技能列表、诊断信息和错误。路径不存在时返回空列表；名称重复时保留第一个定义，并给出诊断信息。加载器不会遍历隐藏目录。

```go
available, diagnostics, err := skill.Load("./skills")
if err != nil {
    return err
}
cfg.SkillConfig = agentcore.SkillConfig{
    AvailableSkills: available,
    SelectedSkills: []string{"code-review"},
    SkillPaths: []string{"./skills"},
    SkillDiagnostics: diagnostics,
}
```

除了检查错误，还应查看诊断信息，确认哪些技能实际加载成功。将列表填入 `AvailableSkills` 后，Agent 会在创建时注册技能扩展。读取技能文件和将技能接入 Agent 是两个步骤。

## 按需加载指令 {#progressive-disclosure}

扩展先在提示词中列出技能的名称、描述和路径，只在技能被选中时加入完整指令。例如，用户输入 `/skill:code-review check this patch`，就会加载代码审查技能，并将后面的文字作为任务。

运行时也能识别模型返回的技能命令，并在后续输入中加入相应指令。如果技能已经加载，会提示模型继续执行，避免反复追加同一份正文。

`DisableModelInvocation` 让模型无法发现或自动选择该技能，但不会禁止应用或用户主动调用。

## 元数据与信任 {#metadata-and-trust}

元数据包括名称、描述、许可证、兼容性和允许使用的工具。相对资源路径以技能目录为基准。`AllowedTools` 只是元数据，不能代替沙箱或工具执行时的权限检查。

加载完整指令时可能再次读取文件，因此应使用经过审查的技能内容，并检查其中引用的脚本和资源。

## HTTP 管理 {#http-management}

服务提供技能列表、诊断、状态、事件和重新加载接口。应配置 `SkillAPIAuthToken`，并禁用不需要的技能管理接口。重新加载会改变可用技能，必须作为受保护的管理操作处理。

见 [skill API](https://pkg.go.dev/github.com/covoyage/covonaut/skill) 与 [HTTP 指南](../integrations/http)。