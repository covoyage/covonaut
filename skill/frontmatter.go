package skill

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const maxSkillNameLength = 64
const maxSkillDescriptionLength = 1024

var skillNamePattern = regexp.MustCompile(`^[a-z0-9-]+$`)

func loadSkillFile(path string) (*Skill, []Diagnostic, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, nil, err
	}
	body, err := os.ReadFile(abs)
	if err != nil {
		return nil, nil, err
	}
	frontmatter, content, diagnostics := parseFrontmatter(string(body), abs)
	item := Skill{
		Name:                   strings.TrimSpace(frontmatter["name"]),
		Description:            strings.TrimSpace(frontmatter["description"]),
		FilePath:               abs,
		BaseDir:                filepath.Dir(abs),
		Body:                   "",
		License:                strings.TrimSpace(frontmatter["license"]),
		Compatibility:          strings.TrimSpace(frontmatter["compatibility"]),
		AllowedTools:           parseList(frontmatter["allowed-tools"]),
		DisableModelInvocation: parseBool(frontmatter["disable-model-invocation"]),
		Metadata:               parseMetadata(frontmatter["metadata"]),
	}
	if item.Name == "" {
		item.Name = filepath.Base(item.BaseDir)
		diagnostics = append(diagnostics, Diagnostic{
			Path:    abs,
			Message: "missing skill name; defaulting to directory name",
		})
	}
	if item.Description == "" {
		diagnostics = append(diagnostics, Diagnostic{
			Path:    abs,
			Message: "missing required description; skipping skill",
		})
		return nil, diagnostics, nil
	}
	diagnostics = append(diagnostics, validateSkill(item)...)
	if strings.TrimSpace(content) != "" {
		item.Body = strings.TrimSpace(content)
	}
	return &item, diagnostics, nil
}

func validateSkill(item Skill) []Diagnostic {
	var diagnostics []Diagnostic
	parentDir := filepath.Base(item.BaseDir)
	if item.Name != parentDir {
		diagnostics = append(diagnostics, Diagnostic{
			Path:    item.FilePath,
			Message: fmt.Sprintf("name %q does not match parent directory %q", item.Name, parentDir),
		})
	}
	if len(item.Name) > maxSkillNameLength {
		diagnostics = append(diagnostics, Diagnostic{
			Path:    item.FilePath,
			Message: fmt.Sprintf("name exceeds %d characters (%d)", maxSkillNameLength, len(item.Name)),
		})
	}
	if !skillNamePattern.MatchString(item.Name) {
		diagnostics = append(diagnostics, Diagnostic{
			Path:    item.FilePath,
			Message: "name contains invalid characters (must be lowercase a-z, 0-9, hyphens only)",
		})
	}
	if strings.HasPrefix(item.Name, "-") || strings.HasSuffix(item.Name, "-") {
		diagnostics = append(diagnostics, Diagnostic{
			Path:    item.FilePath,
			Message: "name must not start or end with a hyphen",
		})
	}
	if strings.Contains(item.Name, "--") {
		diagnostics = append(diagnostics, Diagnostic{
			Path:    item.FilePath,
			Message: "name must not contain consecutive hyphens",
		})
	}
	if len(item.Description) > maxSkillDescriptionLength {
		diagnostics = append(diagnostics, Diagnostic{
			Path:    item.FilePath,
			Message: fmt.Sprintf("description exceeds %d characters (%d)", maxSkillDescriptionLength, len(item.Description)),
		})
	}
	return diagnostics
}

func parseFrontmatter(raw string, path string) (map[string]string, string, []Diagnostic) {
	const fence = "---"
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	if !strings.HasPrefix(raw, fence+"\n") {
		return map[string]string{}, raw, nil
	}
	rest := strings.TrimPrefix(raw, fence+"\n")
	end := strings.Index(rest, "\n"+fence+"\n")
	if end < 0 {
		return map[string]string{}, raw, []Diagnostic{{
			Path:    path,
			Message: "unterminated frontmatter; treating file as plain body",
		}}
	}
	header := rest[:end]
	content := rest[end+len("\n"+fence+"\n"):]
	fields, diagnostics := parseFrontmatterFields(header, path)
	return fields, content, diagnostics
}

func parseFrontmatterFields(header string, path string) (map[string]string, []Diagnostic) {
	scanner := bufio.NewScanner(strings.NewReader(header))
	fields := make(map[string]string)
	var diagnostics []Diagnostic
	var currentKey string
	var currentMode string
	var nestedIndent int
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if currentKey != "" && indent > nestedIndent {
			value := strings.TrimSpace(line)
			switch currentMode {
			case "block":
				appendField(fields, currentKey, strings.TrimPrefix(line, strings.Repeat(" ", nestedIndent+2)))
			case "list":
				value = strings.TrimSpace(strings.TrimPrefix(value, "-"))
				appendField(fields, currentKey, value)
			case "map":
				appendField(fields, currentKey, value)
			default:
				appendField(fields, currentKey, value)
			}
			continue
		}
		currentKey = ""
		currentMode = ""
		nestedIndent = indent

		key, value, ok := strings.Cut(line, ":")
		if !ok {
			diagnostics = append(diagnostics, Diagnostic{
				Path:    path,
				Message: fmt.Sprintf("ignoring malformed frontmatter line %q", trimmed),
			})
			continue
		}
		key = normalizeKey(key)
		value = strings.TrimSpace(value)
		switch value {
		case "|", ">":
			currentKey = key
			currentMode = "block"
			nestedIndent = indent
			fields[key] = ""
		case "":
			currentKey = key
			currentMode = "map"
			nestedIndent = indent
			fields[key] = ""
		default:
			fields[key] = strings.Trim(value, `"'`)
			if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
				fields[key] = strings.Trim(value, "[]")
			}
			if strings.HasPrefix(value, "- ") {
				currentKey = key
				currentMode = "list"
				nestedIndent = indent
				fields[key] = strings.TrimSpace(strings.TrimPrefix(value, "- "))
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fields, append(diagnostics, Diagnostic{
			Path:    path,
			Message: err.Error(),
		})
	}
	for key, value := range fields {
		fields[key] = strings.TrimSpace(value)
	}
	return fields, diagnostics
}

func appendField(fields map[string]string, key, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if existing := strings.TrimSpace(fields[key]); existing != "" {
		fields[key] = existing + "\n" + value
		return
	}
	fields[key] = value
}

func normalizeKey(key string) string {
	key = strings.TrimSpace(strings.Trim(key, `"'`))
	key = strings.ToLower(key)
	key = strings.ReplaceAll(key, "_", "-")
	return key
}

func parseBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "yes", "on":
		return true
	default:
		return false
	}
}

func parseList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == '\n' || r == ','
	})
	out := make([]string, 0, len(parts))
	seen := make(map[string]bool)
	for _, part := range parts {
		part = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(part), "-"))
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return out
}

func parseMetadata(value string) map[string]string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	out := make(map[string]string)
	for _, line := range strings.Split(value, "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(strings.Trim(val, `"'`))
		if key == "" || val == "" {
			continue
		}
		out[key] = val
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func readSkillBody(item Skill) (string, error) {
	if strings.TrimSpace(item.Body) != "" {
		return strings.TrimSpace(item.Body), nil
	}
	raw, err := os.ReadFile(item.FilePath)
	if err != nil {
		return "", err
	}
	_, content, _ := parseFrontmatter(string(raw), item.FilePath)
	return strings.TrimSpace(content), nil
}
