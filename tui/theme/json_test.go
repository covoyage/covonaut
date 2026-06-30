package theme

import (
	"strings"
	"testing"
)

func TestParseSemanticThemeJSONPiShape(t *testing.T) {
	const raw = `{
		"name": "custom",
		"vars": { "accent": "#112233", "green": "#00ff00" },
		"colors": {
			"accent": "accent",
			"mdHeading": "#ff00aa",
			"success": "green"
		}
	}`
	sem, err := ParseSemanticThemeJSON([]byte(raw), DefaultSemanticDark())
	if err != nil {
		t.Fatal(err)
	}
	if sem.Name != "custom" {
		t.Fatalf("name %q", sem.Name)
	}
	if sem.Accent != "#112233" {
		t.Fatalf("accent %q", sem.Accent)
	}
	if sem.MdHeading != "#ff00aa" {
		t.Fatalf("mdHeading %q", sem.MdHeading)
	}
	if sem.Success != "#00ff00" {
		t.Fatalf("success %q", sem.Success)
	}
}

func TestParseSemanticThemeJSONIgnoresUnknownKeys(t *testing.T) {
	raw := `{"name":"x","vars":{},"colors":{"unknownKey":"#123456","accent":"#abcdef"}}`
	sem, err := ParseSemanticThemeJSON([]byte(raw), DefaultSemanticDark())
	if err != nil {
		t.Fatal(err)
	}
	if sem.Accent != "#abcdef" {
		t.Fatalf("accent %q", sem.Accent)
	}
}

func TestSetSemanticThemeUpdatesGlobals(t *testing.T) {
	ForceColor(true)
	t.Cleanup(func() { ForceColor(false) })
	SetSemanticTheme(DefaultSemanticLight(), ColorModeTruecolor)
	if !strings.Contains(StyleUser.Render("x"), "\x1b[") {
		t.Fatalf("StyleUser should emit ansi: %q", StyleUser.Render("x"))
	}
	SetSemanticTheme(DefaultSemanticDark(), ColorModeTruecolor)
}
