package stdio

import "strings"

// ---------------------------------------------------------------------------
// Box-drawing and layout helpers
//
// Moved from the theme package; these produce structured output strings,
// not style/color values.
// ---------------------------------------------------------------------------

// Box-drawing glyphs.
const (
	BoxHorizontal    = "─"
	BoxVertical      = "│"
	BoxTopLeft       = "╭"
	BoxTopRight      = "╮"
	BoxBottomLeft    = "╰"
	BoxBottomRight   = "╯"
	BoxVerticalLeft  = "┤"
	BoxVerticalRight = "├"
)

// HorizontalRule returns a horizontal rule of the given width.
func HorizontalRule(width int64) string {
	return strings.Repeat(BoxHorizontal, int(width))
}

// RenderBox draws a Unicode box around content with an optional title.
func RenderBox(title, content string, width int64) string {
	if width <= 4 {
		width = 60
	}
	innerW := int(width) - 4

	var b strings.Builder
	if title != "" {
		titleStr := " " + title + " "
		padding := innerW - len(titleStr)
		if padding < 0 {
			padding = 0
		}
		b.WriteString(BoxTopLeft + BoxHorizontal + titleStr + strings.Repeat(BoxHorizontal, padding) + BoxHorizontal + BoxTopRight + "\n")
	} else {
		b.WriteString(BoxTopLeft + strings.Repeat(BoxHorizontal, int(width)-2) + BoxTopRight + "\n")
	}

	lines := strings.Split(content, "\n")
	for _, line := range lines {
		padded := line
		if len(padded) > innerW {
			padded = padded[:innerW]
		}
		spaces := innerW - visibleLen(padded)
		if spaces < 0 {
			spaces = 0
		}
		b.WriteString(BoxVertical + " " + padded + strings.Repeat(" ", spaces) + " " + BoxVertical + "\n")
	}

	b.WriteString(BoxBottomLeft + strings.Repeat(BoxHorizontal, int(width)-2) + BoxBottomRight)
	return b.String()
}

// visibleLen counts visible (non-escape) characters in a string.
func visibleLen(s string) int {
	inEsc := false
	count := 0
	for _, r := range s {
		if r == '\033' {
			inEsc = true
			continue
		}
		if inEsc {
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				inEsc = false
			}
			continue
		}
		count++
	}
	return count
}
