package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	core "github.com/covoyage/covonaut/tui/core"
	terminal "github.com/covoyage/covonaut/tui/terminal"
)

// errTUIAlreadyStopped is returned by Start when called on a TUI that has
// already been Stop'd. A TUI is one-shot; construct a new one to restart.
var errTUIAlreadyStopped = errors.New("tui: Start called on a stopped TUI; construct a new TUI to restart")

// ---------------------------------------------------------------------------
// TUI — differential-rendering container.
//
// A TUI instance owns:
//   - A Terminal (real or virtual) for raw I/O.
//   - A set of child Components rendered top-to-bottom.
//   - A focus stack routing Update(Msg) to the focused component.
//   - An overlay stack for floating panels.
//   - A message channel for the Elm-Architecture-style event loop.
//
// All user interaction (keys, mouse, paste, resize) is delivered as Msg
// values through the Updatable interface. Components that implement
// Updatable receive messages and return optional Cmd values for
// asynchronous side effects.
// ---------------------------------------------------------------------------

// TUIOptions configures a TUI instance.
type TUIOptions struct {
	// TickInterval is the minimum time between frames when many renders are
	// requested in a burst. Defaults to 16ms (~60 fps).
	TickInterval time.Duration

	// DisableBracketedPaste suppresses paste mode at start. Default is to enable.
	DisableBracketedPaste bool

	// DisableSynchronizedOutput suppresses CSI 2026 wrapping (useful when the
	// terminal doesn't support it — most modern terminals do, and ignoring
	// terminals ignore the sequence harmlessly).
	DisableSynchronizedOutput bool

	// AltScreen switches to the alternate screen buffer on Start and restores
	// the main screen on Stop. This prevents the TUI from polluting the
	// terminal scrollback and gives a clean "app" feel.
	AltScreen bool

	// MouseMode enables mouse event reporting. Supported values:
	//   "" or "off" — no mouse events (default).
	//   "x11"       — X11-style mouse tracking (basic click/wheel).
	//   "sgr"       — SGR-style mouse tracking (extended, preferred).
	//   "on" / "auto" — auto-detect best available (SGR if supported).
	//
	// When enabled, alternate scroll mode (DEC 1007) is disabled so that
	// the terminal sends real mouse wheel events instead of arrow-key
	// sequences.
	MouseMode string

	// Keybindings overrides the keybinding manager for this TUI (nil = global).
	Keybindings *terminal.KeybindingsManager

	// Filter is invoked before the TUI processes a Msg. The filter can
	// return any Msg which will then be handled instead of the original
	// event. If the filter returns nil, the event will be ignored entirely.
	// This is useful for intercepting quit events, implementing confirm
	// dialogs, or globally transforming messages.
	Filter func(c core.Component, msg core.Msg) core.Msg
}

// TUIOption is a functional option for configuring a TUI.
type TUIOption func(*TUIOptions)

// WithFilter supplies an event filter that will be invoked before the TUI
// processes a Msg. The filter can return any Msg which will then be handled
// instead of the original event. If the filter returns nil, the event will
// be ignored and the TUI will not process it.
func WithFilter(filter func(core.Component, core.Msg) core.Msg) TUIOption {
	return func(o *TUIOptions) {
		o.Filter = filter
	}
}

// WithTickInterval sets the minimum time between frames.
func WithTickInterval(d time.Duration) TUIOption {
	return func(o *TUIOptions) {
		o.TickInterval = d
	}
}

// WithoutBracketedPaste disables bracketed paste mode.
func WithoutBracketedPaste() TUIOption {
	return func(o *TUIOptions) {
		o.DisableBracketedPaste = true
	}
}

// WithoutSynchronizedOutput disables CSI 2026 synchronized output.
func WithoutSynchronizedOutput() TUIOption {
	return func(o *TUIOptions) {
		o.DisableSynchronizedOutput = true
	}
}

// WithKeybindings overrides the keybinding manager.
func WithKeybindings(km *terminal.KeybindingsManager) TUIOption {
	return func(o *TUIOptions) {
		o.Keybindings = km
	}
}

