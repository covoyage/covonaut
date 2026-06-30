package component

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"  // register gif decoder
	_ "image/jpeg" // register jpeg decoder
	"image/png"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/covoyage/covonaut/tui/core"
)

// ---------------------------------------------------------------------------
// Image — a Component that renders a raster image inside the terminal.
//
// Protocol priority (auto):
//   - Kitty graphics protocol (native pixel rendering) — best quality.
//   - iTerm2 inline image protocol — native rendering on iTerm2/WezTerm.
//   - Unicode half-block fallback (▀) — works on every modern truecolor tty.
//   - ASCII fallback — monochrome weighted characters (no colour).
//
// Zero external dependencies: all decoding uses stdlib `image/*` packages,
// resampling is a simple nearest-neighbour implementation.
// ---------------------------------------------------------------------------

// ImageProtocol selects the rendering strategy.
type ImageProtocol int64

const (
	ImageProtocolAuto      ImageProtocol = 0
	ImageProtocolKitty     ImageProtocol = 1
	ImageProtocolITerm2    ImageProtocol = 2
	ImageProtocolHalfBlock ImageProtocol = 3
	ImageProtocolASCII     ImageProtocol = 4
)

// Image is a Component that renders a decoded image.
//
// Sizing uses terminal cells; one cell is 2 pixels tall in half-block mode.
// When MaxWidth or MaxHeight are 0, the image expands to the available
// viewport width and preserves its aspect ratio.
type Image struct {
	mu sync.RWMutex

	img       image.Image
	rawPNG    []byte // original PNG bytes (for Kitty/iTerm2 transmission)
	format    string // "png", "jpeg", "gif", etc
	maxWidth  int64  // cells, 0 = viewport
	maxHeight int64  // cells, 0 = unconstrained (but capped by Render caller)
	protocol  ImageProtocol

	// cache keyed on (width, protocol).
	cacheWidth int64
	cacheProto ImageProtocol
	cacheLines []string
}

// NewImage creates an Image from a decoded image.Image. `format` should be
// "png" / "jpeg" / "gif" — it is used to decide whether to transmit the
// original bytes (Kitty/iTerm2) or re-encode to PNG on the fly.
//
// img may be nil (e.g. when a caller fails to decode but still wants a
// placeholder component); Render on a nil-image returns no lines instead of
// panicking on the nil interface dereference.
func NewImage(img image.Image) *Image {
	return &Image{img: img, format: "png"}
}

// NewImageFromBytes decodes `data` and returns an Image component.
func NewImageFromBytes(data []byte) (*Image, error) {
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("tui: decode image: %w", err)
	}
	out := &Image{img: img, format: format}
	if format == "png" {
		cp := make([]byte, len(data))
		copy(cp, data)
		out.rawPNG = cp
	}
	return out, nil
}

// NewImageFromFile reads and decodes an image from disk.
func NewImageFromFile(path string) (*Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	return NewImageFromBytes(data)
}

// SetMaxSize constrains the render to (maxW x maxH) cells. Zero = no limit.
func (im *Image) SetMaxSize(maxW, maxH int64) {
	im.mu.Lock()
	im.maxWidth = maxW
	im.maxHeight = maxH
	im.cacheLines = nil
	im.mu.Unlock()
}

// SetProtocol forces a specific rendering protocol.
func (im *Image) SetProtocol(p ImageProtocol) {
	im.mu.Lock()
	im.protocol = p
	im.cacheLines = nil
	im.mu.Unlock()
}

// Protocol returns the currently-configured protocol (may be Auto).
func (im *Image) Protocol() ImageProtocol {
	im.mu.RLock()
	defer im.mu.RUnlock()
	return im.protocol
}

// Render emits one or more lines representing the image. The exact content
// depends on the chosen protocol; differential renderers will redraw the
// whole image on change (acceptable since images rarely change).
func (im *Image) Render(width int64) []string {
	im.mu.Lock()
	defer im.mu.Unlock()

	// 防御 NewImage(nil) 误用：nil image.Image 接口解引用会 panic。
	if im.img == nil {
		return nil
	}

	proto := im.protocol
	if proto == ImageProtocolAuto {
		proto = DetectImageProtocol()
	}

	targetW := width
	if im.maxWidth > 0 && im.maxWidth < targetW {
		targetW = im.maxWidth
	}
	if targetW < 1 {
		targetW = 1
	}

	if im.cacheLines != nil && im.cacheWidth == targetW && im.cacheProto == proto {
		return im.cacheLines
	}

	var (
		lines   []string
		textual bool
	)
	switch proto {
	case ImageProtocolKitty:
		lines = im.renderKitty(targetW)
	case ImageProtocolITerm2:
		lines = im.renderITerm2(targetW)
	case ImageProtocolASCII:
		lines = im.renderASCII(targetW)
		textual = true
	default:
		lines = im.renderHalfBlock(targetW)
		textual = true
	}

	// Pad only textual modes; Kitty/iTerm2 sequences must not be padded
	// because trailing spaces would overwrite the image pixels in-place.
	if textual {
		for i, ln := range lines {
			lines[i] = core.PadToWidth(ln, width)
		}
	}

	im.cacheLines = lines
	im.cacheWidth = targetW
	im.cacheProto = proto
	return lines
}

