package component

import (
	"context"
	"sync"
	"time"

	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/terminal"
	"github.com/covoyage/covonaut/tui/theme"
)

// ---------------------------------------------------------------------------
// Loader — animated spinner rendered as a Component.
//
// Unlike the legacy Spinner (which drives stdout directly), Loader is a
// proper Component: call Start() and it continuously asks the TUI to redraw
// the spinner frame. Rendering itself happens on the TUI goroutine.
// ---------------------------------------------------------------------------

// LoaderTheme overrides spinner & message styling.
type LoaderTheme struct {
	SpinnerFn func(string) string
	MessageFn func(string) string
}

// Loader displays a spinning indicator with an adjacent message.
type Loader struct {
	mu              sync.RWMutex
	onRequestRender func() // callback to request a TUI render; nil-safe
	style           core.SpinnerStyle
	theme           LoaderTheme
	message         string
	frame           int64
	running         bool
	stopCh          chan struct{}
	doneCh          chan struct{}
}

// NewLoader creates a Loader that requests renders via the provided callback.
// Pass nil for tests; the frame will not auto-advance.
func NewLoader(onRequestRender func(), message string) *Loader {
	if onRequestRender == nil {
		onRequestRender = func() {}
	}
	return &Loader{
		onRequestRender: onRequestRender,
		style:           core.SpinnerDots,
		message:         message,
	}
}

// SetStyle changes the spinner animation.
func (l *Loader) SetStyle(s core.SpinnerStyle) {
	l.mu.Lock()
	l.style = s
	l.mu.Unlock()
}

// SetTheme customises how the spinner and message are coloured.
func (l *Loader) SetTheme(t LoaderTheme) {
	l.mu.Lock()
	l.theme = t
	l.mu.Unlock()
}

// SetMessage updates the message shown next to the spinner.
func (l *Loader) SetMessage(msg string) {
	l.mu.Lock()
	l.message = msg
	l.mu.Unlock()
	l.onRequestRender()
}

// Start begins the animation (no-op if already running).
func (l *Loader) Start() {
	l.mu.Lock()
	if l.running {
		l.mu.Unlock()
		return
	}
	l.running = true
	l.stopCh = make(chan struct{})
	l.doneCh = make(chan struct{})
	l.mu.Unlock()

	go l.animate()
}

// Stop halts the animation and (if running inside a TUI) requests one last
// render so the component disappears cleanly.
func (l *Loader) Stop() {
	l.mu.Lock()
	if !l.running {
		l.mu.Unlock()
		return
	}
	l.running = false
	close(l.stopCh)
	done := l.doneCh
	l.mu.Unlock()

	<-done
	l.onRequestRender()
}

// IsRunning reports whether the loader animation is active.
func (l *Loader) IsRunning() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.running
}

func (l *Loader) animate() {
	defer close(l.doneCh)

	l.mu.RLock()
	interval := l.style.Interval
	l.mu.RUnlock()
	if interval <= 0 {
		interval = 80 * time.Millisecond
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-l.stopCh:
			return
		case <-ticker.C:
			l.mu.Lock()
			l.frame++
			l.mu.Unlock()
			l.onRequestRender()
		}
	}
}

// Render draws one line: "<spinner> <message>" truncated to width.
func (l *Loader) Render(width int64) []string {
	l.mu.RLock()
	frame := l.frame
	msg := l.message
	style := l.style
	ltheme := l.theme
	running := l.running
	l.mu.RUnlock()

	if !running {
		return []string{core.PadToWidth("", width)}
	}
	if len(style.Frames) == 0 {
		style = core.SpinnerDots
	}

	sp := style.Frames[frame%int64(len(style.Frames))]
	if ltheme.SpinnerFn != nil {
		sp = ltheme.SpinnerFn(sp)
	} else {
		sp = theme.CurrentPalette().LoaderSpinner.Render(sp)
	}
	m := msg
	if ltheme.MessageFn != nil {
		m = ltheme.MessageFn(m)
	} else {
		m = theme.CurrentPalette().Dim.Render(m)
	}

	line := sp + " " + m
	return []string{core.PadToWidth(core.TruncateToWidth(line, width, "…"), width)}
}

func (l *Loader) Update(msg core.Msg) core.Cmd {
	switch msg.(type) {
	case core.WindowSizeMsg:
		l.Invalidate()
	}
	return nil
}

// Invalidate is a no-op.
func (l *Loader) Invalidate() {}

// ---------------------------------------------------------------------------
// CancellableLoader — Loader that can be aborted via Escape.
// ---------------------------------------------------------------------------

// CancellableLoader wraps Loader and surfaces an AbortSignal-like context
// that is cancelled when the user presses Escape while the loader has focus.
type CancellableLoader struct {
	*Loader

	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	onAbort func()
	aborted bool
}

// NewCancellableLoader builds a CancellableLoader that requests renders via
// the provided callback.
func NewCancellableLoader(onRequestRender func(), message string) *CancellableLoader {
	base := NewLoader(onRequestRender, message)
	ctx, cancel := context.WithCancel(context.Background())
	return &CancellableLoader{
		Loader: base,
		ctx:    ctx,
		cancel: cancel,
	}
}

// Context returns a context that is cancelled when the user aborts.
func (c *CancellableLoader) Context() context.Context { return c.ctx }

// Aborted reports whether Escape was pressed.
func (c *CancellableLoader) Aborted() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.aborted
}

// OnAbort registers a callback fired when the user aborts.
func (c *CancellableLoader) OnAbort(fn func()) {
	c.mu.Lock()
	c.onAbort = fn
	c.mu.Unlock()
}

func (c *CancellableLoader) Update(msg core.Msg) core.Cmd {
	switch m := msg.(type) {
	case core.KeyMsg:
		data := m.Data
		if terminal.MatchesKey(data, "escape") || terminal.MatchesKey(data, "ctrl+c") {
			c.mu.Lock()
			if !c.aborted {
				c.aborted = true
				c.cancel()
				cb := c.onAbort
				c.mu.Unlock()
				if cb != nil {
					cb()
				}
				return nil
			}
			c.mu.Unlock()
		}
	case core.WindowSizeMsg:
		c.Invalidate()
	}
	return nil
}

// SetFocused is a no-op implementation of Focusable so the TUI can route
// keys (Escape) to this component when it is active.
func (c *CancellableLoader) SetFocused(bool) {}

// IsFocused always returns true when the loader is running, so the TUI
// delivers input here even without an explicit focus push. In practice
// callers should use app.Focus(loader) / app.Unfocus(loader) explicitly.
func (c *CancellableLoader) IsFocused() bool { return c.Loader.IsRunning() }