// WithAltScreen enables the alternate screen buffer.
func WithAltScreen() TUIOption {
	return func(o *TUIOptions) {
		o.AltScreen = true
	}
}

// WithMouse enables mouse event reporting. mode is one of "off", "x11",
// "sgr", "on", "auto" (empty = "off").
func WithMouse(mode string) TUIOption {
	return func(o *TUIOptions) {
		o.MouseMode = mode
	}
}

// TUI is the top-level differential renderer.
type TUI struct {
	term    terminal.Terminal
	stdin   *terminal.StdinBuffer
	options TUIOptions
	km      *terminal.KeybindingsManager

	mu       sync.Mutex
	children []core.Component
	overlays []*Overlay
	focus    []core.Component // focus stack; top is the active target

	renderRequested int64
	prevFrame       []core.Row
	prevWidth       int64
	firstFrame      bool
	started         bool

	// outMu guards terminal-output state (altScreenOn, mouseMode) that can be
	// mutated from public EnableMouse/DisableMouse calls concurrently with
	// Start/Stop.
	outMu       sync.Mutex
	altScreenOn bool
	mouseMode   string

	doneCh chan struct{}
	tickCh chan struct{}
	msgCh  chan core.Msg

	// stopped is set atomically BEFORE close(doneCh) in Stop. sendMsgSafe
	// checks it to decide whether to enqueue, which closes the TOCTOU window
	// that a pure channel-based check leaves: doneCh-closed and msgCh-writable
	// can both be ready in a select, letting Go pseudorandomly pick the send
	// even after Stop. The atomic flag is observed before the select, so a
	// send is impossible once stopped=true is published.
	stopped atomic.Bool

	// ctx is cancelled when the TUI stops. It is the cancellation root for
	// Tick/Every/WithContext Cmds issued via the TUI's helper methods, so
	// background timers and long-running Cmds terminate promptly on Stop.
	ctx    context.Context
	cancel context.CancelFunc

	// OnDebug is invoked for ctrl+shift+d (if the terminal sends that chord).
	OnDebug func()
}

