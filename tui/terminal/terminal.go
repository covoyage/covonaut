package terminal

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/term"
)

// ---------------------------------------------------------------------------
// Terminal interface.
//
// Implementations are expected to:
//   - Put the tty into "raw" mode so every keystroke is delivered to the
//     application unprocessed.
//   - Call onInput for each chunk of bytes read from stdin.
//   - Call onResize whenever the window dimensions change (SIGWINCH).
//   - Restore the original tty state on Stop() / process exit.
//
// Two implementations ship with covonaut:
//   - ProcessTerminal (real stdin/stdout on darwin/linux).
//   - VirtualTerminal (in-memory, for tests).
// ---------------------------------------------------------------------------

// Terminal abstracts the I/O boundary for a TUI.
type Terminal interface {
	io.Writer

	Start(onInput func(data []byte), onResize func()) error
	Stop() error

	Size() (cols, rows int64)

	HideCursor()
	ShowCursor()
	ClearLine()
	ClearFromCursor()
	ClearScreen()
	MoveBy(lines int64)
	MoveTo(row, col int64)

	// PushKittyKeyboard re-emits the Kitty keyboard push sequence. Call after
	// entering the alternate screen, which resets Kitty protocol state.
	// See ProcessTerminal.PushKittyKeyboard for details.
	PushKittyKeyboard()
	// PopKittyKeyboard emits the Kitty keyboard pop sequence. Call before
	// leaving the alternate screen.
	PopKittyKeyboard()
}

// ---------------------------------------------------------------------------
// ProcessTerminal — real stdin/stdout on a terminal device.
// ---------------------------------------------------------------------------

// ProcessTerminal drives the current process's stdin/stdout.
type ProcessTerminal struct {
	in       *os.File
	out      *os.File
	readFile *os.File

	mu            sync.Mutex
	started       bool
	savedState    *term.State
	savedValid    bool
	onInput       func([]byte)
	onResize      func()
	stopRead      chan struct{}
	readDone      chan struct{}
	resizeSig     chan os.Signal
	resizeDone    chan struct{}
	stopResize    func()
	signalStopped bool
	kittyKbdOn    bool

	cols, rows int64

	// EnableKittyKeyboard, when true, pushes Kitty keyboard protocol flags
	// via CSI >Nu on Start and pops them on Stop. Default: auto — negotiate
	// only on terminals that advertise support via env variables.
	enableKittyKeyboard kittyKeyboardMode
	kittyFlags          int64

	// probeEnable, when true, runs a bounded DA1/DA2/XTVERSION capability
	// probe during Start (see probe.go). Replies are parsed in the read loop;
	// the env fast path is honored — terminals that already advertise support
	// skip the probe.
	probeEnable bool
	probe       *probeState // guarded by mu; nil unless probing
	caps        atomic.Pointer[Capabilities]
}

type kittyKeyboardMode int64

const (
	kittyKbdAuto kittyKeyboardMode = iota
	kittyKbdForceOn
	kittyKbdForceOff
)

// NewProcessTerminal builds a ProcessTerminal wired to os.Stdin / os.Stdout.
func NewProcessTerminal() *ProcessTerminal {
	return &ProcessTerminal{
		in:         os.Stdin,
		out:        os.Stdout,
		kittyFlags: 1, // disambiguate escape codes
	}
}

// SetKittyKeyboardMode overrides the default Kitty keyboard protocol policy.
//
//   - "auto" (default): enable on Kitty/Ghostty/WezTerm/foot/Alacritty.
//   - "on":  force enable (compatible with any terminal — unsupported ones
//     silently ignore the CSI).
//   - "off": never enable.
func (t *ProcessTerminal) SetKittyKeyboardMode(mode string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch mode {
	case "on":
		t.enableKittyKeyboard = kittyKbdForceOn
	case "off":
		t.enableKittyKeyboard = kittyKbdForceOff
	default:
		t.enableKittyKeyboard = kittyKbdAuto
	}
}

// SetTerminalProbe enables or disables the active capability probe. When
// enabled, Start emits DA1/DA2/XTVERSION queries and the read loop parses the
// replies in-band until the probe deadline (ProbeTimeout). Terminals that
// advertise kitty keyboard support via env vars skip the probe.
func (t *ProcessTerminal) SetTerminalProbe(enable bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.probeEnable = enable
}

