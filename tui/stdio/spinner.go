package stdio

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	core "github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/terminal"
	"github.com/covoyage/covonaut/tui/theme"
)

// Spinner displays an animated progress indicator in the terminal.
type Spinner struct {
	style   core.SpinnerStyle
	message string
	color   theme.Style
	writer  io.Writer

	mu      sync.Mutex
	running bool
	stopCh  chan struct{}
	doneCh  chan struct{}
}

func NewSpinner(style core.SpinnerStyle) *Spinner {
	return &Spinner{
		style:  style,
		writer: os.Stdout,
		color:  theme.CurrentPalette().LoaderSpinner,
	}
}

func (s *Spinner) SetMessage(msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.message = msg
}

func (s *Spinner) SetColor(c theme.Style) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.color = c
}

func (s *Spinner) SetWriter(w io.Writer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writer = w
}

func (s *Spinner) Start(message string) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.message = message
	s.running = true
	s.stopCh = make(chan struct{})
	s.doneCh = make(chan struct{})
	s.mu.Unlock()

	go s.animate()
}

func (s *Spinner) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	close(s.stopCh)
	s.mu.Unlock()
	<-s.doneCh

	fmt.Fprint(s.writer, "\r"+terminal.ClearLine())
}

func (s *Spinner) StopWith(finalMessage string) {
	s.Stop()
	fmt.Fprintln(s.writer, finalMessage)
}

func (s *Spinner) StopSuccess(msg string) {
	s.Stop()
	pal := theme.CurrentPalette()
	fmt.Fprintln(s.writer, pal.Success.Render(theme.SymbolCheck)+" "+msg)
}

func (s *Spinner) StopFail(msg string) {
	s.Stop()
	pal := theme.CurrentPalette()
	fmt.Fprintln(s.writer, pal.Error.Render(theme.SymbolCross)+" "+msg)
}

func (s *Spinner) animate() {
	defer close(s.doneCh)

	idx := 0
	ticker := time.NewTicker(s.style.Interval)
	defer ticker.Stop()

	fmt.Fprint(s.writer, terminal.HideCursor())
	defer fmt.Fprint(s.writer, terminal.ShowCursor())

	for {
		s.mu.Lock()
		frame := s.color.Render(s.style.Frames[idx%len(s.style.Frames)])
		msg := s.message
		s.mu.Unlock()

		fmt.Fprintf(s.writer, "\r%s %s %s", terminal.ClearLine(), frame, msg)
		idx++

		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
		}
	}
}

// WithSpinner runs fn while showing a spinner. Returns fn's result.
func WithSpinner[T any](message string, fn func() (T, error)) (T, error) {
	sp := NewSpinner(core.SpinnerDots)
	sp.Start(message)
	result, err := fn()
	if err != nil {
		sp.StopFail(err.Error())
	} else {
		sp.StopSuccess(message)
	}
	return result, err
}
