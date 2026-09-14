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

	// ticker 状态独立加锁：Render 期需要按时间节流地推进展示窗口，
	// 避免与主状态共用一把锁产生写锁竞争。
	tickerMu   sync.Mutex
	tickerIn   string   // 上游最新写入的副文案
	tickerShow string   // 当前展示的窗口（节流同步自 tickerIn）
	tickerAt   time.Time // 上次同步时刻
}

// tickerSyncInterval 限制尾窗滚动刷新频率：流式 delta 到达很快，
// 逐字跟随会滚得看不清；节流后窗口按固定节奏推进，视觉更缓。
const tickerSyncInterval = 160 * time.Millisecond

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

// SetTicker 更新同行滚动的副文案（如正在流式产出的思考尾部）。
// 空串清除。与 SetMessage 不同，这里不主动请求重绘：副文案随上游
// 流式 delta 到来，而 delta 本身会触发渲染；loader 的动画帧（~80ms）
// 兜底刷新，最迟一帧内可见。
func (l *Loader) SetTicker(text string) {
	l.tickerMu.Lock()
	l.tickerIn = text
	if text == "" {
		// 清除立即生效，不节流——避免结束/重置后残留旧文案。
		l.tickerShow = ""
		l.tickerAt = time.Now()
	}
	l.tickerMu.Unlock()
}

// currentTicker 返回当前应展示的尾窗内容：按 tickerSyncInterval 节流
// 地把最新输入同步进展示窗口，让滚动速度与 token 到达速率解耦。
func (l *Loader) currentTicker() string {
	l.tickerMu.Lock()
	defer l.tickerMu.Unlock()
	if l.tickerIn != l.tickerShow && time.Since(l.tickerAt) >= tickerSyncInterval {
		l.tickerShow = l.tickerIn
		l.tickerAt = time.Now()
	}
	return l.tickerShow
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
// 设置了 ticker 时同行追加 " │ <尾部窗口>"：窗口取副文案最右侧的
// 可用列数（始终贴最新内容），放不下（状态文案太长）时整体退化为
// 原来的单段渲染。
func (l *Loader) Render(width int64) []string {
	l.mu.RLock()
	frame := l.frame
	msg := l.message
	style := l.style
	ltheme := l.theme
	running := l.running
	l.mu.RUnlock()
	ticker := l.currentTicker()

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

	prefix := sp + " " + m
	line := prefix
	if ticker != "" {
		sep := theme.CurrentPalette().BorderMuted.Render(" │ ")
		sepW := core.VisibleWidth(sep)
		if avail := width - core.VisibleWidth(prefix) - sepW; avail >= 8 {
			tail := theme.CurrentPalette().Thinking.Render(tickerTail(ticker, avail))
			line = prefix + sep + tail
		}
	}
	return []string{core.PadToWidth(core.TruncateToWidth(line, width, "…"), width)}
}

// tickerTail 取 text 最右侧 w 列的窗口：内容超出窗口时左侧以 … 起头，
// 始终显示最新结尾；SliceByColumn 按列切取，CJK 宽字符不会被切成半字。
func tickerTail(text string, w int64) string {
	vw := core.VisibleWidth(text)
	if vw <= w {
		return text
	}
	return "…" + core.SliceByColumn(text, vw-w+1, vw)
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