// NewTUI constructs a TUI bound to term.
// Accepts either the legacy TUIOptions struct or the new functional options.
func NewTUI(term terminal.Terminal, opts ...TUIOptions) *TUI {
	var o TUIOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	if o.TickInterval <= 0 {
		o.TickInterval = 16 * time.Millisecond
	}
	km := o.Keybindings
	if km == nil {
		km = terminal.GetGlobalKeybindings()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &TUI{
		term:       term,
		stdin:      terminal.NewStdinBuffer(),
		options:    o,
		km:         km,
		firstFrame: true,
		doneCh:     make(chan struct{}),
		tickCh:     make(chan struct{}, 1),
		msgCh:      make(chan core.Msg, 64),
		ctx:        ctx,
		cancel:     cancel,
	}
}

// NewTUIWithOptions constructs a TUI using functional options.
func NewTUIWithOptions(term terminal.Terminal, opts ...TUIOption) *TUI {
	var o TUIOptions
	for _, opt := range opts {
		opt(&o)
	}
	return NewTUI(term, o)
}

// ---------------------------------------------------------------------------
// Lifecycle
// ---------------------------------------------------------------------------

// Start begins input and render loops. Non-blocking.
//
// A TUI is one-shot: after Stop, calling Start again returns an error
// rather than silently failing. This avoids the subtle bug where a second
// Start appears to succeed but the event loop exits immediately because
// doneCh is already closed. Callers that need a fresh TUI should construct
// a new one.
func (t *TUI) Start() error {
	t.mu.Lock()
	if t.started {
		t.mu.Unlock()
		return nil
	}
	// Detect a stopped TUI (doneCh already closed) and refuse to restart.
	select {
	case <-t.doneCh:
		t.mu.Unlock()
		return errTUIAlreadyStopped
	default:
	}
	t.started = true
	t.mu.Unlock()

	t.stdin.OnKey(func(data string) { t.onKey(data) })
	t.stdin.OnPaste(func(text string) { t.onPaste(text) })
	t.stdin.OnMouse(func(msg core.MouseMsg) { t.onMouse(msg) })

	if err := t.term.Start(t.onTerminalInput, t.onTerminalResize); err != nil {
		// Roll back started and fully tear down lifecycle signals (doneCh,
		// ctx, stdin) so the object is in a clean terminal state regardless
		// of whether the caller remembers to defer Stop(). Stop is
		// idempotent, so a later defer Stop() from the caller is a no-op.
		// We swallow Stop's error here because the term.Start failure is the
		// primary error the caller needs to see.
		t.mu.Lock()
		t.started = false
		t.mu.Unlock()
		_ = t.Stop()
		return err
	}

	if t.options.AltScreen {
		_, _ = t.term.Write([]byte("\x1b[?1049h"))
		t.outMu.Lock()
		t.altScreenOn = true
		t.outMu.Unlock()
		// Re-push Kitty keyboard after entering the alt screen. The alt
		// screen switch resets the Kitty protocol state negotiated in
		// terminal.Start; without this re-push, modifier-rich keys (e.g.
		// Cmd+C on macOS) fall back to legacy encoding and their modifiers
		// are lost — breaking shortcuts like copy. The Kitty spec
		// recommends emitting CSI >1u "when entering alternate screen
		// mode". See https://sw.kovidgoyal.net/kitty/keyboard-protocol/
		t.term.PushKittyKeyboard()
	}

	t.enableMouse(t.options.MouseMode)

	go t.eventLoop()

	cols, rows := t.term.Size()
	t.SendMsg(core.WindowSizeMsg{Width: cols, Height: rows})

	t.RequestRender()
	return nil
}

// Stop ends input/render loops and restores the terminal.
//
// Stop is safe to call even if Start was never called or failed:
//   - The stdin buffer's flushLoop goroutine is always cleaned up (it starts
//     at NewStdinBuffer time, so a never-started TUI would otherwise leak it).
//   - Done() is always closed so callers waiting on it don't block forever.
//   - The lifecycle context is always cancelled so Tick/Every/WithContext
//     goroutines terminate.
//
// Stop is idempotent — subsequent calls are no-ops that return nil.
//
// The error (if any) comes from the underlying terminal's Stop; it is no
// longer swallowed so callers can surface terminal-restoration failures.
func (t *TUI) Stop() error {
	// Always close the stdin buffer, even if Start was never called or
	// failed. The buffer starts its flushLoop in its constructor, so it
	// must be explicitly closed regardless of TUI started state.
	t.stdin.Close()

	// Idempotency gate: if doneCh is already closed, we've fully stopped.
	// This must come before the !started check — a TUI where Start failed
	// has started=false but doneCh still open, and we still need to close
	// doneCh + cancel ctx below.
	t.mu.Lock()
	select {
	case <-t.doneCh:
		t.mu.Unlock()
		return nil
	default:
	}
	wasStarted := t.started
	t.started = false
	// Publish stopped BEFORE close(doneCh). sendMsgSafe observes this flag
	// before its select, so once we set it no new message can enter msgCh —
	// closing the TOCTOU window where doneCh closes mid-check and a select
	// still picks the send branch.
	t.stopped.Store(true)
	t.mu.Unlock()

	// Tear down the lifecycle context unconditionally. This covers all
	// paths: never-started, Start-failed, and normal running. ctx/cancel
	// are created in NewTUI, so cancel is never nil here.
	if t.cancel != nil {
		t.cancel()
	}

	var stopErr error
	if wasStarted {
		t.disableMouse()

		t.outMu.Lock()
		altScreenOn := t.altScreenOn
		t.altScreenOn = false
		t.outMu.Unlock()
		if altScreenOn {
			// Pop the alt-screen Kitty push before leaving the alt screen
			// so the pop targets the correct screen context. terminal.Stop
			// will issue the final pop for the main-screen push.
			t.term.PopKittyKeyboard()
			_, _ = t.term.Write([]byte("\x1b[?1049l"))
		}

		// Restore terminal state BEFORE signaling done, so callers waiting on
		// Done() don't exit the process before termios is restored. Capture
		// the error rather than swallowing it — callers may need to surface
		// terminal-restoration failures.
		stopErr = t.term.Stop()
	}

	// Signal completion last. Callers blocked on Done() see a fully torn-down
	// TUI (terminal restored, context cancelled, stdin closed).
	close(t.doneCh)
	return stopErr
}

// Quit is an alias for Stop that returns no error. Useful for Ctrl+C handlers.
func (t *TUI) Quit() { _ = t.Stop() }

// Done returns a channel closed when the TUI has exited.
func (t *TUI) Done() <-chan struct{} { return t.doneCh }

// Context returns a context cancelled when the TUI stops. Use it to derive
// cancellation contexts for long-running operations triggered from Cmds.
// The context is created at NewTUI time (not Start), so Tick/Every/WithContext
// are always bound to the TUI's lifecycle — even if called before Start or if
// Start fails. Stop cancels it unconditionally.
func (t *TUI) Context() context.Context {
	return t.ctx
}

// Tick schedules fn to fire once after d, delivering its Msg to the event
// loop. The timer runs on the TUI's lifecycle context, so it is cancelled
// when the TUI stops (no goroutine leak). Unlike core.Tick (which is a Cmd
// constructor), this method starts the timer immediately and is the
// preferred way to schedule one-shot delays from application code.
func (t *TUI) Tick(d time.Duration, fn func(time.Time) core.Msg) {
	ctx := t.Context()
	go func() {
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case ts := <-timer.C:
			t.SendMsg(fn(ts))
		}
	}()
}

