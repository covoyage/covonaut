package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Skill is one parsed SKILL.md package discovered from disk.
type Skill struct {
	Name                   string
	Description            string
	FilePath               string
	BaseDir                string
	Body                   string
	License                string
	Compatibility          string
	AllowedTools           []string
	DisableModelInvocation bool
	Metadata               map[string]string
}

// Diagnostic reports a non-fatal issue found while loading a skill.
type Diagnostic struct {
	Path    string
	Message string
}

// Command represents an explicit /skill:name invocation.
type Command struct {
	Name string
	Args string
}

// Index formats discoverable skills for progressive-disclosure prompt injection.
func Index(skills []Skill) string {
	visible := make([]Skill, 0, len(skills))
	for _, item := range skills {
		if item.DisableModelInvocation {
			continue
		}
		visible = append(visible, item)
	}
	if len(visible) == 0 {
		return ""
	}
	sort.Slice(visible, func(i, j int) bool { return visible[i].Name < visible[j].Name })
	var b strings.Builder
	b.WriteString("The following skills provide specialized instructions for specific tasks.\n")
	b.WriteString("When a task matches one of these descriptions, respond with /skill:<name> followed by optional arguments to load that skill for the next turn.\n")
	b.WriteString("When a skill references a relative path, resolve it against the skill directory (the parent directory of SKILL.md).\n\n")
	b.WriteString("<available_skills>\n")
	for _, item := range visible {
		b.WriteString("  <skill>\n")
		b.WriteString("    <name>" + escapeXML(item.Name) + "</name>\n")
		b.WriteString("    <description>" + escapeXML(item.Description) + "</description>\n")
		b.WriteString("    <path>" + escapeXML(item.FilePath) + "</path>\n")
		b.WriteString("  </skill>\n")
	}
	b.WriteString("</available_skills>")
	return b.String()
}

// ExplicitInvocation expands a skill into a canonical user-facing prompt block.
func ExplicitInvocation(item Skill, args string) string {
	var b strings.Builder
	body, err := readSkillBody(item)
	if err != nil {
		body = strings.TrimSpace(item.Body)
	}
	b.WriteString(`<skill name="` + escapeXML(item.Name) + `" path="` + escapeXML(item.FilePath) + `" base_dir="` + escapeXML(item.BaseDir) + `">` + "\n")
	b.WriteString(body)
	b.WriteString("\n</skill>")
	args = strings.TrimSpace(args)
	if args != "" {
		b.WriteString("\n\nUser: ")
		b.WriteString(args)
	}
	return b.String()
}

// ActivePrompt expands selected skills into a system-facing instruction block.
func ActivePrompt(skills []Skill) string {
	if len(skills) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<active_skills>\n")
	for _, item := range skills {
		body, err := readSkillBody(item)
		if err != nil {
			body = strings.TrimSpace(item.Body)
		}
		b.WriteString("  <skill name=\"" + escapeXML(item.Name) + "\" path=\"" + escapeXML(item.FilePath) + "\" base_dir=\"" + escapeXML(item.BaseDir) + "\">\n")
		b.WriteString(body)
		b.WriteString("\n  </skill>\n")
	}
	b.WriteString("</active_skills>")
	return b.String()
}

// ParseCommand returns the skill command if input starts with /skill:NAME.
func ParseCommand(input string) (Command, bool) {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "/skill:") {
		return Command{}, false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "/skill:"))
	if rest == "" {
		return Command{}, false
	}
	name, args, _ := strings.Cut(rest, " ")
	name = strings.TrimSpace(name)
	if name == "" {
		return Command{}, false
	}
	return Command{Name: name, Args: strings.TrimSpace(args)}, true
}

// FindByName returns the first skill with the given name.
func FindByName(skills []Skill, name string) (Skill, bool) {
	for _, item := range skills {
		if item.Name == name {
			return item, true
		}
	}
	return Skill{}, false
}

// ResolveSelection resolves selected skill names in the provided order.
func ResolveSelection(skills []Skill, names []string) ([]Skill, []string) {
	if len(names) == 0 {
		return nil, nil
	}
	out := make([]Skill, 0, len(names))
	var missing []string
	seen := make(map[string]bool)
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		item, ok := FindByName(skills, name)
		if !ok {
			missing = append(missing, name)
			continue
		}
		out = append(out, item)
	}
	return out, missing
}

// Load reads skills from one or more skill roots. Collisions keep the first skill found.
func Load(paths ...string) ([]Skill, []Diagnostic, error) {
	var all []Skill
	var diagnostics []Diagnostic
	seen := make(map[string]string)
	for _, root := range paths {
		loaded, diags, err := LoadPath(root)
		diagnostics = append(diagnostics, diags...)
		if err != nil {
			return nil, diagnostics, err
		}
		for _, item := range loaded {
			if prev, exists := seen[item.Name]; exists {
				diagnostics = append(diagnostics, Diagnostic{
					Path:    item.FilePath,
					Message: fmt.Sprintf("skill %q collides with %s; keeping the first one", item.Name, prev),
				})
				continue
			}
			seen[item.Name] = item.FilePath
			all = append(all, item)
		}
	}
	return all, diagnostics, nil
}

// LoadPath reads one skill root from either a skill directory or a SKILL.md path.
func LoadPath(path string) ([]Skill, []Diagnostic, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	if !info.IsDir() {
		item, diagnostics, err := loadSkillFile(path)
		if err != nil {
			return nil, diagnostics, err
		}
		if item == nil {
			return nil, diagnostics, nil
		}
		return []Skill{*item}, diagnostics, nil
	}
	return loadSkillsFromDir(path)
}

func loadSkillsFromDir(root string) ([]Skill, []Diagnostic, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, nil, err
	}
	var out []Skill
	var diagnostics []Diagnostic
	var walk func(string) error
	walk = func(dir string) error {
		skillFile := filepath.Join(dir, "SKILL.md")
		if info, err := os.Stat(skillFile); err == nil && !info.IsDir() {
			item, diags, loadErr := loadSkillFile(skillFile)
			diagnostics = append(diagnostics, diags...)
			if loadErr != nil {
				return loadErr
			}
			if item != nil {
				out = append(out, *item)
			}
			return nil
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			if strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			if err := walk(filepath.Join(dir, entry.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root); err != nil {
		return out, diagnostics, err
	}
	return out, diagnostics, nil
}

func escapeXML(value string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
	)
	return replacer.Replace(value)
}
