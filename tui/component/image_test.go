package component

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"testing"

	"github.com/covoyage/covonaut/tui/core"
)

// makeTestImage returns a 4x4 RGBA image with 4 coloured quadrants.
func makeTestImage() image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			var c color.NRGBA
			switch {
			case x < 2 && y < 2:
				c = color.NRGBA{255, 0, 0, 255}
			case x >= 2 && y < 2:
				c = color.NRGBA{0, 255, 0, 255}
			case x < 2 && y >= 2:
				c = color.NRGBA{0, 0, 255, 255}
			default:
				c = color.NRGBA{255, 255, 255, 255}
			}
			img.Set(x, y, c)
		}
	}
	return img
}

func TestImageHalfBlockRender(t *testing.T) {
	im := NewImage(makeTestImage())
	im.SetProtocol(ImageProtocolHalfBlock)
	im.SetMaxSize(8, 0)
	lines := im.Render(8)
	if len(lines) == 0 {
		t.Fatalf("no lines")
	}
	for _, l := range lines {
		if !strings.Contains(l, "\u2580") {
			t.Fatalf("half-block char missing in: %q", l)
		}
	}
}

func TestImageASCIIRender(t *testing.T) {
	im := NewImage(makeTestImage())
	im.SetProtocol(ImageProtocolASCII)
	im.SetMaxSize(6, 0)
	lines := im.Render(6)
	if len(lines) == 0 {
		t.Fatalf("no lines")
	}
	for _, l := range lines {
		if core.VisibleWidth(l) != 6 {
			t.Fatalf("width %d != 6, line=%q", core.VisibleWidth(l), l)
		}
	}
}

func TestImageKittyEncoder(t *testing.T) {
	im := NewImage(makeTestImage())
	im.SetProtocol(ImageProtocolKitty)
	im.SetMaxSize(4, 0)
	lines := im.Render(4)
	if len(lines) == 0 {
		t.Fatalf("no lines")
	}
	// First line should contain the APC prefix \x1b_G... and terminator \x1b\\.
	if !strings.HasPrefix(lines[0], "\x1b_G") {
		t.Fatalf("missing kitty APC prefix: %q", lines[0])
	}
	if !strings.Contains(lines[0], "\x1b\\") {
		t.Fatalf("missing kitty APC terminator in first line")
	}
	// Verify base64 payload decodes to a PNG.
	// Pattern: ...;<base64>\x1b\\
	idx := strings.Index(lines[0], ";")
	if idx < 0 {
		t.Fatalf("no separator in APC: %q", lines[0])
	}
	rest := lines[0][idx+1:]
	end := strings.Index(rest, "\x1b\\")
	if end < 0 {
		t.Fatalf("no terminator: %q", rest)
	}
	if _, err := base64.StdEncoding.DecodeString(rest[:end]); err != nil {
		t.Fatalf("invalid base64: %v", err)
	}
}

func TestImageITerm2Encoder(t *testing.T) {
	im := NewImage(makeTestImage())
	im.SetProtocol(ImageProtocolITerm2)
	im.SetMaxSize(4, 0)
	lines := im.Render(4)
	if !strings.HasPrefix(lines[0], "\x1b]1337;File=inline=1") {
		t.Fatalf("missing iTerm2 OSC prefix: %q", lines[0])
	}
}

func TestImageFromBytes(t *testing.T) {
	// Encode a tiny PNG and decode it back through NewImageFromBytes.
	src := makeTestImage()
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatalf("encode: %v", err)
	}
	im, err := NewImageFromBytes(buf.Bytes())
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if im.format != "png" {
		t.Fatalf("format=%s", im.format)
	}
	if len(im.rawPNG) == 0 {
		t.Fatalf("rawPNG not captured")
	}
}

func TestDetectImageProtocol_Kitty(t *testing.T) {
	t.Setenv("KITTY_WINDOW_ID", "1")
	t.Setenv("TERM", "")
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("LC_TERMINAL", "")
	ResetImageProtocolDetection()
	defer ResetImageProtocolDetection()
	if got := DetectImageProtocol(); got != ImageProtocolKitty {
		t.Fatalf("want kitty, got %v", got)
	}
}

func TestDetectImageProtocol_ITerm2(t *testing.T) {
	os.Unsetenv("KITTY_WINDOW_ID")
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("TERM_PROGRAM", "iTerm.app")
	ResetImageProtocolDetection()
	defer ResetImageProtocolDetection()
	if got := DetectImageProtocol(); got != ImageProtocolITerm2 {
		t.Fatalf("want iterm2, got %v", got)
	}
}

func TestDetectImageProtocol_Fallback(t *testing.T) {
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("TERM_PROGRAM", "Apple_Terminal")
	t.Setenv("LC_TERMINAL", "")
	ResetImageProtocolDetection()
	defer ResetImageProtocolDetection()
	if got := DetectImageProtocol(); got != ImageProtocolHalfBlock {
		t.Fatalf("want halfblock, got %v", got)
	}
}