// Every schedules fn to fire repeatedly every d, delivering each Msg to the
// event loop. The ticker runs on the TUI's lifecycle context and stops when
// the TUI stops. This is the correct way to implement periodic updates
// (spinners, clock displays, polling); core.Every was removed because the
// Cmd signature (func() Msg) cannot express repeated emission.
func (t *TUI) Every(d time.Duration, fn func(time.Time) core.Msg) {
	ctx := t.Context()
	go func() {
		ticker := time.NewTicker(d)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case ts := <-ticker.C:
				t.SendMsg(fn(ts))
			}
		}
	}()
}

// ---------------------------------------------------------------------------
// Children
// ---------------------------------------------------------------------------

// AddChild appends a root-level component.
func (t *TUI) AddChild(c core.Component) {
	if c == nil {
		return
	}
	t.mu.Lock()
	t.children = append(t.children, c)
	t.mu.Unlock()
	t.RequestRender()
}

// RemoveChild removes the first occurrence of c.
func (t *TUI) RemoveChild(c core.Component) bool {
	t.mu.Lock()
	for i, ch := range t.children {
		if ch == c {
			t.children = append(t.children[:i], t.children[i+1:]...)
			t.mu.Unlock()
			t.RequestRender()
			return true
		}
	}
	t.mu.Unlock()
	return false
}

// Children returns a snapshot of root-level children.
func (t *TUI) Children() []core.Component {
	t.mu.Lock()
	defer t.mu.Unlock()
	cp := make([]core.Component, len(t.children))
	copy(cp, t.children)
	return cp
}

// ---------------------------------------------------------------------------
// Focus
// ---------------------------------------------------------------------------

// Focus pushes c onto the focus stack and makes it the active input target.
func (t *TUI) Focus(c core.Component) {
	if c == nil {
		return
	}
	t.mu.Lock()
	for i, f := range t.focus {
		if f == c {
			t.focus = append(t.focus[:i], t.focus[i+1:]...)
			break
		}
	}
	t.focus = append(t.focus, c)
	if fc, ok := c.(core.Focusable); ok {
		fc.SetFocused(true)
	}
	for i := 0; i < len(t.focus)-1; i++ {
		if fc, ok := t.focus[i].(core.Focusable); ok {
			fc.SetFocused(false)
		}
	}
	t.mu.Unlock()
	t.RequestRender()
}

