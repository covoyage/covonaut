package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad_RecursiveDiscoveryAndCollisions(t *testing.T) {
	root := t.TempDir()
	mustWriteSkill(t, filepath.Join(root, "alpha", "SKILL.md"), `---
name: alpha
description: First skill
---
Alpha body`)
	mustWriteSkill(t, filepath.Join(root, "nested", "beta", "SKILL.md"), `---
name: beta
description: Beta skill
disable-model-invocation: true
---
Beta body`)
	mustWriteSkill(t, filepath.Join(root, "collision", "SKILL.md"), `---
name: alpha
description: Duplicate alpha
---
Other body`)
	mustWriteSkill(t, filepath.Join(root, "skip", "SKILL.md"), `---
name: skip
---
No description`)
	mustWriteSkill(t, filepath.Join(root, "alpha", "child", "SKILL.md"), `---
name: child
description: Should not be discovered because parent is a skill root
---
child`)

	skills, diagnostics, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 2 {
		t.Fatalf("skills len = %d", len(skills))
	}
	if _, found := FindByName(skills, "child"); found {
		t.Fatal("unexpected nested child skill under existing root")
	}
	var collisionWarn, missingDescWarn bool
	for _, diag := range diagnostics {
		if strings.Contains(diag.Message, "collides") {
			collisionWarn = true
		}
		if strings.Contains(diag.Message, "missing required description") {
			missingDescWarn = true
		}
	}
	if !collisionWarn {
		t.Fatal("expected collision diagnostic")
	}
	if !missingDescWarn {
		t.Fatal("expected missing description diagnostic")
	}
}

func TestIndexAndExplicitInvocation(t *testing.T) {
	skills := []Skill{
		{Name: "visible", Description: "Shown", FilePath: "/tmp/visible/SKILL.md", BaseDir: "/tmp/visible", Body: "Visible body"},
		{Name: "hidden", Description: "Hidden", FilePath: "/tmp/hidden/SKILL.md", BaseDir: "/tmp/hidden", Body: "Hidden body", DisableModelInvocation: true},
	}
	index := Index(skills)
	if !strings.Contains(index, "<name>visible</name>") {
		t.Fatalf("index = %q", index)
	}
	if strings.Contains(index, "hidden") {
		t.Fatalf("hidden skill should not appear in index: %q", index)
	}
	invocation := ExplicitInvocation(skills[1], "run it now")
	if !strings.Contains(invocation, `<skill name="hidden"`) {
		t.Fatalf("invocation = %q", invocation)
	}
	if !strings.Contains(invocation, "User: run it now") {
		t.Fatalf("invocation args = %q", invocation)
	}
}

func TestParseCommand(t *testing.T) {
	cmd, ok := ParseCommand(" /skill:debug investigate flaky test ")
	if !ok {
		t.Fatal("expected command to parse")
	}
	if cmd.Name != "debug" || cmd.Args != "investigate flaky test" {
		t.Fatalf("command = %#v", cmd)
	}
	if _, ok := ParseCommand("/thinking summarized"); ok {
		t.Fatal("unexpected non-skill command parse")
	}
}

func TestLoad_WarnsOnSpecValidationIssues(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "MismatchDir")
	longDescription := strings.Repeat("x", maxSkillDescriptionLength+1)
	mustWriteSkill(t, filepath.Join(dir, "SKILL.md"), `---
name: Invalid--Name-
description: `+longDescription+`
---
Body`)

	skills, diagnostics, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 {
		t.Fatalf("skills len = %d", len(skills))
	}
	var parentWarn, charsetWarn, hyphenWarn, longDescWarn bool
	for _, diag := range diagnostics {
		switch {
		case strings.Contains(diag.Message, "does not match parent directory"):
			parentWarn = true
		case strings.Contains(diag.Message, "invalid characters"):
			charsetWarn = true
		case strings.Contains(diag.Message, "start or end with a hyphen"),
			strings.Contains(diag.Message, "consecutive hyphens"):
			hyphenWarn = true
		case strings.Contains(diag.Message, "description exceeds"):
			longDescWarn = true
		}
	}
	if !parentWarn || !charsetWarn || !hyphenWarn || !longDescWarn {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}

func TestActivePrompt_ReadsBodyOnDemand(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "planner", "SKILL.md")
	mustWriteSkill(t, path, `---
name: planner
description: Planning skill
---
Plan carefully.
Use references/guide.md when needed.`)

	skills, _, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 {
		t.Fatalf("skills len = %d", len(skills))
	}
	item := skills[0]
	item.Body = ""

	prompt := ActivePrompt([]Skill{item})
	if !strings.Contains(prompt, "Plan carefully.") || !strings.Contains(prompt, "references/guide.md") {
		t.Fatalf("prompt = %q", prompt)
	}

	invocation := ExplicitInvocation(item, "do it")
	if !strings.Contains(invocation, "Plan carefully.") || !strings.Contains(invocation, "User: do it") {
		t.Fatalf("invocation = %q", invocation)
	}
}

func mustWriteSkill(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
