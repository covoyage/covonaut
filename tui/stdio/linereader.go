package stdio

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/covoyage/covonaut/tui/theme"
)

// LineReaderConfig configures LineReader (the simple "print a prompt, scan
// a line" helper used by the old procedural Chat API).
//
// This is a DIFFERENT concept from the phase-2 Input component defined in
// input.go — LineReader is a blocking helper; Input is a Focusable Component
// with Emacs-style editing.
type LineReaderConfig struct {
	Prompt     string
	HistoryMax int64
	Writer     io.Writer
	Reader     io.Reader
}

// LineReader provides line editing with history for blocking terminal input.
//
// Kept for backward compatibility with the procedural Chat API.
// For interactive TUI applications, use the Input component instead.
type LineReader struct {
	config  LineReaderConfig
	scanner *bufio.Scanner
	history []string

	mu sync.Mutex
}

// NewLineReader creates a LineReader with the given config.
func NewLineReader(cfg LineReaderConfig) *LineReader {
	if cfg.Prompt == "" {
		cfg.Prompt = "> "
	}
	if cfg.HistoryMax <= 0 {
		cfg.HistoryMax = 100
	}
	if cfg.Writer == nil {
		cfg.Writer = os.Stdout
	}
	if cfg.Reader == nil {
		cfg.Reader = os.Stdin
	}

	scanner := bufio.NewScanner(cfg.Reader)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	return &LineReader{
		config:  cfg,
		scanner: scanner,
	}
}

// ReadLine displays the prompt and reads one line of input.
// Returns ("", false) on EOF.
func (r *LineReader) ReadLine() (string, bool) {
	fmt.Fprint(r.config.Writer, r.config.Prompt)
	if !r.scanner.Scan() {
		return "", false
	}
	line := r.scanner.Text()
	trimmed := strings.TrimSpace(line)
	if trimmed != "" {
		r.addHistory(trimmed)
	}
	return trimmed, true
}

// ReadMultiLine reads input that may span multiple lines.
// A blank line or EOF terminates input. Returns the joined string.
func (r *LineReader) ReadMultiLine() (string, bool) {
	fmt.Fprint(r.config.Writer, r.config.Prompt)
	var lines []string
	first := true

	for r.scanner.Scan() {
		line := r.scanner.Text()
		if first {
			first = false
		} else if strings.TrimSpace(line) == "" {
			break
		}
		lines = append(lines, line)

		if !strings.HasSuffix(strings.TrimSpace(line), "\\") {
			break
		}
		trimmed := strings.TrimSuffix(strings.TrimSpace(line), "\\")
		lines[len(lines)-1] = trimmed
		fmt.Fprint(r.config.Writer, "... ")
	}

	if len(lines) == 0 && !r.scanner.Scan() {
		return "", false
	}

	result := strings.Join(lines, "\n")
	trimmed := strings.TrimSpace(result)
	if trimmed != "" {
		r.addHistory(trimmed)
	}
	return trimmed, true
}

// History returns a copy of the input history (newest last).
func (r *LineReader) History() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]string, len(r.history))
	copy(cp, r.history)
	return cp
}

// ClearHistory clears the input history.
func (r *LineReader) ClearHistory() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.history = nil
}

func (r *LineReader) addHistory(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.history) > 0 && r.history[len(r.history)-1] == line {
		return
	}
	r.history = append(r.history, line)
	if int64(len(r.history)) > r.config.HistoryMax {
		r.history = r.history[1:]
	}
}

// Confirm asks a yes/no question. Returns true for y/yes.
func Confirm(prompt string) bool {
	p := theme.CurrentPalette()
	fmt.Print(p.Bold.Render(prompt) + " " + p.Dim.Render("[y/N]") + " ")
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return false
	}
	answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
	return answer == "y" || answer == "yes"
}

// PromptSelect displays a numbered list of options and returns the selected
// index (or -1 if the selection is invalid or cancelled). Renamed from the
// former `Select` to avoid collision with the SelectList component.
func PromptSelect(prompt string, options []string) int64 {
	p := theme.CurrentPalette()
	fmt.Println(p.Bold.Render(prompt))
	for i, opt := range options {
		fmt.Printf("  %s %s\n",
			p.Dim.Render(fmt.Sprintf("[%d]", i+1)),
			opt,
		)
	}
	fmt.Print(p.Dim.Render("Choice: "))

	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return -1
	}
	in := strings.TrimSpace(scanner.Text())
	var idx int64
	if _, err := fmt.Sscanf(in, "%d", &idx); err != nil || idx < 1 || idx > int64(len(options)) {
		return -1
	}
	return idx - 1
}