// Unfocus pops c from the focus stack (if present) and returns focus to the
// previous target.
func (t *TUI) Unfocus(c core.Component) {
	t.mu.Lock()
	for i, f := range t.focus {
		if f == c {
			t.focus = append(t.focus[:i], t.focus[i+1:]...)
			if fc, ok := c.(core.Focusable); ok {
				fc.SetFocused(false)
			}
			break
		}
	}
	if len(t.focus) > 0 {
		if fc, ok := t.focus[len(t.focus)-1].(core.Focusable); ok {
			fc.SetFocused(true)
		}
	}
	t.mu.Unlock()
	t.RequestRender()
}

// Focused returns the current top of the focus stack (may be nil).
func (t *TUI) Focused() core.Component {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.focus) == 0 {
		return nil
	}
	return t.focus[len(t.focus)-1]
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

// RequestRender coalesces repeated calls into a single frame.
func (t *TUI) RequestRender() {
	atomic.StoreInt64(&t.renderRequested, 1)
	select {
	case t.tickCh <- struct{}{}:
	default:
	}
}

// Keybindings returns the manager used by this TUI.
func (t *TUI) Keybindings() *terminal.KeybindingsManager { return t.km }

// Terminal returns the underlying Terminal.
func (t *TUI) Terminal() terminal.Terminal { return t.term }

func (t *TUI) eventLoop() {
	defer func() {
		if r := recover(); r != nil {
			// Ensure terminal is restored before the process exits
			t.Stop()
			panic(r) // re-panic after cleanup so the stack trace still shows
		}
	}()

	ticker := time.NewTicker(t.options.TickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-t.doneCh:
			return
		case msg := <-t.msgCh:
			t.processMsg(msg)
		case <-t.tickCh:
		case <-ticker.C:
		}
		// Promote a pending lone ESC byte into an actual key event so
		// ESC-only presses reach the application (not just CSI sequences).
		if t.stdin != nil {
			t.stdin.FlushEsc()
		}
		if atomic.SwapInt64(&t.renderRequested, 0) == 0 {
			continue
		}
		t.renderFrame()
	}
}

func (t *TUI) processMsg(msg core.Msg) {
	if msg == nil {
		return
	}

	switch m := msg.(type) {
	case core.BatchMsg:
		// Run every Cmd concurrently — each result Msg flows back into the
		// event loop asynchronously. This never blocks the loop, even if a
		// Cmd performs slow IO. Order of completion is unspecified by design
		// (use Sequence when order matters).
		for i, cmd := range m {
			if cmd != nil {
				go t.execCmdIndexed(cmd, i)
			}
		}
		return
	case core.SequenceMessage:
		// Asynchronous ordered execution: run the first Cmd, and when it
		// completes, re-enqueue the remaining Cmds as a new SequenceMessage
		// so the event loop runs the next one. This preserves order without
		// blocking the loop.
		//
		// Skip leading nil Cmds defensively (core.Sequence filters them at
		// construction, but an externally-built SequenceMessage might not).
		// This mirrors BatchMsg's nil guard and avoids a panic → PanicMsg
		// round-trip for what is really a no-op.
		for len(m) > 0 && m[0] == nil {
			m = m[1:]
		}
		if len(m) == 0 {
			return
		}
		first := m[0]
		rest := m[1:]
		go func() {
			defer func() {
				if r := recover(); r != nil {
					t.SendMsg(core.PanicMsg{Err: r, Stack: captureStack(), CmdIndex: 0})
				}
			}()
			result := first()
			if result != nil {
				t.SendMsg(result)
			}
			if len(rest) > 0 {
				t.SendMsg(core.SequenceMessage(rest))
			}
		}()
		return
	case core.CtxMessage:
		if m.Inner() != nil {
			t.processMsg(m.Inner())
		}
		return
	case core.QuitMsg:
		t.Stop()
		return
	}

	focused := t.Focused()

	if t.options.Filter != nil {
		filtered := t.options.Filter(focused, msg)
		if filtered == nil {
			return
		}
		msg = filtered
	}

	if focused != nil {
		if u, ok := focused.(core.Updatable); ok {
			if cmd := u.Update(msg); cmd != nil {
				go t.execCmd(cmd)
			}
		}
	}

	t.mu.Lock()
	focusedIsOverlay := false
	for _, ov := range t.overlays {
		if ov != nil && ov.Content == focused {
			focusedIsOverlay = true
			break
		}
	}
	children := make([]core.Component, len(t.children))
	copy(children, t.children)
	t.mu.Unlock()

	if !focusedIsOverlay {
		for _, child := range children {
			if child == focused {
				continue
			}
			if u, ok := child.(core.Updatable); ok {
				// Non-focused children also get to run Cmds. This matches
				// the focused-component path and avoids the footgun where a
				// background component's Cmd is silently dropped.
				if cmd := u.Update(msg); cmd != nil {
					go t.execCmd(cmd)
				}
			}
		}
	}

	// Overlays are modal layers: once any overlay exists, only the focused
	// component receives input. The focused component (overlay content or
	// otherwise) was already updated above, so no further dispatch to
	// non-focused overlays is needed here.

	t.RequestRender()
}