// Capabilities returns the resolved terminal capabilities. If the probe has
// not completed yet (or was disabled), an env-derived result is returned.
// Safe to call from any goroutine.
func (t *ProcessTerminal) Capabilities() Capabilities {
	if c := t.caps.Load(); c != nil {
		return *c
	}
	return capabilitiesFromEnv()
}

// SetKittyKeyboardFlags overrides the progressive-enhancement flags pushed
// when Kitty keyboard protocol is enabled (default: 1 — disambiguate).
//
//	flag 1  = disambiguate escape codes
//	flag 2  = report event types (press/repeat/release)
//	flag 4  = report alternate keys
//	flag 8  = report all keys as escape codes
//	flag 16 = report associated text
func (t *ProcessTerminal) SetKittyKeyboardFlags(flags int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if flags < 1 {
		flags = 1
	}
	t.kittyFlags = flags
}

// Start enters raw mode, enables bracketed paste, and begins pumping input.
func (t *ProcessTerminal) Start(onInput func(data []byte), onResize func()) error {
	t.mu.Lock()
	if t.started {
		t.mu.Unlock()
		return nil
	}

	saved, err := term.MakeRaw(int(t.in.Fd()))
	if err != nil {
		t.mu.Unlock()
		return fmt.Errorf("tui: enter raw mode: %w", err)
	}
	t.savedState = saved
	t.savedValid = true
	readFile, err := duplicateInput(t.in)
	if err != nil {
		_ = term.Restore(int(t.in.Fd()), saved)
		t.savedState = nil
		t.savedValid = false
		t.mu.Unlock()
		return fmt.Errorf("tui: duplicate input: %w", err)
	}
	t.readFile = readFile

	if cols, rows, err := term.GetSize(int(t.out.Fd())); err == nil {
		atomic.StoreInt64(&t.cols, int64(cols))
		atomic.StoreInt64(&t.rows, int64(rows))
	} else {
		atomic.StoreInt64(&t.cols, 80)
		atomic.StoreInt64(&t.rows, 24)
	}

	t.onInput = onInput
	t.onResize = onResize
	t.stopRead = make(chan struct{})
	t.readDone = make(chan struct{})
	t.resizeSig = make(chan os.Signal, 1)
	t.resizeDone = make(chan struct{})
	t.stopResize = startResizeNotifications(t.resizeSig)
	t.signalStopped = false
	t.started = true

	_, _ = t.out.WriteString("\x1b[?2004h") // enable bracketed paste

	if t.shouldEnableKittyKbdLocked() {
		fmt.Fprintf(t.out, "\x1b[>%du", t.kittyFlags)
		t.kittyKbdOn = true
		SetKittyProtocolActive(true)
	}

	// Capability probe: emit the queries now; replies are parsed in-band by
	// the read loop (input is never lost — unmatched bytes are forwarded).
	var probe *probeState
	startProbe := t.probeEnable
	if startProbe && t.enableKittyKeyboard == kittyKbdAuto && TerminalSupportsKittyKeyboard() {
		// Env fast path: the terminal already advertises kitty keyboard
		// support, so a probe would add latency without new information.
		startProbe = false
	}
	if startProbe {
		probe = newProbeState(time.Now().Add(ProbeTimeout))
		t.probe = probe
		_, _ = t.out.WriteString(terminalCapabilityQueries())
	}

	t.mu.Unlock()

	go t.readLoop()
	if startProbe {
		go t.finishProbeAfterTimeout(probe)
	}
	go t.resizeLoop()
	return nil
}

