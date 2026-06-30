package core

import "strings"

// ---------------------------------------------------------------------------
// SGR (Select Graphic Rendition) state machine.
//
// ParseSGR parses the parameter list of a CSI ... m sequence (the bytes
// between ESC [ and the final 'm'). The parser is permissive: empty
// parameters default to 0, ';' and ':' are both accepted as separators
// (the colon form is used by 38:2:R:G:B / 38:5:N, which we normalise to
// the semicolon form), and unknown codes are skipped.
//
// The parser supports every SGR code produced by the renderer and the
// theme system:
//
//	 0   reset
//	 1   bold
//	 2   dim / faint
//	 3   italic
//	 4   underline
//	 7   reverse
//	 8   hidden / concealed
//	 9   strikethrough
//	22   not bold / not dim
//	23   not italic
//	24   not underline
//	27   not reverse
//	28   not hidden
//	29   not strike
//	30-37   fg standard
//	38      fg 256 / truecolor (extended)
//	39      default fg
//	40-47   bg standard
//	48      bg 256 / truecolor (extended)
//	49      default bg
//	90-97   fg bright
//	100-107 bg bright
// ---------------------------------------------------------------------------

// ParseSGR parses the parameter list of an SGR sequence (without the
// leading "ESC [" or trailing "m") and applies the resulting style changes
// to base, returning the new style.
func ParseSGR(params string, base Style) Style {
	out := base
	if params == "" {
		// ESC[m is equivalent to ESC[0m.
		return DefaultStyle
	}
	// Normalise ':' to ';' so we can split on a single delimiter. The colon
	// form (ITU T.416) is semantically richer but the subset we accept
	// (38:5:n and 38:2:r:g:b) maps cleanly to the semicolon form.
	normalised := strings.ReplaceAll(params, ":", ";")
	parts := strings.Split(normalised, ";")
	nums := make([]int, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			nums = append(nums, 0)
			continue
		}
		n := atoiSafe(p)
		nums = append(nums, n)
	}
	applySGR(nums, &out)
	return out
}

func atoiSafe(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
		if n > 1_000_000 {
			return 0
		}
	}
	return n
}

func applySGR(nums []int, s *Style) {
	for i := 0; i < len(nums); {
		i = applyOneSGR(nums, i, s)
	}
}

// applyOneSGR applies one SGR code starting at index i and returns the
// index of the next code to process. Multi-parameter codes (38/48) consume
// their arguments.
func applyOneSGR(nums []int, i int, s *Style) int {
	code := nums[i]
	switch {
	case code == 0:
		*s = DefaultStyle
		return i + 1
	case code == 1:
		s.Attrs |= AttrBold
		return i + 1
	case code == 2:
		s.Attrs |= AttrDim
		return i + 1
	case code == 3:
		s.Attrs |= AttrItalic
		return i + 1
	case code == 4:
		s.Attrs |= AttrUnderline
		return i + 1
	case code == 7:
		s.Attrs |= AttrReverse
		return i + 1
	case code == 8:
		s.Attrs |= AttrHidden
		return i + 1
	case code == 9:
		s.Attrs |= AttrStrike
		return i + 1
	case code == 22:
		s.Attrs &^= AttrBold | AttrDim
		return i + 1
	case code == 23:
		s.Attrs &^= AttrItalic
		return i + 1
	case code == 24:
		s.Attrs &^= AttrUnderline
		return i + 1
	case code == 27:
		s.Attrs &^= AttrReverse
		return i + 1
	case code == 28:
		s.Attrs &^= AttrHidden
		return i + 1
	case code == 29:
		s.Attrs &^= AttrStrike
		return i + 1
	case code >= 30 && code <= 37:
		s.Fg = Palette(uint8(code - 30))
		return i + 1
	case code == 38:
		return applyExtended(nums, i, &s.Fg)
	case code == 39:
		s.Fg = ColorDefault
		return i + 1
	case code >= 40 && code <= 47:
		s.Bg = Palette(uint8(code - 40))
		return i + 1
	case code == 48:
		return applyExtended(nums, i, &s.Bg)
	case code == 49:
		s.Bg = ColorDefault
		return i + 1
	case code >= 90 && code <= 97:
		// Bright fg = palette index 8..15.
		s.Fg = Palette(uint8(code - 90 + 8))
		return i + 1
	case code >= 100 && code <= 107:
		// Bright bg = palette index 8..15.
		s.Bg = Palette(uint8(code - 100 + 8))
		return i + 1
	default:
		// Unknown code — skip.
		return i + 1
	}
}

