# Skills

Skills are task-specific instruction packages. They supplement prompts; they
do not implement or authorize tools by themselves.

## Load and Select

`skill.Load` accepts directories or `SKILL.md` paths and returns skills,
non-fatal diagnostics, and a fatal error. Missing paths produce no skills.
Duplicate names keep the first definition and produce a diagnostic. Hidden
directories are not traversed.

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

Handle diagnostics rather than assuming every package loaded. Supplying
available skills registers the skill extension during agent construction.
Loading paths and registering the resulting list are separate concerns.

## Progressive Disclosure

The extension adds an index of names, descriptions, and paths to the prompt.
Selected skills contribute full instructions at request time. A user command
such as `/skill:code-review check this patch` expands that skill with the task.

The runtime also recognizes a model's skill command response and can schedule
loaded instructions as a follow-up. Repeated selection of an already loaded
skill prompts continuation instead of repeatedly adding its whole body.

`DisableModelInvocation` removes a skill from model-facing discovery and
automatic selection. Explicit host/user invocation is a separate path.

## Metadata and Trust

Metadata includes name, description, license, compatibility, allowed tools,
and other values. Relative resources resolve against the skill's base directory.
`AllowedTools` metadata is not an execution sandbox: enforce permissions in
the registry, executor, and host.

Files may be read again when instructions are expanded. Deploy trusted skill
content and review scripts or referenced files before allowing execution.

## HTTP Management

The server exposes listing, diagnostics, status, events, and reload endpoints.
Configure `SkillAPIAuthToken` and disable registry/reload APIs when unnecessary.
Reload is an administrative operation, not an endpoint to leave publicly mutable.

See [skill](https://pkg.go.dev/github.com/covoyage/covonaut/skill) and the
[HTTP guide](./sessions).