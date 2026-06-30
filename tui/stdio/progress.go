package stdio

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covonaut/tui/theme"
)

// ProgressBar renders a terminal progress bar.
type ProgressBar struct {
	total   int64
	current int64
	width   int64
	label   string
	writer  io.Writer
	style   theme.Style

	mu      sync.Mutex
	started time.Time
}

func NewProgressBar(total, width int64) *ProgressBar {
	if width <= 0 {
		width = 40
	}
	return &ProgressBar{
		total:   total,
		width:   width,
		writer:  os.Stdout,
		style:   theme.CurrentPalette().LoaderSpinner,
		started: time.Now(),
	}
}

func (p *ProgressBar) SetLabel(label string) { p.mu.Lock(); p.label = label; p.mu.Unlock() }
func (p *ProgressBar) SetStyle(s theme.Style) { p.mu.Lock(); p.style = s; p.mu.Unlock() }
func (p *ProgressBar) SetWriter(w io.Writer)  { p.mu.Lock(); p.writer = w; p.mu.Unlock() }

// Set updates the current progress value and re-renders.
func (p *ProgressBar) Set(value int64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.current = value
	if p.current > p.total {
		p.current = p.total
	}
	p.render()
}

// Increment adds delta to the current progress.
func (p *ProgressBar) Increment(delta int64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.current += delta
	if p.current > p.total {
		p.current = p.total
	}
	p.render()
}

// Done renders the final state and moves to a new line.
func (p *ProgressBar) Done() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.current = p.total
	p.render()
	fmt.Fprintln(p.writer)
}

func (p *ProgressBar) render() {
	if p.total <= 0 {
		return
	}

	pal := theme.CurrentPalette()

	ratio := float64(p.current) / float64(p.total)
	filled := int(ratio * float64(p.width))
	if filled > int(p.width) {
		filled = int(p.width)
	}
	empty := int(p.width) - filled

	bar := p.style.Render(strings.Repeat("█", filled)) +
		pal.Dim.Render(strings.Repeat("░", empty))

	pct := int(ratio * 100)
	elapsed := time.Since(p.started).Round(time.Millisecond)

	label := ""
	if p.label != "" {
		label = p.label + " "
	}

	fmt.Fprintf(p.writer, "\r%s%s %s %s",
		label,
		bar,
		pal.Bold.Render(fmt.Sprintf("%3d%%", pct)),
		pal.Dim.Render(fmt.Sprintf("(%d/%d, %s)", p.current, p.total, elapsed)),
	)
}

// ---------------------------------------------------------------------------
// Token usage display
// ---------------------------------------------------------------------------

// TokenUsageDisplay renders a styled token usage summary.
type TokenUsageDisplay struct {
	writer io.Writer
	style  theme.Style
}

func NewTokenUsageDisplay() *TokenUsageDisplay {
	return &TokenUsageDisplay{
		writer: os.Stdout,
		style:  theme.CurrentPalette().Usage,
	}
}

func (d *TokenUsageDisplay) SetWriter(w io.Writer) { d.writer = w }

// Render displays the token usage in a compact format.
func (d *TokenUsageDisplay) Render(prompt, completion, total int64) {
	fmt.Fprintln(d.writer, d.style.Render(
		fmt.Sprintf("[token usage: prompt=%d tokens, completion=%d tokens, total=%d tokens]", prompt, completion, total),
	))
}

// RenderDetailed displays token usage in a detailed box format.
func (d *TokenUsageDisplay) RenderDetailed(prompt, completion, total int64, model string, dur time.Duration) {
	pal := theme.CurrentPalette()
	maxWidth := int64(50)
	maxBar := maxWidth - 2
	promptRatio := float64(prompt) / float64(total)
	completionRatio := float64(completion) / float64(total)

	promptBar := int(promptRatio * float64(maxBar))
	completionBar := int(completionRatio * float64(maxBar))
	if promptBar+completionBar > int(maxBar) {
		completionBar = int(maxBar) - promptBar
	}

	var lines []string
	if model != "" {
		lines = append(lines, fmt.Sprintf("Model: %s", model))
	}
	lines = append(lines,
		fmt.Sprintf("Prompt:     %6d tokens", prompt),
		fmt.Sprintf("Completion: %6d tokens", completion),
		fmt.Sprintf("Total:      %6d tokens", total),
		"",
		pal.ProgressPrompt.Render(strings.Repeat("▓", promptBar))+
			pal.ProgressCompletion.Render(strings.Repeat("▓", completionBar))+
			pal.Dim.Render(strings.Repeat("░", int(maxBar)-promptBar-completionBar)),
		pal.Dim.Render(fmt.Sprintf("%s prompt  %s completion",
			pal.ProgressPrompt.Render("▓"),
			pal.ProgressCompletion.Render("▓"),
		)),
	)
	if dur > 0 {
		tokPerSec := float64(completion) / dur.Seconds()
		lines = append(lines, fmt.Sprintf("Speed: %.1f tok/s (%s)", tokPerSec, dur.Round(time.Millisecond)))
	}

	fmt.Fprintln(d.writer, RenderBox("Token Usage", strings.Join(lines, "\n"), maxWidth+4))
}

// ---------------------------------------------------------------------------
// Timer
// ---------------------------------------------------------------------------

// Timer tracks and displays elapsed time.
type Timer struct {
	start time.Time
	label string
}

func NewTimer(label string) *Timer {
	return &Timer{start: time.Now(), label: label}
}

func (t *Timer) Elapsed() time.Duration {
	return time.Since(t.start)
}

func (t *Timer) String() string {
	return theme.CurrentPalette().Dim.Render(fmt.Sprintf("%s: %s", t.label, t.Elapsed().Round(time.Millisecond)))
}

func (t *Timer) Print() {
	fmt.Println(t.String())
}