// Stop restores the tty state. Safe to call multiple times.
func (t *ProcessTerminal) Stop() error {
	t.mu.Lock()
	if !t.started {
		t.mu.Unlock()
		return nil
	}
	t.started = false
	close(t.stopRead)
	readFile := t.readFile
	t.readFile = nil
	if !t.signalStopped {
		// Stop signal delivery first, then close the channel so the
		// resizeLoop's `for range` exits. Closing before signal.Stop would
		// risk a send-on-closed-channel panic if a SIGWINCH arrived in
		// between (signal.Notify would still be registered).
		if t.stopResize != nil {
			t.stopResize()
			t.stopResize = nil
		}
		close(t.resizeSig)
		t.signalStopped = true
	}
	savedValid := t.savedValid
	saved := t.savedState
	// Read kittyKbdOn under the lock: it is written under t.mu in Start,
	// and reading it unlocked below was a (narrow) data race. Clearing it
	// here also makes Stop idempotent — a second Stop won't re-emit the
	// pop sequence.
	kittyKbdOn := t.kittyKbdOn
	t.kittyKbdOn = false
	t.mu.Unlock()
	if readFile != nil {
		_ = cancelRead(readFile)
		_ = readFile.Close()
	}

	select {
	case <-t.readDone:
	case <-time.After(300 * time.Millisecond):
	}
	select {
	case <-t.resizeDone:
	case <-time.After(300 * time.Millisecond):
	}

	_, _ = t.out.WriteString("\x1b[?2004l") // disable bracketed paste
	if kittyKbdOn {
		_, _ = t.out.WriteString("\x1b[<u") // pop kitty keyboard flags
		SetKittyProtocolActive(false)
	}
	_, _ = t.out.WriteString("\x1b[?25h") // ensure cursor is visible

	if savedValid && saved != nil {
		_ = term.Restore(int(t.in.Fd()), saved)
	}
	return nil
}

// Write writes directly to stdout.
func (t *ProcessTerminal) Write(data []byte) (int, error) {
	return t.out.Write(data)
}

// Size returns the last-known (cols, rows).
func (t *ProcessTerminal) Size() (int64, int64) {
	return atomic.LoadInt64(&t.cols), atomic.LoadInt64(&t.rows)
}

func (t *ProcessTerminal) HideCursor()      { _, _ = t.out.WriteString("\x1b[?25l") }
func (t *ProcessTerminal) ShowCursor()      { _, _ = t.out.WriteString("\x1b[?25h") }
func (t *ProcessTerminal) ClearLine()       { _, _ = t.out.WriteString("\x1b[2K") }
func (t *ProcessTerminal) ClearFromCursor() { _, _ = t.out.WriteString("\x1b[0J") }
func (t *ProcessTerminal) ClearScreen()     { _, _ = t.out.WriteString("\x1b[2J\x1b[H") }
func (t *ProcessTerminal) MoveBy(lines int64) {
	if lines < 0 {
		fmt.Fprintf(t.out, "\x1b[%dA", -lines)
	} else if lines > 0 {
		fmt.Fprintf(t.out, "\x1b[%dB", lines)
	}
}
func (t *ProcessTerminal) MoveTo(row, col int64) {
	fmt.Fprintf(t.out, "\x1b[%d;%dH", row, col)
}

// PushKittyKeyboard re-emits the Kitty keyboard push sequence (CSI >flags u).
// Call this after entering the alternate screen.
//
// Background: switching to the alt screen resets the Kitty keyboard protocol
// state negotiated in Start, so modifier-rich keys (e.g. Cmd+C on macOS) fall
// back to legacy encoding and their modifiers are lost — breaking shortcuts
// like copy. Re-pushing after the alt-screen switch restores CSI u encoding.
// See https://sw.kovidgoyal.net/kitty/keyboard-protocol/ ("when entering
// alternate screen mode").
//
// No-op when Kitty keyboard is disabled (mode "off") or unsupported (mode
// "auto" on an unsupported terminal). Safe to call multiple times; each call
// is balanced by a PopKittyKeyboard before leaving the alt screen.
func (t *ProcessTerminal) PushKittyKeyboard() {
	t.mu.Lock()
	if !t.shouldEnableKittyKbdLocked() {
		t.mu.Unlock()
		return
	}
	fmt.Fprintf(t.out, "\x1b[>%du", t.kittyFlags)
	t.kittyKbdOn = true
	t.mu.Unlock()
	SetKittyProtocolActive(true)
}

