package theme

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ColorMode selects how hex colors are encoded for the terminal.
type ColorMode int64

const (
	// ColorModeTruecolor emits 38;2;r;g;b / 48;2;r;g;b when COLORTERM etc.
	// indicates 24-bit support.
	ColorModeTruecolor ColorMode = iota + 1
	// ColorMode256 maps hex to the nearest xterm 256-color index.
	ColorMode256
)

// DetectColorMode mirrors common heuristics (similar to pi-mono coding-agent).
func DetectColorMode() ColorMode {
	if os.Getenv("COLORTERM") == "truecolor" || os.Getenv("COLORTERM") == "24bit" {
		return ColorModeTruecolor
	}
	if os.Getenv("WT_SESSION") != "" {
		return ColorModeTruecolor
	}
	term := os.Getenv("TERM")
	if term == "dumb" || term == "" || term == "linux" {
		return ColorMode256
	}
	if os.Getenv("TERM_PROGRAM") == "Apple_Terminal" {
		return ColorMode256
	}
	if term == "screen" || strings.HasPrefix(term, "screen-") || strings.HasPrefix(term, "screen.") {
		return ColorMode256
	}
	return ColorModeTruecolor
}

// DetectTerminalBackground returns "dark" or "light" from COLORFGBG when possible.
func DetectTerminalBackground() string {
	fgbg := os.Getenv("COLORFGBG")
	if fgbg == "" {
		return "dark"
	}
	parts := strings.Split(fgbg, ";")
	if len(parts) < 2 {
		return "dark"
	}
	bg, err := strconv.Atoi(parts[1])
	if err != nil {
		return "dark"
	}
	if bg < 8 {
		return "dark"
	}
	return "light"
}

func hexToRGB(hex string) (r, g, b int64, ok bool) {
	h := strings.TrimSpace(strings.TrimPrefix(strings.ToLower(hex), "#"))
	if len(h) != 6 {
		return 0, 0, 0, false
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	n := int64(v)
	return (n >> 16) & 0xff, (n >> 8) & 0xff, n & 0xff, true
}

var cubeValues = []int64{0, 95, 135, 175, 215, 255}

func findClosestCubeIndex(value int64) int64 {
	var minIdx int64
	var minDist int64 = 1 << 62
	for i := int64(0); i < int64(len(cubeValues)); i++ {
		d := value - cubeValues[i]
		if d < 0 {
			d = -d
		}
		if d < minDist {
			minDist = d
			minIdx = i
		}
	}
	return minIdx
}

var grayRamp []int64

func init() {
	grayRamp = make([]int64, 24)
	for i := int64(0); i < 24; i++ {
		grayRamp[i] = 8 + i*10
	}
}

func colorDistance(r1, g1, b1, r2, g2, b2 int64) float64 {
	dr := float64(r1 - r2)
	dg := float64(g1 - g2)
	db := float64(b1 - b2)
	return dr*dr*0.299 + dg*dg*0.587 + db*db*0.114
}

func findClosestGrayIndex(gray int64) int64 {
	var minIdx int64
	var minDist float64 = 1e18
	for i := int64(0); i < int64(len(grayRamp)); i++ {
		gv := grayRamp[i]
		d := colorDistance(gray, gray, gray, gv, gv, gv)
		if d < minDist {
			minDist = d
			minIdx = i
		}
	}
	return minIdx
}

// RGBTo256 maps sRGB components to an xterm 256 palette index (16–231 cube or 232–255 gray).
func RGBTo256(r, g, b int64) int64 {
	rIdx := findClosestCubeIndex(r)
	gIdx := findClosestCubeIndex(g)
	bIdx := findClosestCubeIndex(b)
	cubeR := cubeValues[rIdx]
	cubeG := cubeValues[gIdx]
	cubeB := cubeValues[bIdx]
	cubeIndex := int64(16 + 36*rIdx + 6*gIdx + bIdx)
	cubeDist := colorDistance(r, g, b, cubeR, cubeG, cubeB)

	gray := int64(0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b))
	grayIdx := findClosestGrayIndex(gray)
	grayValue := grayRamp[grayIdx]
	grayIndex := int64(232 + grayIdx)
	grayDist := colorDistance(r, g, b, grayValue, grayValue, grayValue)

	maxC := r
	minC := r
	if g > maxC {
		maxC = g
	}
	if b > maxC {
		maxC = b
	}
	if g < minC {
		minC = g
	}
	if b < minC {
		minC = b
	}
	spread := maxC - minC
	if spread < 10 && grayDist < cubeDist {
		return grayIndex
	}
	return cubeIndex
}

// FgParams returns the CSI SGR parameter segment for a foreground color value.
// Empty string means default foreground. Values: "#rrggbb" or decimal index 0–255.
func FgParams(value string, mode ColorMode) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if n, err := strconv.ParseInt(value, 10, 32); err == nil && n >= 0 && n <= 255 {
		return fmt.Sprintf("38;5;%d", n)
	}
	r, g, b, ok := hexToRGB(value)
	if !ok {
		return ""
	}
	if mode == ColorModeTruecolor {
		return fmt.Sprintf("38;2;%d;%d;%d", r, g, b)
	}
	idx := RGBTo256(r, g, b)
	return fmt.Sprintf("38;5;%d", idx)
}

// BgParams returns CSI parameters for a background (48;…).
func BgParams(value string, mode ColorMode) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if n, err := strconv.ParseInt(value, 10, 32); err == nil && n >= 0 && n <= 255 {
		return fmt.Sprintf("48;5;%d", n)
	}
	r, g, b, ok := hexToRGB(value)
	if !ok {
		return ""
	}
	if mode == ColorModeTruecolor {
		return fmt.Sprintf("48;2;%d;%d;%d", r, g, b)
	}
	idx := RGBTo256(r, g, b)
	return fmt.Sprintf("48;5;%d", idx)
}