func (im *Image) Invalidate() {
	im.mu.Lock()
	im.cacheLines = nil
	im.mu.Unlock()
}

func (im *Image) Update(msg core.Msg) core.Cmd { return nil }

// ---------------------------------------------------------------------------
// Kitty encoder
// ---------------------------------------------------------------------------

// kittyChunkSize caps the base64 payload per APC command (Kitty spec ≤ 4096).
const kittyChunkSize = 4096

func (im *Image) renderKitty(widthCells int64) []string {
	png, err := im.getPNG()
	if err != nil {
		return im.renderHalfBlock(widthCells)
	}
	srcW := im.img.Bounds().Dx()
	srcH := im.img.Bounds().Dy()
	cellH := cellHeightFromPixels(srcW, srcH, int(widthCells), int(im.maxHeight))

	encoded := base64.StdEncoding.EncodeToString(png)
	var b strings.Builder
	// First chunk carries control keys.
	first := true
	for len(encoded) > 0 {
		chunkLen := kittyChunkSize
		if chunkLen > len(encoded) {
			chunkLen = len(encoded)
		}
		chunk := encoded[:chunkLen]
		encoded = encoded[chunkLen:]
		more := int64(0)
		if len(encoded) > 0 {
			more = 1
		}
		if first {
			first = false
			fmt.Fprintf(&b, "\x1b_Gf=100,a=T,c=%d,r=%d,m=%d;%s\x1b\\", widthCells, cellH, more, chunk)
		} else {
			fmt.Fprintf(&b, "\x1b_Gm=%d;%s\x1b\\", more, chunk)
		}
	}

	out := make([]string, cellH)
	out[0] = b.String() // the image is drawn "out-of-band" starting here.
	for i := 1; i < cellH; i++ {
		out[i] = ""
	}
	return out
}

// ---------------------------------------------------------------------------
// iTerm2 encoder
// ---------------------------------------------------------------------------

func (im *Image) renderITerm2(widthCells int64) []string {
	png, err := im.getPNG()
	if err != nil {
		return im.renderHalfBlock(widthCells)
	}
	srcW := im.img.Bounds().Dx()
	srcH := im.img.Bounds().Dy()
	cellH := cellHeightFromPixels(srcW, srcH, int(widthCells), int(im.maxHeight))
	payload := base64.StdEncoding.EncodeToString(png)
	seq := fmt.Sprintf(
		"\x1b]1337;File=inline=1;preserveAspectRatio=1;width=%d;height=%d;size=%d:%s\x07",
		widthCells, cellH, len(png), payload,
	)
	out := make([]string, cellH)
	out[0] = seq
	for i := 1; i < cellH; i++ {
		out[i] = ""
	}
	return out
}

// ---------------------------------------------------------------------------
// Half-block encoder (truecolor ANSI).
// ---------------------------------------------------------------------------

func (im *Image) renderHalfBlock(widthCells int64) []string {
	srcBounds := im.img.Bounds()
	srcW := srcBounds.Dx()
	srcH := srcBounds.Dy()
	if srcW == 0 || srcH == 0 {
		return []string{""}
	}
	// Each cell renders 2 pixels vertically (▀: fg=top, bg=bottom).
	targetW := int(widthCells)
	if targetW < 1 {
		targetW = 1
	}
	aspect := float64(srcH) / float64(srcW)
	// Terminal cell is ~2:1 (h:w) on most fonts, so divide by 2.
	targetH := int(float64(targetW) * aspect / 2.0 * 2.0) // pixel rows
	if targetH < 2 {
		targetH = 2
	}
	if im.maxHeight > 0 {
		maxPx := int(im.maxHeight * 2)
		if targetH > maxPx {
			targetH = maxPx
		}
	}
	if targetH%2 != 0 {
		targetH++
	}

	resized := resizeNearest(im.img, targetW, targetH)

	var out []string
	for y := 0; y < targetH; y += 2 {
		var line strings.Builder
		prevFg := color.NRGBA{}
		prevBg := color.NRGBA{}
		prevFgSet := false
		prevBgSet := false
		for x := 0; x < targetW; x++ {
			fg := nrgbaAt(resized, x, y)
			bg := nrgbaAt(resized, x, y+1)

			if !prevFgSet || fg != prevFg {
				fmt.Fprintf(&line, "\x1b[38;2;%d;%d;%dm", fg.R, fg.G, fg.B)
				prevFg = fg
				prevFgSet = true
			}
			if !prevBgSet || bg != prevBg {
				fmt.Fprintf(&line, "\x1b[48;2;%d;%d;%dm", bg.R, bg.G, bg.B)
				prevBg = bg
				prevBgSet = true
			}
			line.WriteString("\u2580") // ▀
		}
		line.WriteString("\x1b[0m")
		out = append(out, line.String())
	}
	return out
}