// PopKittyKeyboard emits the Kitty keyboard pop sequence (CSI <u). Call this
// before leaving the alternate screen so the pop targets the alt-screen push.
// terminal.Stop will issue the final pop for the main-screen push.
//
// No-op when Kitty keyboard was never enabled. Does not clear kittyKbdOn —
// that flag tracks the main-screen push and is reset by terminal.Stop.
func (t *ProcessTerminal) PopKittyKeyboard() {
	t.mu.Lock()
	on := t.kittyKbdOn
	t.mu.Unlock()
	if !on {
		return
	}
	_, _ = t.out.WriteString("\x1b[<u")
}

func (t *ProcessTerminal) readLoop() {
	defer close(t.readDone)
	t.mu.Lock()
	readFile := t.readFile
	t.mu.Unlock()
	if readFile == nil {
		return
	}
	buf := make([]byte, 4096)
	for {
		select {
		case <-t.stopRead:
			return
		default:
		}
		n, err := readFile.Read(buf)
		if n > 0 && t.onInput != nil {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			t.mu.Lock()
			var toInput []byte
			if p := t.probe; p != nil {
				// Fast path: no reply frame is mid-assembly and this chunk
				// cannot start one (reply frames always begin with ESC), so
				// hand it straight through. The long-lived lax probe must not
				// add per-keystroke cost on the common hot path.
				if len(p.buf) == 0 && chunk[0] != 0x1b {
					toInput = chunk
				} else {
					toInput = t.routeProbeLocked(p, chunk)
				}
			} else {
				toInput = chunk
			}
			t.mu.Unlock()
			if len(toInput) > 0 {
				t.onInput(toInput)
			}
		}
		if err != nil {
			return
		}
	}
}

// routeProbeLocked feeds a stdin chunk to the active probe, returning the
// bytes that belong to the user (not the probe). When the probe completes — a
// reply arrived or the deadline passed — capabilities are stored once and the
// probe transitions to lax mode: it stays installed to swallow any late or
// fragmented reply frames (see probeState.lax) rather than being torn down.
// Callers hold t.mu.
func (t *ProcessTerminal) routeProbeLocked(p *probeState, chunk []byte) []byte {
	toInput, done := p.feed(chunk)
	if done || (!p.lax && time.Now().After(p.deadline)) {
		toInput = append(toInput, p.drain()...)
		p.lax = true
		caps := capabilitiesFromProbe(p, capabilitiesFromEnv())
		t.caps.Store(&caps)
		// A probe-verified kitty-capable terminal that the env fast path
		// missed: push the kitty keyboard protocol now that we know.
		if caps.KittyKeyboard && t.enableKittyKeyboard == kittyKbdAuto && !t.kittyKbdOn {
			fmt.Fprintf(t.out, "\x1b[>%du", t.kittyFlags)
			t.kittyKbdOn = true
			SetKittyProtocolActive(true)
		}
	}
	return toInput
}

// finishProbeAfterTimeout finalizes the probe even when the terminal never
// responds (e.g. stdin is a pipe or the reply was lost). Keeps Start
// non-blocking: this runs up to ProbeTimeout after Start returns.
func (t *ProcessTerminal) finishProbeAfterTimeout(p *probeState) {
	timer := time.NewTimer(ProbeTimeout + 20*time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			t.mu.Lock()
			if t.probe == p {
				leftover := t.routeProbeLocked(p, nil)
				t.mu.Unlock()
				if len(leftover) > 0 && t.onInput != nil {
					t.onInput(leftover)
				}
			} else {
				// Already finalized by the read loop.
				t.mu.Unlock()
			}
			return
		case <-t.stopRead:
			return
		}
	}
}

func (t *ProcessTerminal) resizeLoop() {
	defer close(t.resizeDone)
	for range t.resizeSig {
		cols, rows, err := term.GetSize(int(t.out.Fd()))
		if err == nil && t.updateSize(int64(cols), int64(rows)) && t.onResize != nil {
			t.onResize()
		}
	}
}

func (t *ProcessTerminal) updateSize(cols, rows int64) bool {
	oldCols := atomic.SwapInt64(&t.cols, cols)
	oldRows := atomic.SwapInt64(&t.rows, rows)
	return oldCols != cols || oldRows != rows
}

