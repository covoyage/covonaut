package component

import (
	"strings"
	"testing"

	"github.com/covoyage/covonaut/tui/terminal"
)

func TestKeyHelpRender(t *testing.T) {
	km := terminal.NewKeybindingsManager(map[string]terminal.KeybindingDef{
		"app.quit":  {DefaultKeys: []terminal.KeyID{"ctrl+q"}, Description: "Quit application"},
		"app.save":  {DefaultKeys: []terminal.KeyID{"ctrl+s"}, Description: "Save file"},
		"edit.undo": {DefaultKeys: []terminal.KeyID{"ctrl+z"}, Description: "Undo last change"},
	})
	h := NewKeyHelp(km)
	lines := h.Render(60)
	joined := strings.Join(lines, "\n")

	for _, want := range []string{"Keybindings", "ctrl+q", "Quit application", "Undo last change"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in output:\n%s", want, joined)
		}
	}
}

func TestKeyHelpFilter(t *testing.T) {
	km := terminal.NewKeybindingsManager(map[string]terminal.KeybindingDef{
		"app.quit":  {DefaultKeys: []terminal.KeyID{"ctrl+q"}, Description: "Quit"},
		"app.save":  {DefaultKeys: []terminal.KeyID{"ctrl+s"}, Description: "Save"},
		"edit.undo": {DefaultKeys: []terminal.KeyID{"ctrl+z"}, Description: "Undo"},
	})
	h := NewKeyHelp(km)
	h.SetFilter("quit")
	joined := strings.Join(h.Render(60), "\n")
	if !strings.Contains(joined, "ctrl+q") {
		t.Fatalf("expected ctrl+q in filtered output:\n%s", joined)
	}
	if strings.Contains(joined, "ctrl+s") {
		t.Fatalf("unexpected ctrl+s in filtered output:\n%s", joined)
	}
}

func TestKeyHelpViewportClipping(t *testing.T) {
	defs := map[string]terminal.KeybindingDef{}
	for i := 0; i < 30; i++ {
		defs[fakeID(i)] = terminal.KeybindingDef{
			DefaultKeys: []terminal.KeyID{"ctrl+a"},
			Description: "entry",
		}
	}
	km := terminal.NewKeybindingsManager(defs)
	h := NewKeyHelp(km)
	h.SetMaxRows(5)
	lines := h.Render(60)
	if int64(len(lines)) != 5 {
		t.Fatalf("viewport should clip to 5 rows, got %d", len(lines))
	}
}

func fakeID(n int) string {
	return "group.binding" + string(rune('a'+n%26)) + string(rune('0'+n/26))
}
