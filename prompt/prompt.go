package prompt

import (
	"fmt"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

// Template renders a template string by replacing {{variable}} placeholders.
type Template struct {
	Raw      string
	Defaults map[string]string
	Strict   bool
}

func New(tmpl string) *Template {
	return &Template{Raw: tmpl}
}

func (t *Template) WithDefaults(defaults map[string]string) *Template {
	cp := *t
	cp.Defaults = defaults
	return &cp
}

func (t *Template) WithStrict() *Template {
	cp := *t
	cp.Strict = true
	return &cp
}

func (t *Template) Render(vars map[string]string) (string, error) {
	result := t.Raw
	merged := make(map[string]string)
	for k, v := range t.Defaults {
		merged[k] = v
	}
	for k, v := range vars {
		merged[k] = v
	}

	placeholders := extractPlaceholders(t.Raw)
	for _, name := range placeholders {
		val, ok := merged[name]
		if !ok {
			if t.Strict {
				return "", fmt.Errorf("prompt template: undefined variable %q", name)
			}
			continue
		}
		result = strings.ReplaceAll(result, "{{"+name+"}}", val)
	}
	return result, nil
}

// RenderMessages renders the template and wraps it as a system message.
func (t *Template) RenderMessages(vars map[string]string) ([]agentcore.Message, error) {
	content, err := t.Render(vars)
	if err != nil {
		return nil, err
	}
	return []agentcore.Message{{Role: agentcore.RoleSystem, Content: content}}, nil
}

// Format is a convenience shorthand for one-off rendering.
func Format(tmpl string, vars map[string]string) string {
	result := tmpl
	for k, v := range vars {
		result = strings.ReplaceAll(result, "{{"+k+"}}", v)
	}
	return result
}

func extractPlaceholders(tmpl string) []string {
	var names []string
	seen := make(map[string]bool)
	rest := tmpl
	for {
		start := strings.Index(rest, "{{")
		if start < 0 {
			break
		}
		end := strings.Index(rest[start:], "}}")
		if end < 0 {
			break
		}
		name := strings.TrimSpace(rest[start+2 : start+end])
		if name != "" && !seen[name] {
			names = append(names, name)
			seen[name] = true
		}
		rest = rest[start+end+2:]
	}
	return names
}