// IsTerminal reports whether fd refers to a terminal device.
func IsTerminal(fd uintptr) bool {
	return term.IsTerminal(int(fd))
}

// shouldEnableKittyKbdLocked evaluates the kitty keyboard mode against the
// current environment. Callers must hold t.mu.
func (t *ProcessTerminal) shouldEnableKittyKbdLocked() bool {
	switch t.enableKittyKeyboard {
	case kittyKbdForceOn:
		return true
	case kittyKbdForceOff:
		return false
	default:
		return TerminalSupportsKittyKeyboard()
	}
}

// TerminalSupportsKittyKeyboard returns true when the current terminal is
// known to implement (a subset of) the Kitty keyboard protocol. This is a
// best-effort heuristic based on env variables.
func TerminalSupportsKittyKeyboard() bool {
	term := os.Getenv("TERM")
	termProgram := os.Getenv("TERM_PROGRAM")
	switch {
	case os.Getenv("KITTY_WINDOW_ID") != "":
		return true
	case term == "xterm-kitty", term == "xterm-ghostty":
		return true
	case termProgram == "WezTerm", termProgram == "ghostty":
		return true
	case os.Getenv("GHOSTTY_RESOURCES_DIR") != "":
		return true
	case os.Getenv("FOOT_VERSION") != "":
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// VirtualTerminal — in-memory terminal for tests.
//
// Output is captured in a buffer retrievable via Output(). Input can be
// injected with Type(). SIGWINCH is simulated via Resize().
// ---------------------------------------------------------------------------

// VirtualTerminal is a test-only Terminal implementation.
type VirtualTerminal struct {
	mu       sync.Mutex
	output   []byte
	onInput  func([]byte)
	onResize func()
	cols     int64
	rows     int64
	started  bool
}

// NewVirtualTerminal returns a VirtualTerminal sized (cols x rows).
func NewVirtualTerminal(cols, rows int64) *VirtualTerminal {
	return &VirtualTerminal{cols: cols, rows: rows}
}

func (v *VirtualTerminal) Start(onInput func([]byte), onResize func()) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.started {
		return errors.New("tui: VirtualTerminal already started")
	}
	v.onInput = onInput
	v.onResize = onResize
	v.started = true
	return nil
}

func (v *VirtualTerminal) Stop() error {
	v.mu.Lock()
	v.started = false
	v.mu.Unlock()
	return nil
}

func (v *VirtualTerminal) Write(p []byte) (int, error) {
	v.mu.Lock()
	v.output = append(v.output, p...)
	v.mu.Unlock()
	return len(p), nil
}

func (v *VirtualTerminal) Size() (int64, int64) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.cols, v.rows
}

func (v *VirtualTerminal) HideCursor()         {}
func (v *VirtualTerminal) ShowCursor()         {}
func (v *VirtualTerminal) ClearLine()          {}
func (v *VirtualTerminal) ClearFromCursor()    {}
func (v *VirtualTerminal) ClearScreen()        {}
func (v *VirtualTerminal) MoveBy(int64)        {}
func (v *VirtualTerminal) MoveTo(int64, int64) {}
func (v *VirtualTerminal) PushKittyKeyboard()  {}
func (v *VirtualTerminal) PopKittyKeyboard()   {}

// Type feeds raw bytes as though the user typed them.
func (v *VirtualTerminal) Type(data string) {
	v.mu.Lock()
	fn := v.onInput
	v.mu.Unlock()
	if fn != nil {
		fn([]byte(data))
	}
}

// Resize updates dimensions and notifies the TUI.
func (v *VirtualTerminal) Resize(cols, rows int64) {
	v.mu.Lock()
	v.cols = cols
	v.rows = rows
	fn := v.onResize
	v.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// Output returns a copy of everything written to the terminal since start.
func (v *VirtualTerminal) Output() []byte {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := make([]byte, len(v.output))
	copy(out, v.output)
	return out
}

// OutputString is a convenience wrapper around Output.
func (v *VirtualTerminal) OutputString() string {
	return string(v.Output())
}

// ResetOutput clears the captured output buffer.
func (v *VirtualTerminal) ResetOutput() {
	v.mu.Lock()
	v.output = v.output[:0]
	v.mu.Unlock()
}