// applyExtended handles 38;5;n (256-color) and 38;2;r;g;b (truecolor),
// returning the next index to process. Same form for bg via the 48 prefix.
func applyExtended(nums []int, i int, c *Color) int {
	// Need at least one more parameter (the sub-mode 5 or 2).
	if i+1 >= len(nums) {
		return i + 1
	}
	switch nums[i+1] {
	case 5:
		// 38;5;n — 256-color palette.
		if i+2 >= len(nums) {
			return i + 2
		}
		*c = Palette(uint8(nums[i+2]))
		return i + 3
	case 2:
		// 38;2;r;g;b — truecolor. The ITU T.416 colon form
		// (38:2:CS:r:g:b) carries a colorspace ID; when normalised to
		// semicolons it becomes 38;2;CS;r;g;b (6 params). Detect by
		// parameter count: if there are 4 values after the sub-mode, the
		// first is the colorspace ID and must be skipped.
		base := i + 2
		// 4 trailing values (base..base+3) ⇒ leading one is colorspace.
		if base+3 < len(nums) {
			base++
		}
		if base+2 >= len(nums) {
			return len(nums)
		}
		r, g, b := nums[base], nums[base+1], nums[base+2]
		// Clamp to [0,255].
		if r > 255 {
			r = 255
		}
		if g > 255 {
			g = 255
		}
		if b > 255 {
			b = 255
		}
		*c = RGB(uint8(r), uint8(g), uint8(b))
		return base + 3
	default:
		// Unknown sub-mode; skip the 38 and let the caller continue.
		return i + 1
	}
}

// RenderSGR returns the SGR escape sequence that transitions from `from` to
// `to`. Returns "" if no transition is needed (the styles are equal).
//
// The optimisation strategy is conservative: if any colour channel changes,
// we re-emit the full colour spec (cheap and correct). Attribute changes
// emit only the affected bits (on or off).
func RenderSGR(from, to Style) string {
	if from.Equal(to) {
		return ""
	}
	if to.Equal(DefaultStyle) {
		return "\x1b[0m"
	}
	var b strings.Builder
	b.WriteString("\x1b[")
	first := true
	write := func(s string) {
		if !first {
			b.WriteByte(';')
		}
		b.WriteString(s)
		first = false
	}

	// Reset all then re-emit if reset is implied. We avoid a full reset
	// unless the target has default everything but the source had non-default
	// on some channel — simpler to be conservative: if attrs go from non-empty
	// to empty, or colour goes from set to default, do a full reset + rebuild.
	if needsReset(from, to) {
		b.WriteString("0")
		first = false
		// Re-emit everything.
		emitFullSGR(&b, to, &first)
		b.WriteByte('m')
		return b.String()
	}

	// Attr diffs.
	attrOn, attrOff := attrDiff(from.Attrs, to.Attrs)
	if attrOff != 0 {
		write(attrOffCode(attrOff))
	}
	if attrOn != 0 {
		write(attrOnCode(attrOn))
	}

	// Fg diff.
	if from.Fg != to.Fg {
		write(fgCode(to.Fg))
	}
	// Bg diff.
	if from.Bg != to.Bg {
		write(bgCode(to.Bg))
	}

	b.WriteByte('m')
	return b.String()
}