func (t *TUI) execCmd(cmd core.Cmd) {
	t.execCmdIndexed(cmd, 0)
}

// execCmdIndexed runs a Cmd and forwards its result Msg to the event loop.
// If the Cmd panics, a PanicMsg is emitted instead of silently dropping the
// result. The index is preserved in PanicMsg for Batch diagnostics.
func (t *TUI) execCmdIndexed(cmd core.Cmd, idx int) {
	if cmd == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			t.sendMsgSafe(core.PanicMsg{Err: r, Stack: captureStack(), CmdIndex: idx})
		}
	}()
	msg := cmd()
	if msg == nil {
		return
	}
	t.sendMsgSafe(msg)
}

// sendMsgSafe enqueues a Msg, aborting silently if the TUI is already stopped.
//
// The stopped atomic flag is observed first. Stop sets it BEFORE closing
// doneCh, so once stopped=true is published no message can enter msgCh.
// This closes the TOCTOU window a pure channel-based check leaves: doneCh
// being closed and msgCh being writable can both be ready in a select, and
// Go's pseudorandom select could pick the send — accumulating zombie
// messages the (exited) event loop never drains.
//
// We still fall back to the doneCh select for the actual blocking send:
// once stopped is true we've already returned, so the select only runs in
// the not-stopped path, where doneCh-closed is the normal "TUI stopped
// while we were trying to send" race that the select handles correctly.
func (t *TUI) sendMsgSafe(msg core.Msg) {
	if t.stopped.Load() {
		return // already stopped — drop silently
	}
	select {
	case t.msgCh <- msg:
	case <-t.doneCh:
	}
}

// captureStack returns a truncated stack trace for panic diagnostics.
func captureStack() string {
	buf := make([]byte, 4096)
	n := runtime.Stack(buf, false)
	return string(buf[:n])
}

// SendMsg enqueues a message for processing by the event loop.
// This is the primary way to deliver custom messages to Updatable
// components from outside the event loop (e.g. from agent callbacks).
func (t *TUI) SendMsg(msg core.Msg) {
	if msg == nil {
		return
	}
	t.sendMsgSafe(msg)
}

