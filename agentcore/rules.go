package agentcore

import (
	"context"
	"os"
	"strings"
)

// FileReader is the minimal file-reading interface used by RulesExtension.
// Inject a custom implementation to load rules from a sandbox, embedded
// data, or test fixtures instead of the local filesystem.
type FileReader interface {
	ReadFile(name string) ([]byte, error)
}

type osFileReader struct{}

func (osFileReader) ReadFile(name string) ([]byte, error) { return os.ReadFile(name) }

// RulesExtension loads project rule files (such as AGENTS.md, .cursorrules,
// or any caller-specified file) and appends their content to the agent's
// system prompt. This gives the agent persistent, project-specific guidance
// without hard-coding it into Config.SystemPrompt.
//
// The extension is intentionally generic: it does not bind to any single
// convention filename. Pass whichever files make sense for your project.
// Missing files are silently skipped, so a single extension can try
// several candidate paths.
//
// Files are read once during Init. Callers needing hot-reload should
// dispose and re-register the extension.
type RulesExtension struct {
	paths  []string
	reader FileReader
	loaded string
}

// RulesOption configures a RulesExtension.
type RulesOption func(*RulesExtension)

// WithRulesPaths sets the candidate file paths to read, in order. Earlier
// paths take precedence in the output ordering. Missing files are skipped.
func WithRulesPaths(paths ...string) RulesOption {
	return func(e *RulesExtension) { e.paths = paths }
}

// WithRulesReader replaces the default OS file reader with a custom one.
func WithRulesReader(r FileReader) RulesOption {
	return func(e *RulesExtension) { e.reader = r }
}

// NewRulesExtension creates an extension that loads rule files. With no
// options it tries "AGENTS.md" in the working directory.
func NewRulesExtension(opts ...RulesOption) *RulesExtension {
	e := &RulesExtension{
		paths:  []string{"AGENTS.md"},
		reader: osFileReader{},
	}
	for _, o := range opts {
		o(e)
	}
	return e
}

// Name implements Extension.
func (e *RulesExtension) Name() string { return "rules" }

// Init implements Extension. It reads all configured paths, skipping any
// that are missing or empty, and concatenates the surviving contents.
func (e *RulesExtension) Init(_ context.Context, _ *Agent) error {
	var parts []string
	for _, p := range e.paths {
		if p == "" {
			continue
		}
		data, err := e.reader.ReadFile(p)
		if err != nil {
			continue
		}
		s := strings.TrimSpace(string(data))
		if s != "" {
			parts = append(parts, s)
		}
	}
	e.loaded = strings.Join(parts, "\n\n")
	return nil
}

// Dispose implements Extension.
func (e *RulesExtension) Dispose() error { return nil }

// SystemPromptSuffix implements SystemPromptProvider. Returns the
// concatenated rule-file content (empty if no files were found).
func (e *RulesExtension) SystemPromptSuffix() string { return e.loaded }

// Loaded returns the content gathered during Init. Useful for diagnostics.
func (e *RulesExtension) Loaded() string { return e.loaded }
