# Templates & Utility Packages

These packages support applications around the agent loop. They are intentionally
small and do not define a separate application framework.

## Prompt Templates

`prompt.New` renders double-brace placeholders. `WithDefaults` supplies fallback
values and `WithStrict` rejects missing variables. `RenderMessages` wraps rendered
content as a system message. Non-strict rendering leaves missing placeholders
unchanged.

```go
template := prompt.New("You review {{language}} code for {{project}}.").
    WithDefaults(map[string]string{"language": "Go"}).
    WithStrict()
text, err := template.Render(map[string]string{"project": "Covonaut"})
if err != nil {
    return err
}
```

Template rendering is textual substitution, not escaping, input sanitization,
or authorization. Keep untrusted content out of privileged instruction slots
where possible. The simple `Format` helper does not provide strict missing-value
validation.

## File Mutation Queue

`filequeue.New` creates a process-local queue that serializes operations on a
resolved file path. `WithFile` wraps a mutation; `WithFileResult` returns a value.
`ReadFileSafe` and `WriteFileSafe` use the same queue.

Path resolution accounts for existing symlinks where possible, and unused lock
entries are released. This is not a distributed lock, an atomic multi-file
transaction, or protection against a process that does not use the queue.
Resolve permissions and allowed roots separately, and use one shared queue
where participating operations must serialize.

## Fuzzy Text

`fuzzy.Find` attempts exact matching before normalized matching. Normalization
handles selected punctuation, space, and line-ending differences. `Replace`
replaces the first matching occurrence; `LevenshteinDistance` is a separate
distance helper.

Use these helpers for controlled edit workflows, not arbitrary semantic code
transformation. Check which occurrence is being changed and inspect a diff;
similar-looking text is not proof that a patch targets the intended code.

## Environment Helpers

`pkg/util` includes `ErrorString`, `DefaultString`, `FirstNonEmpty`, and
`EnvOrDefault`. These helpers simplify application configuration but do not
validate credentials, model names, or URLs. Validate required values before
starting services.

See [prompt](https://pkg.go.dev/github.com/covoyage/covonaut/prompt),
[filequeue](https://pkg.go.dev/github.com/covoyage/covonaut/filequeue),
[fuzzy](https://pkg.go.dev/github.com/covoyage/covonaut/fuzzy), and
[util](https://pkg.go.dev/github.com/covoyage/covonaut/pkg/util).