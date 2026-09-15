package theme

import (
	"strings"
	"testing"
)

func TestFgParamsTruecolor(t *testing.T) {
	p := FgParams("#ff0000", ColorModeTruecolor)
	if !strings.Contains(p, "38;2;255;0;0") {
		t.Fatalf("unexpected fg params: %q", p)
	}
}

func TestFgParams256(t *testing.T) {
	p := FgParams("#ff0000", ColorMode256)
	if !strings.HasPrefix(p, "38;5;") {
		t.Fatalf("expected 256 palette prefix, got %q", p)
	}
}

func TestRGBTo256GrayscalePreference(t *testing.T) {
	// Near-neutral gray should map toward grayscale ramp sometimes.
	idx := RGBTo256(10, 10, 10)
	if idx < 16 || idx > 255 {
		t.Fatalf("out of range index %d", idx)
	}
}

func TestMixHexMidpoint(t *testing.T) {
	got := MixHex("#000000", "#ffffff", 0.5)
	if got != "#7f7f7f" {
		t.Fatalf("MixHex midpoint = %q", got)
	}
}

func TestDetectColorModeAppleTerminal(t *testing.T) {
	t.Setenv("COLORTERM", "")
	t.Setenv("WT_SESSION", "")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("TERM_PROGRAM", "Apple_Terminal")
	if m := DetectColorMode(); m != ColorMode256 {
		t.Fatalf("Apple_Terminal should force 256, got %v", m)
	}
}

// TestFaintHairline 验证分割线混色：亮文字（深色底）向黑混、暗文字
// （浅色底）向白混、非 hex 原样返回。
func TestFaintHairline(t *testing.T) {
	dark := FaintHairline("#606070", "#e0e0e0")
	if dark != "#292930" {
		t.Fatalf("dark-theme hairline = %q, want #292930", dark)
	}
	light := FaintHairline("#bdbdbd", "#212121")
	if light != "#e3e3e3" {
		t.Fatalf("light-theme hairline = %q, want #e3e3e3", light)
	}
	if got := FaintHairline("plain", "#e0e0e0"); got != "plain" {
		t.Fatalf("non-hex base must pass through, got %q", got)
	}
}