func (t *TUI) renderFrame() {
	cols, _ := t.term.Size()
	if cols <= 0 {
		cols = 80
	}

	t.mu.Lock()
	children := make([]core.Component, len(t.children))
	copy(children, t.children)
	prev := t.prevFrame
	prevW := t.prevWidth
	first := t.firstFrame
	t.mu.Unlock()

	// Render children to strings, then parse each line into a cell Row.
	// Parsing happens here (not in components) so component authors keep the
	// simple []string API and the engine owns the cell model.
	var rows []core.Row
	for _, c := range children {
		for _, ln := range c.Render(cols) {
			ln = normalizeLine(ln, cols)
			rows = append(rows, core.ParseLine(ln))
		}
	}

	t.mu.Lock()
	overlays := make([]*Overlay, len(t.overlays))
	copy(overlays, t.overlays)
	t.mu.Unlock()
	_, termRows := t.term.Size()
	if termRows <= 0 {
		termRows = int64(len(rows))
	}
	rows = composeOverlays(rows, overlays, cols, termRows)

	// Locate the IME cursor marker across all rows. ParseLine already strips
	// CURSOR_MARKER and records its column on the Row; here we just find the
	// first row that carries one.
	cursorRow := int64(-1)
	cursorCol := int64(-1)
	for i, r := range rows {
		if r.CursorCol >= 0 {
			cursorRow = int64(i)
			cursorCol = int64(r.CursorCol)
			break
		}
	}

	var buf bytes.Buffer
	if !t.options.DisableSynchronizedOutput {
		buf.WriteString("\x1b[?2026h")
	}

	// Disable auto-wrap (DECAWM) for the duration of the frame render.
	// VisibleWidth (which drives truncation/padding) treats East-Asian
	// Ambiguous chars (—, →, ★, …) as width 1, but CJK-capable terminals
	// on macOS often render them as width 2. When a history line contains
	// such a character, the terminal sees the line as wider than cols and
	// wraps the last column to the next row. If that next row (e.g. the
	// editor top border) is unchanged, the diff engine skips it, and the
	// wrapped character overwrites its first cell — visible as a "gap" at
	// the left edge of the border. With DECAWM off, excess characters are
	// silently dropped at the right margin instead of wrapping.
	buf.WriteString("\x1b[?7l")

	if first || prevW != cols {
		// Full repaint: write every row from top to bottom.
		buf.WriteString("\x1b[?25l")
		buf.WriteString("\x1b[H")
		buf.WriteString("\x1b[0J")
		for i, r := range rows {
			buf.WriteString(core.SerializeRow(r))
			// SerializeRow emits its own reset when needed, but a trailing
			// reset guarantees no style leaks across lines.
			buf.WriteString("\x1b[0m")
			if i < len(rows)-1 {
				buf.WriteString("\r\n")
			}
		}
	} else {
		// Differential repaint: emit only rows that changed at the cell level.
		buf.WriteString("\x1b[?25l")
		diff := core.DiffRows(prev, rows)
		for _, d := range diff {
			fmt.Fprintf(&buf, "\x1b[%d;1H", d.Row+1)
			buf.WriteString("\x1b[2K")
			buf.WriteString(core.SerializeRow(d.Content))
			buf.WriteString("\x1b[0m")
		}
		if len(rows) < len(prev) {
			fmt.Fprintf(&buf, "\x1b[%d;1H", len(rows)+1)
			buf.WriteString("\x1b[0J")
		}
	}

	if cursorRow >= 0 {
		fmt.Fprintf(&buf, "\x1b[%d;%dH", cursorRow+1, cursorCol+1)
		buf.WriteString("\x1b[?25h")
	} else {
		buf.WriteString("\x1b[?25l")
	}

	// Re-enable auto-wrap after the frame render.
	buf.WriteString("\x1b[?7h")

	if !t.options.DisableSynchronizedOutput {
		buf.WriteString("\x1b[?2026l")
	}

	_, _ = t.term.Write(buf.Bytes())

	t.mu.Lock()
	t.prevFrame = rows
	t.prevWidth = cols
	t.firstFrame = false
	t.mu.Unlock()
}

// EnableMouse enables SGR mouse reporting. Safe to call multiple times.
func (t *TUI) EnableMouse(mode string) { t.enableMouse(mode) }

// DisableMouse disables SGR mouse reporting.
func (t *TUI) DisableMouse() { t.disableMouse() }

// ---------------------------------------------------------------------------
// Input
// ---------------------------------------------------------------------------

func (t *TUI) onTerminalInput(data []byte) {
	t.stdin.Feed(data)
}

func (t *TUI) onTerminalResize() {
	t.mu.Lock()
	t.firstFrame = true
	t.mu.Unlock()

	cols, rows := t.term.Size()
	t.SendMsg(core.WindowSizeMsg{Width: cols, Height: rows})
	t.RequestRender()
}