// needsReset reports whether a full "0" reset is the simplest transition.
// This is true when multiple channels go from non-default to default, or
// when the source has reverse video set (clearing reverse via 27 is fine
// but a reset is clearer).
func needsReset(from, to Style) bool {
	// If target is the default and source isn't, always full reset.
	if to.Equal(DefaultStyle) {
		return true
	}
	// Count channels going from set -> default.
	fgSetToDefault := from.Fg != ColorDefault && to.Fg == ColorDefault
	bgSetToDefault := from.Bg != ColorDefault && to.Bg == ColorDefault
	attrsCleared := from.Attrs != 0 && to.Attrs == 0
	count := 0
	if fgSetToDefault {
		count++
	}
	if bgSetToDefault {
		count++
	}
	if attrsCleared {
		count++
	}
	return count >= 2
}

func emitFullSGR(b *strings.Builder, s Style, first *bool) {
	emit := func(str string) {
		if !*first {
			b.WriteByte(';')
		}
		b.WriteString(str)
		*first = false
	}
	if s.Attrs&AttrBold != 0 {
		emit("1")
	}
	if s.Attrs&AttrDim != 0 {
		emit("2")
	}
	if s.Attrs&AttrItalic != 0 {
		emit("3")
	}
	if s.Attrs&AttrUnderline != 0 {
		emit("4")
	}
	if s.Attrs&AttrReverse != 0 {
		emit("7")
	}
	if s.Attrs&AttrHidden != 0 {
		emit("8")
	}
	if s.Attrs&AttrStrike != 0 {
		emit("9")
	}
	if !s.Fg.IsDefault() {
		emit(fgCode(s.Fg))
	}
	if !s.Bg.IsDefault() {
		emit(bgCode(s.Bg))
	}
}

func attrDiff(from, to Attrs) (on, off Attrs) {
	return to &^ from, from &^ to
}

func attrOnCode(a Attrs) string {
	var parts []string
	if a&AttrBold != 0 {
		parts = append(parts, "1")
	}
	if a&AttrDim != 0 {
		parts = append(parts, "2")
	}
	if a&AttrItalic != 0 {
		parts = append(parts, "3")
	}
	if a&AttrUnderline != 0 {
		parts = append(parts, "4")
	}
	if a&AttrReverse != 0 {
		parts = append(parts, "7")
	}
	if a&AttrHidden != 0 {
		parts = append(parts, "8")
	}
	if a&AttrStrike != 0 {
		parts = append(parts, "9")
	}
	return strings.Join(parts, ";")
}

func attrOffCode(a Attrs) string {
	var parts []string
	if a&AttrBold != 0 || a&AttrDim != 0 {
		parts = append(parts, "22")
	}
	if a&AttrItalic != 0 {
		parts = append(parts, "23")
	}
	if a&AttrUnderline != 0 {
		parts = append(parts, "24")
	}
	if a&AttrReverse != 0 {
		parts = append(parts, "27")
	}
	if a&AttrHidden != 0 {
		parts = append(parts, "28")
	}
	if a&AttrStrike != 0 {
		parts = append(parts, "29")
	}
	return strings.Join(parts, ";")
}

func fgCode(c Color) string {
	if c.IsDefault() {
		return "39"
	}
	if c.IsPalette() {
		return "38;5;" + itoa(int(c.PaletteIndex()))
	}
	r, g, b := c.RGBComponents()
	return "38;2;" + itoa(int(r)) + ";" + itoa(int(g)) + ";" + itoa(int(b))
}

func bgCode(c Color) string {
	if c.IsDefault() {
		return "49"
	}
	if c.IsPalette() {
		return "48;5;" + itoa(int(c.PaletteIndex()))
	}
	r, g, b := c.RGBComponents()
	return "48;2;" + itoa(int(r)) + ";" + itoa(int(g)) + ";" + itoa(int(b))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