// ---------------------------------------------------------------------------
// ASCII fallback (monochrome).
// ---------------------------------------------------------------------------

var asciiRamp = []rune(" .:-=+*#%@")

func (im *Image) renderASCII(widthCells int64) []string {
	srcBounds := im.img.Bounds()
	srcW := srcBounds.Dx()
	srcH := srcBounds.Dy()
	if srcW == 0 || srcH == 0 {
		return []string{""}
	}
	targetW := int(widthCells)
	if targetW < 1 {
		targetW = 1
	}
	aspect := float64(srcH) / float64(srcW)
	targetH := int(float64(targetW) * aspect / 2.0)
	if targetH < 1 {
		targetH = 1
	}
	if im.maxHeight > 0 && int64(targetH) > im.maxHeight {
		targetH = int(im.maxHeight)
	}
	resized := resizeNearest(im.img, targetW, targetH)

	var out []string
	for y := 0; y < targetH; y++ {
		var line strings.Builder
		for x := 0; x < targetW; x++ {
			c := nrgbaAt(resized, x, y)
			// Perceptual luma.
			l := (0.299*float64(c.R) + 0.587*float64(c.G) + 0.114*float64(c.B)) / 255.0
			idx := int(l * float64(len(asciiRamp)-1))
			if idx < 0 {
				idx = 0
			}
			if idx >= len(asciiRamp) {
				idx = len(asciiRamp) - 1
			}
			line.WriteRune(asciiRamp[idx])
		}
		out = append(out, line.String())
	}
	return out
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (im *Image) getPNG() ([]byte, error) {
	if len(im.rawPNG) > 0 {
		return im.rawPNG, nil
	}
	return encodePNG(im.img)
}

// cellHeightFromPixels estimates a row count that preserves the aspect ratio,
// assuming cells are twice as tall as they are wide.
func cellHeightFromPixels(srcW, srcH, widthCells, maxCells int) int {
	if srcW == 0 || widthCells == 0 {
		return 1
	}
	aspect := float64(srcH) / float64(srcW)
	cells := int(float64(widthCells) * aspect / 2.0)
	if cells < 1 {
		cells = 1
	}
	if maxCells > 0 && cells > maxCells {
		cells = maxCells
	}
	return cells
}

func nrgbaAt(img image.Image, x, y int) color.NRGBA {
	r, g, b, a := img.At(img.Bounds().Min.X+x, img.Bounds().Min.Y+y).RGBA()
	return color.NRGBA{
		R: uint8(r >> 8),
		G: uint8(g >> 8),
		B: uint8(b >> 8),
		A: uint8(a >> 8),
	}
}

// resizeNearest performs nearest-neighbour resampling into an *image.NRGBA.
func resizeNearest(src image.Image, w, h int) *image.NRGBA {
	bounds := src.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		sy := y * srcH / h
		for x := 0; x < w; x++ {
			sx := x * srcW / w
			r, g, b, a := src.At(bounds.Min.X+sx, bounds.Min.Y+sy).RGBA()
			dst.SetNRGBA(x, y, color.NRGBA{
				R: uint8(r >> 8),
				G: uint8(g >> 8),
				B: uint8(b >> 8),
				A: uint8(a >> 8),
			})
		}
	}
	return dst
}

// encodePNG writes `img` into the PNG format (used when we don't have the
// original bytes cached — e.g. constructed in-memory or decoded from JPEG).
func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ---------------------------------------------------------------------------
// Protocol detection
// ---------------------------------------------------------------------------

var imageProtocolCache atomic.Int64

// DetectImageProtocol inspects environment variables to pick the best
// image protocol. Result is cached per process.
func DetectImageProtocol() ImageProtocol {
	if v := imageProtocolCache.Load(); v != 0 {
		return ImageProtocol(v)
	}
	proto := detectImageProtocolUncached()
	imageProtocolCache.Store(int64(proto))
	return proto
}

// ResetImageProtocolDetection clears the cached detection (useful in tests).
func ResetImageProtocolDetection() { imageProtocolCache.Store(0) }

func detectImageProtocolUncached() ImageProtocol {
	get := os.Getenv

	if get("KITTY_WINDOW_ID") != "" {
		return ImageProtocolKitty
	}
	term := strings.ToLower(get("TERM"))
	if strings.Contains(term, "kitty") {
		return ImageProtocolKitty
	}
	if strings.Contains(term, "wezterm") {
		return ImageProtocolKitty
	}
	termProgram := strings.ToLower(get("TERM_PROGRAM"))
	if termProgram == "wezterm" {
		return ImageProtocolKitty
	}

	if termProgram == "iterm.app" || strings.ToLower(get("LC_TERMINAL")) == "iterm2" {
		return ImageProtocolITerm2
	}

	return ImageProtocolHalfBlock
}