func (t *TUI) onKey(data string) {
	if t.OnDebug != nil && terminal.MatchesKey(data, "ctrl+shift+d") {
		t.OnDebug()
		return
	}
	t.SendMsg(core.KeyMsg{Data: data})
}

func (t *TUI) onPaste(text string) {
	t.SendMsg(core.PasteMsg{Text: text})
}

func (t *TUI) onMouse(msg core.MouseMsg) {
	t.SendMsg(msg)
}

func (t *TUI) enableMouse(mode string) {
	mode = strings.ToLower(mode)
	if mode == "" || mode == "off" {
		t.outMu.Lock()
		t.mouseMode = ""
		t.outMu.Unlock()
		return
	}
	if mode == "auto" || mode == "on" {
		mode = "sgr"
	}
	t.outMu.Lock()
	t.mouseMode = mode
	t.outMu.Unlock()
	switch mode {
	case "sgr":
		// Enable SGR positioning (?1006h) + button-event tracking (?1002h).
		// No ?1007l — we let the terminal convert wheel-to-arrow itself;
		// some terminals show rendering artifacts when toggling ?1007.
		_, _ = t.term.Write([]byte("\x1b[?1002h\x1b[?1006h"))
	case "x11":
		_, _ = t.term.Write([]byte("\x1b[?1000h"))
	}
}

func (t *TUI) disableMouse() {
	t.outMu.Lock()
	mode := t.mouseMode
	t.mouseMode = ""
	t.outMu.Unlock()
	if mode == "" {
		return
	}
	switch mode {
	case "sgr":
		_, _ = t.term.Write([]byte("\x1b[?1006l\x1b[?1002l"))
	case "x11":
		_, _ = t.term.Write([]byte("\x1b[?1000l"))
	}
}

// ---------------------------------------------------------------------------
// Overlay helpers (public API — the Overlay type itself lives in overlay.go).
// ---------------------------------------------------------------------------

// PushOverlay mounts an overlay on top of the root view.
func (t *TUI) PushOverlay(o *Overlay) {
	if o == nil {
		return
	}
	t.mu.Lock()
	t.overlays = append(t.overlays, o)
	t.mu.Unlock()
	if o.Focus {
		t.Focus(o.Content)
	}
	t.RequestRender()
}

// PopOverlay removes the top overlay; returns it or nil if the stack is empty.
func (t *TUI) PopOverlay() *Overlay {
	t.mu.Lock()
	if len(t.overlays) == 0 {
		t.mu.Unlock()
		return nil
	}
	top := t.overlays[len(t.overlays)-1]
	t.overlays = t.overlays[:len(t.overlays)-1]
	t.mu.Unlock()
	if top.Focus {
		t.Unfocus(top.Content)
	}
	t.RequestRender()
	return top
}

// RemoveOverlay pops the given overlay (no-op if not on the stack).
func (t *TUI) RemoveOverlay(o *Overlay) bool {
	if o == nil {
		return false
	}
	t.mu.Lock()
	for i, cur := range t.overlays {
		if cur == o {
			t.overlays = append(t.overlays[:i], t.overlays[i+1:]...)
			t.mu.Unlock()
			if o.Focus {
				t.Unfocus(o.Content)
			}
			t.RequestRender()
			return true
		}
	}
	t.mu.Unlock()
	return false
}

// Overlays returns a snapshot of the current overlay stack.
func (t *TUI) Overlays() []*Overlay {
	t.mu.Lock()
	defer t.mu.Unlock()
	cp := make([]*Overlay, len(t.overlays))
	copy(cp, t.overlays)
	return cp
}

// ---------------------------------------------------------------------------
// Internals
// ---------------------------------------------------------------------------

// normalizeLine ensures a single component-rendered line fits within `cols`.
// It truncates with ellipsis and preserves ANSI styles across the cut.
func normalizeLine(line string, cols int64) string {
	if core.VisibleWidth(line) <= cols {
		return line
	}
	return core.TruncateToWidth(line, cols, "…")
}
