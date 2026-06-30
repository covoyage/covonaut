// Command tui-demo exercises the phase-1 TUI scaffold: raw-mode input,
// differential rendering, focus stack, CSI 2026 synchronized output,
// bracketed paste, SIGWINCH, and keybinding resolution.
//
// Controls (within the demo):
//   - Type any printable character        append to buffer
//   - Backspace                           delete last char
//   - Enter                               commit line into history
//   - Up / Down                           walk cursor across history
//   - Ctrl+L                              clear history
//   - Ctrl+C or Esc                       quit
//
// The header line renders a running tick + terminal size so you can verify
// differential rendering and SIGWINCH events are flowing.
package main

import (
	"fmt"
	"sync"
	"time"

	core "github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/terminal"
	"github.com/covoyage/covonaut/tui"
)

// ---------------------------------------------------------------------------
// Header: shows "tui-demo  | 120x40 | tick=42" so we can visually confirm
// the render loop only rewrites changed lines.
// ---------------------------------------------------------------------------

type header struct {
	app  *tui.TUI
	tick int64
	mu   sync.Mutex
}

func newHeader(app *tui.TUI) *header {
	h := &header{app: app}
	go h.loop()
	return h
}

func (h *header) loop() {
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-h.app.Done():
			return
		case <-t.C:
			h.mu.Lock()
			h.tick++
			h.mu.Unlock()
			h.app.RequestRender()
		}
	}
}

func (h *header) Render(width int64) []string {
	cols, rows := h.app.Terminal().Size()
	h.mu.Lock()
	tick := h.tick
	h.mu.Unlock()
	line := fmt.Sprintf("tui-demo  | %dx%d | tick=%d", cols, rows, tick)
	return []string{core.PadToWidth(line, width), core.PadToWidth("", width)}
}

func (h *header) HandleInput(string) {}
func (h *header) Invalidate()        {}

// ---------------------------------------------------------------------------
// History: rendered above the input, shows recent submitted lines.
// ---------------------------------------------------------------------------

type history struct {
	mu    sync.Mutex
	lines []string
}

func (h *history) add(line string) {
	h.mu.Lock()
	h.lines = append(h.lines, line)
	if len(h.lines) > 12 {
		h.lines = h.lines[len(h.lines)-12:]
	}
	h.mu.Unlock()
}

func (h *history) clear() {
	h.mu.Lock()
	h.lines = nil
	h.mu.Unlock()
}

func (h *history) Render(width int64) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.lines) == 0 {
		return []string{core.PadToWidth("(no messages yet — try typing below)", width)}
	}
	out := make([]string, 0, len(h.lines))
	for i, l := range h.lines {
		out = append(out, core.PadToWidth(fmt.Sprintf("%2d  %s", i+1, l), width))
	}
	return out
}

func (h *history) HandleInput(string) {}
func (h *history) Invalidate()        {}

// ---------------------------------------------------------------------------
// MiniInput: a rudimentary single-line input component implementing Focusable.
// It shows how CURSOR_MARKER places the hardware cursor for IME support.
// ---------------------------------------------------------------------------

type miniInput struct {
	app     *tui.TUI
	hist    *history
	mu      sync.Mutex
	value   []rune
	focused bool
	quitOn  func()
}

func (m *miniInput) SetFocused(on bool) { m.mu.Lock(); m.focused = on; m.mu.Unlock() }
func (m *miniInput) IsFocused() bool    { m.mu.Lock(); defer m.mu.Unlock(); return m.focused }

func (m *miniInput) Render(width int64) []string {
	m.mu.Lock()
	val := string(m.value)
	focused := m.focused
	m.mu.Unlock()

	prompt := "> "
	display := prompt + val
	if focused {
		display += core.CURSOR_MARKER
	}
	display = core.PadToWidth(display, width)

	helpLine := core.PadToWidth("[Enter] submit   [Up/Down] walk   [Ctrl+L] clear   [Ctrl+C] quit", width)

	return []string{
		core.PadToWidth("────────────────────────────────────────────────", width),
		display,
		helpLine,
	}
}

func (m *miniInput) HandleInput(data string) {
	km := m.app.Keybindings()
	switch {
	case terminal.MatchesKey(data, "ctrl+c"), terminal.MatchesKey(data, "escape"):
		if m.quitOn != nil {
			m.quitOn()
		}
		return
	case km.Matches(data, "tui.input.submit"):
		m.mu.Lock()
		v := string(m.value)
		m.value = nil
		m.mu.Unlock()
		if v != "" {
			m.hist.add(v)
		}
		return
	case km.Matches(data, "tui.editor.deleteCharBackward"):
		m.mu.Lock()
		if len(m.value) > 0 {
			m.value = m.value[:len(m.value)-1]
		}
		m.mu.Unlock()
		return
	case terminal.MatchesKey(data, "ctrl+l"):
		m.hist.clear()
		return
	}

	for _, k := range terminal.ParseKeys(data) {
		if k.IsPrintable() {
			m.mu.Lock()
			m.value = append(m.value, k.Rune)
			m.mu.Unlock()
		}
	}
}

func (m *miniInput) Invalidate() {}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func main() {
	term := terminal.NewProcessTerminal()
	app := tui.NewTUI(term)

	hist := &history{}
	input := &miniInput{app: app, hist: hist}
	input.quitOn = func() { app.Quit() }

	app.AddChild(newHeader(app))
	app.AddChild(hist)
	app.AddChild(input)
	app.Focus(input)

	if err := app.Start(); err != nil {
		fmt.Println("start tui:", err)
		return
	}
	defer app.Stop()

	<-app.Done()
	fmt.Println("\n(tui-demo exited cleanly)")
}
