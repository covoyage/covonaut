package component

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/theme"
)

type StatusBarSection struct {
	Text string
	Fn   func(string) string
}

type StatusBar struct {
	mu      sync.RWMutex
	sections []StatusBarSection
	elapsed  time.Duration
	running  bool
	start    time.Time
	mode     string
	agent    string
	width    int64
}

func NewStatusBar() *StatusBar {
	return &StatusBar{}
}

func (s *StatusBar) SetMode(mode string) {
	s.mu.Lock()
	s.mode = mode
	s.mu.Unlock()
}

func (s *StatusBar) SetAgent(agent string) {
	s.mu.Lock()
	s.agent = agent
	s.mu.Unlock()
}

func (s *StatusBar) SetSections(sections []StatusBarSection) {
	s.mu.Lock()
	s.sections = sections
	s.mu.Unlock()
}

func (s *StatusBar) Busy() {
	s.mu.Lock()
	s.running = true
	s.start = time.Now()
	s.mu.Unlock()
}

func (s *StatusBar) Idle() {
	s.mu.Lock()
	if s.running {
		s.elapsed = time.Since(s.start)
	}
	s.running = false
	s.mu.Unlock()
}

func (s *StatusBar) Render(width int64) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	p := theme.CurrentPalette()
	var left, right strings.Builder

	if s.mode != "" {
		left.WriteString(p.Accent.Render(" " + s.mode + " "))
		left.WriteString(" ")
	}

	if s.running {
		elapsed := time.Since(s.start)
		left.WriteString(p.LoaderSpinner.Render(theme.SymbolThinking + " " + formatDuration(elapsed)))
	} else if s.agent != "" {
		left.WriteString(p.Dim.Render(theme.SymbolCheck + " " + s.agent))
	}

	for _, sec := range s.sections {
		text := sec.Text
		if sec.Fn != nil {
			text = sec.Fn(text)
		}
		right.WriteString(" ")
		right.WriteString(text)
	}

	leftStr := left.String()
	rightStr := right.String()

	leftW := core.VisibleWidth(leftStr)
	rightW := core.VisibleWidth(rightStr)
	gap := width - leftW - rightW
	if gap < 1 {
		gap = 1
	}

	line := leftStr + strings.Repeat(" ", int(gap)) + rightStr
	if core.VisibleWidth(line) > width {
		line = core.TruncateToWidth(line, width, "…")
	}
	line = core.PadToWidth(line, width)

	bg := p.BorderMuted
	line = bg.Render(line)

	return []string{line}
}

func (s *StatusBar) Invalidate() {}

func (s *StatusBar) Update(msg core.Msg) core.Cmd {
	switch msg.(type) {
	case core.WindowSizeMsg:
		s.Invalidate()
	}
	return nil
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second
	if m > 0 {
		return fmt.Sprintf("%dm%ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
