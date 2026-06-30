package stdio

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/covoyage/covonaut/tui/terminal"
	"github.com/covoyage/covonaut/tui/theme"
)

// Renderer handles streaming text output with awareness of markdown code blocks,
// tool calls, and other structured content.
type Renderer struct {
	writer io.Writer

	mu          sync.Mutex
	inCodeBlock bool
	codeLang    string
	lineBuffer  string
	totalChars  int64
}

func NewRenderer() *Renderer {
	return &Renderer{writer: os.Stdout}
}

func (r *Renderer) SetWriter(w io.Writer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.writer = w
}

// WriteChunk processes an incremental text chunk (streaming delta).
// It detects markdown code fences and applies syntax-aware styling.
func (r *Renderer) WriteChunk(chunk string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, ch := range chunk {
		r.totalChars++
		r.lineBuffer += string(ch)

		if ch == '\n' {
			r.flushLine()
			continue
		}
	}
}

// Flush writes any remaining buffered content.
func (r *Renderer) Flush() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.lineBuffer != "" {
		r.flushLine()
	}
}

// Reset clears the renderer state for a new message.
func (r *Renderer) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.inCodeBlock = false
	r.codeLang = ""
	r.lineBuffer = ""
	r.totalChars = 0
}

func (r *Renderer) flushLine() {
	pal := theme.CurrentPalette()
	line := r.lineBuffer
	r.lineBuffer = ""

	trimmed := strings.TrimSpace(strings.TrimRight(line, "\n"))

	if strings.HasPrefix(trimmed, "```") {
		if r.inCodeBlock {
			fmt.Fprint(r.writer, pal.CodeBlock.Render(trimmed)+"\n")
			r.inCodeBlock = false
			r.codeLang = ""
		} else {
			r.inCodeBlock = true
			r.codeLang = strings.TrimPrefix(trimmed, "```")
			label := trimmed
			if r.codeLang != "" {
				label = pal.CodeBlock.Render("```") + pal.Dim.Render(r.codeLang)
			} else {
				label = pal.CodeBlock.Render(trimmed)
			}
			fmt.Fprint(r.writer, label+"\n")
		}
		return
	}

	if r.inCodeBlock {
		fmt.Fprint(r.writer, pal.Code.Render(line))
		return
	}

	styled := r.styleMarkdownLine(line)
	fmt.Fprint(r.writer, styled)
}

func (r *Renderer) styleMarkdownLine(line string) string {
	pal := theme.CurrentPalette()
	trimmed := strings.TrimSpace(line)

	if strings.HasPrefix(trimmed, "# ") {
		return pal.Bold.Render(trimmed) + "\n"
	}
	if strings.HasPrefix(trimmed, "## ") {
		return pal.Bold.Render(trimmed) + "\n"
	}
	if strings.HasPrefix(trimmed, "### ") {
		return pal.Bold.Render(trimmed) + "\n"
	}

	if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
		return "  " + theme.SymbolBullet + " " + strings.TrimSpace(trimmed[2:]) + "\n"
	}

	result := line
	result = styleInlineCode(result)
	result = styleInlineBold(result)
	return result
}

func styleInlineCode(s string) string {
	if !theme.ColorEnabled() {
		return s
	}
	parts := strings.Split(s, "`")
	if len(parts) < 3 {
		return s
	}
	var b strings.Builder
	inCode := false
	for i, p := range parts {
		if i > 0 {
			if inCode {
				b.WriteString(terminal.Reset + "`")
			} else {
				b.WriteString("`" + terminal.Esc + "32m")
			}
			inCode = !inCode
		}
		b.WriteString(p)
	}
	if inCode {
		b.WriteString(terminal.Reset)
	}
	return b.String()
}

func styleInlineBold(s string) string {
	if !theme.ColorEnabled() {
		return s
	}
	parts := strings.Split(s, "**")
	if len(parts) < 3 {
		return s
	}
	var b strings.Builder
	inBold := false
	for i, p := range parts {
		if i > 0 {
			if inBold {
				b.WriteString(terminal.Reset)
			} else {
				b.WriteString(terminal.Esc + "1m")
			}
			inBold = !inBold
		}
		b.WriteString(p)
	}
	if inBold {
		b.WriteString(terminal.Reset)
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Formatted output helpers
// ---------------------------------------------------------------------------

// ToolStatus prints a tool call status line.
func ToolStatus(name, status string, s theme.Style) string {
	pal := theme.CurrentPalette()
	return fmt.Sprintf("  %s %s %s",
		pal.ToolName.Render(name),
		pal.Dim.Render(theme.SymbolArrow),
		s.Render(status),
	)
}

// HandoffStatus prints a handoff status line.
func HandoffStatus(source, target, mode string) string {
	pal := theme.CurrentPalette()
	return fmt.Sprintf("  %s %s %s %s",
		pal.Handoff.Render(theme.SymbolArrow+theme.SymbolArrow),
		pal.Dim.Render(mode),
		pal.Dim.Render(source),
		pal.Handoff.Render(target),
	)
}

// UsageSummary formats token usage as a styled string.
func UsageSummary(prompt, completion, total int64) string {
	return theme.CurrentPalette().Usage.Render(fmt.Sprintf(
		"[token usage: %d prompt tokens, %d completion tokens, %d total tokens]",
		prompt, completion, total,
	))
}

// ErrorMessage formats an error for display.
func ErrorMessage(err error) string {
	pal := theme.CurrentPalette()
	return pal.Error.Render(theme.SymbolCross+" Error: ") + err.Error()
}

// Divider returns a horizontal divider line.
func Divider(width int64) string {
	return theme.CurrentPalette().Dim.Render(HorizontalRule(width))
}
