# Prompts, Rules & Profiles

## System Prompt

For reusable variable substitution, see [prompt templates](./utilities#prompt-templates).

The system prompt defines the agent's task and behavior. Keep tool permissions
and execution policies in host code rather than relying on prompt wording.
Use `WithSystemPrompt` or `Config.SystemPrompt` for the base instruction.

## Project Rules

`NewRulesExtension` loads project rule files into the system prompt. It defaults
to `AGENTS.md`; `WithRulesPaths` selects other paths and `WithRulesReader` supplies
a custom reader for remote or sandboxed sources. Missing rule files are skipped.

```go
rules := agentcore.NewRulesExtension(
    agentcore.WithRulesPaths("AGENTS.md", ".cursorrules"),
)
cfg := agentcore.NewConfig(
    agentcore.WithModel(model),
    agentcore.WithProvider(provider),
    agentcore.WithExtensions(rules),
)
```

Treat rule contents as input from the chosen source. Loading a file does not
authorize its requested commands or make an untrusted repository safe.

## Model Profiles

`RegisterProfile` associates a model name with defaults such as a system-prompt
suffix, excluded tools, temperature, and maximum turns. User configuration takes
precedence; the suffix is additive.

Profiles apply during `New`. A later `ApplyCallConfig` model switch does not
reapply profiles, so a host supporting dynamic models must account for that
behavior rather than expecting all model defaults to change automatically.

## Prompt Caching

`NewPromptCachingExtension` annotates stable message prefixes with cache-control
breakpoints for compatible providers. Options control breakpoint count, system
prompt caching, and recent-message handling. A zero-value caching extension is
a no-op.

Caching annotations are provider-specific hints, not a cross-provider guarantee
of a cache hit, latency reduction, or cost savings. Measure reported usage for
your provider and workload.

See [RulesExtension](https://pkg.go.dev/github.com/covoyage/covonaut/agentcore#RulesExtension),
[ModelProfile](https://pkg.go.dev/github.com/covoyage/covonaut/agentcore#ModelProfile),
and [PromptCachingExtension](https://pkg.go.dev/github.com/covoyage/covonaut/agentcore#PromptCachingExtension).