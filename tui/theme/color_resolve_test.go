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

func TestDetectColorModeAppleTerminal(t *testing.T) {
	t.Setenv("COLORTERM", "")
	t.Setenv("WT_SESSION", "")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("TERM_PROGRAM", "Apple_Terminal")
	if m := DetectColorMode(); m != ColorMode256 {
		t.Fatalf("Apple_Terminal should force 256, got %v", m)
	}
}
