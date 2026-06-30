package terminal

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covonaut/tui/core"
)

// ---------------------------------------------------------------------------
// StdinBuffer
//
// Raw terminal input may arrive in fragmented chunks that either:
//   1. Split a multi-byte ANSI escape (e.g. "\x1b[" arrives separately from
//      "A"), breaking naive parsers.
//   2. Batch many keystrokes (or an entire paste) into one read.
//
// StdinBuffer solves both:
//   - It buffers partial escape sequences until complete.
//   - It splits batched input into logical "events" (keys / paste blobs)
//     so consumers can process them one at a time.
//   - It recognises bracketed-paste markers ESC[200~ ... ESC[201~ and emits
//     the inner content as a single Paste event.
//
// Usage:
//
//   sb := tui.NewStdinBuffer()
//   sb.OnKey(func(data string) { ... })    // raw per-key chunks
//   sb.OnPaste(func(text string) { ... })  // bracketed-paste content
//   sb.Feed(chunkFromTerminal)
// ---------------------------------------------------------------------------

// StdinBufferOptions configures optional behaviour.
type StdinBufferOptions struct {
	// MaxPasteBytes caps the in-memory paste buffer. Excess bytes are
	// truncated silently. 0 means 16 MiB (sane default).
	MaxPasteBytes int64
}

// StdinBuffer holds fragmented terminal input and emits logical events.
type StdinBuffer struct {
	opts StdinBufferOptions

	mu         sync.Mutex
	buf        []byte
	inPaste    bool
	pasteBuf   strings.Builder
	pasteBytes int64

	// escPendingAt records when a lone ESC byte first landed in b.buf.
	// FlushEsc uses this to emit a standalone ESC key event after
	// EscFlushDelay elapses, so the user pressing ESC alone is not lost
	// while the buffer waits for a (possibly nonexistent) CSI continuation.
	escPendingAt time.Time

	// stopFlush signals the background flushLoop goroutine to exit.
	stopFlush chan struct{}
	// closed guards stopFlush against a double close.
	closed bool

	onKey   func(data string)
	onPaste func(text string)
	onMouse func(msg core.MouseMsg)
}

// NewStdinBuffer returns a new buffer with defaults applied.
func NewStdinBuffer(opts ...StdinBufferOptions) *StdinBuffer {
	var o StdinBufferOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	if o.MaxPasteBytes <= 0 {
		o.MaxPasteBytes = 16 * 1024 * 1024
	}
	b := &StdinBuffer{opts: o, stopFlush: make(chan struct{})}
	// Run a background ticker that promotes a long-waiting lone ESC byte
	// into a key event, independent of any TUI render loop. Without this
	// goroutine, a TUI handler that blocks for longer than EscFlushDelay
	// (e.g. opening a heavy overlay synchronously) would prevent the
	// render loop's per-tick FlushEsc from ever firing and ESC would
	// appear "stuck" until the next unrelated key arrived.
	go b.flushLoop()
	return b
}

// flushLoop runs for the lifetime of the buffer and ticks every
// EscFlushDelay/2 (so the deadline is never missed by more than half a
// window). It exits when stopFlush is closed.
func (b *StdinBuffer) flushLoop() {
	t := time.NewTicker(EscFlushDelay / 2)
	defer t.Stop()
	for {
		select {
		case <-b.stopFlush:
			return
		case <-t.C:
			b.FlushEsc()
		}
	}
}

// OnKey registers the callback for regular key chunks.
func (b *StdinBuffer) OnKey(fn func(data string)) {
	b.mu.Lock()
	b.onKey = fn
	b.mu.Unlock()
}

// OnPaste registers the callback for bracketed-paste content.
func (b *StdinBuffer) OnPaste(fn func(text string)) {
	b.mu.Lock()
	b.onPaste = fn
	b.mu.Unlock()
}

// OnMouse registers the callback for mouse events.
func (b *StdinBuffer) OnMouse(fn func(msg core.MouseMsg)) {
	b.mu.Lock()
	b.onMouse = fn
	b.mu.Unlock()
}

// Feed appends raw bytes and drains any complete events.
func (b *StdinBuffer) Feed(data []byte) {
	b.mu.Lock()
	b.buf = append(b.buf, data...)
	keys, pastes, mice := b.drainLocked()
	onKey := b.onKey
	onPaste := b.onPaste
	onMouse := b.onMouse
	b.mu.Unlock()

	for _, p := range pastes {
		if onPaste != nil {
			onPaste(p)
		}
	}
	for _, m := range mice {
		if onMouse != nil {
			onMouse(m)
		}
	}
	for _, k := range keys {
		if onKey != nil {
			onKey(k)
		}
	}
}

// FeedString is a convenience wrapper around Feed.
func (b *StdinBuffer) FeedString(data string) { b.Feed([]byte(data)) }

// EscFlushDelay is how long a lone ESC byte may sit in the buffer before
// FlushEsc forces it out as a standalone ESC key event. The value is short
// enough to feel instantaneous to the user and long enough to never
// prematurely split a real CSI sequence that the kernel happened to
// fragment across two reads.
const EscFlushDelay = 50 * time.Millisecond

// FlushEsc emits any lone ESC byte that has been pending in the buffer
// longer than EscFlushDelay. It is a no-op when the buffer has no pending
// ESC or the ESC is part of a still-completing sequence.
//
// Two callers keep this working: the TUI render loop calls it every frame
// so the common case is responsive, and a background goroutine started by
// NewStdinBuffer calls it independently of the render loop, so a lone ESC
// still fires even when the render loop is blocked in a long Update.
func (b *StdinBuffer) FlushEsc() {
	b.mu.Lock()
	pending := len(b.buf) == 1 && b.buf[0] == 0x1B
	if !pending || b.escPendingAt.IsZero() || time.Since(b.escPendingAt) < EscFlushDelay {
		b.mu.Unlock()
		return
	}
	// Promote the lone ESC into a one-byte event and clear the buffer.
	b.buf = nil
	b.escPendingAt = time.Time{}
	onKey := b.onKey
	b.mu.Unlock()
	if onKey != nil {
		onKey("\x1b")
	}
}

// Close stops the background flushLoop goroutine. It is safe to call
// multiple times.
func (b *StdinBuffer) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	close(b.stopFlush)
	b.mu.Unlock()
}

// Reset clears all buffered bytes without emitting events.
func (b *StdinBuffer) Reset() {
	b.mu.Lock()
	b.buf = nil
	b.inPaste = false
	b.pasteBuf.Reset()
	b.pasteBytes = 0
	b.escPendingAt = time.Time{}
	b.mu.Unlock()
}

// drainLocked walks the buffer and returns completed events. It mutates
// b.buf to keep only the incomplete tail.
func (b *StdinBuffer) drainLocked() (keys []string, pastes []string, mice []core.MouseMsg) {
	const (
		pasteStart = "\x1b[200~"
		pasteEnd   = "\x1b[201~"
	)

	for {
		b.updateEscPendingLocked()

		if len(b.buf) == 0 {
			return
		}

		if b.inPaste {
			idx := indexBytes(b.buf, []byte(pasteEnd))
			if idx < 0 {
				// keep one near-marker in buf so split markers still match
				keepTail := len(pasteEnd) - 1
				if keepTail > len(b.buf) {
					keepTail = len(b.buf)
				}
				b.appendPaste(b.buf[:len(b.buf)-keepTail])
				b.buf = append(b.buf[:0], b.buf[len(b.buf)-keepTail:]...)
				return
			}
			b.appendPaste(b.buf[:idx])
			pastes = append(pastes, b.pasteBuf.String())
			b.pasteBuf.Reset()
			b.pasteBytes = 0
			b.inPaste = false
			b.buf = append(b.buf[:0], b.buf[idx+len(pasteEnd):]...)
			continue
		}

		// X11-style mouse: \x1b[MCbCxCy (3 bytes after [M)
		if len(b.buf) >= 6 && b.buf[0] == 0x1B && b.buf[1] == '[' && b.buf[2] == 'M' {
			cb := int(b.buf[3]) - 32
			cx := int(b.buf[4]) - 32
			cy := int(b.buf[5]) - 32
			if cx < 1 {
				cx = 1
			}
			if cy < 1 {
				cy = 1
			}
			mice = append(mice, parseX11Mouse(cb, int64(cx), int64(cy)))
			b.buf = append(b.buf[:0], b.buf[6:]...)
			continue
		}

		// SGR-style mouse: \x1b[<Cb;Cx;CyM or \x1b[<Cb;Cx;Cym
		if len(b.buf) >= 6 && b.buf[0] == 0x1B && b.buf[1] == '[' && b.buf[2] == '<' {
			end := indexByteFrom(b.buf, 3, 'M', 'm')
			if end < 0 {
				if len(b.buf) > 64 {
					b.buf = nil
				}
				return
			}
			seq := string(b.buf[3 : end+1]) // include terminating M/m
			m, ok := parseSGRMouse(seq)
			if ok {
				mice = append(mice, m)
			}
			b.buf = append(b.buf[:0], b.buf[end+1:]...)
			continue
		}

		// Look for next paste-start; anything before is key data.
		idx := indexBytes(b.buf, []byte(pasteStart))
		if idx >= 0 {
			if idx > 0 {
				chunks := splitInputIntoEvents(string(b.buf[:idx]))
				keys = append(keys, chunks...)
			}
			b.buf = append(b.buf[:0], b.buf[idx+len(pasteStart):]...)
			b.inPaste = true
			continue
		}

		// No paste start in view — drain any fully-formed events, keep tail.
		consumed, chunks := consumeKeyEvents(b.buf)
		keys = append(keys, chunks...)
		if consumed == 0 {
			return
		}
		b.buf = append(b.buf[:0], b.buf[consumed:]...)
	}
}

// updateEscPendingLocked maintains escPendingAt according to current buffer
// state. A lone ESC byte starts/keeps a pending timer; any other state clears
// it.
func (b *StdinBuffer) updateEscPendingLocked() {
	if len(b.buf) == 1 && b.buf[0] == 0x1B {
		if b.escPendingAt.IsZero() {
			b.escPendingAt = time.Now()
		}
		return
	}
	b.escPendingAt = time.Time{}
}

func (b *StdinBuffer) appendPaste(p []byte) {
	if len(p) == 0 {
		return
	}
	remaining := b.opts.MaxPasteBytes - b.pasteBytes
	if remaining <= 0 {
		return
	}
	if int64(len(p)) > remaining {
		p = p[:remaining]
	}
	b.pasteBuf.Write(p)
	b.pasteBytes += int64(len(p))
}

// ---------------------------------------------------------------------------
// Event splitting
// ---------------------------------------------------------------------------

// consumeKeyEvents walks the buffer and returns (bytesConsumed, events),
// where each event is one complete logical key chunk (including ANSI escape
// sequences parsed atomically). Trailing incomplete escape is preserved.
func consumeKeyEvents(buf []byte) (int64, []string) {
	var events []string
	i := 0
	for i < len(buf) {
		if buf[i] == 0x1B {
			adv := core.SkipAnsiSeq(string(buf), i)
			if adv <= 0 {
				// no more bytes — incomplete escape, stop here
				return int64(i), events
			}
			// Guard against the "incomplete" case where skipAnsiSeq returned
			// the remaining length (no final byte seen yet).
			if !ansiSeqComplete(buf, i, adv) {
				return int64(i), events
			}
			events = append(events, string(buf[i:i+adv]))
			i += adv
			continue
		}
		// A single UTF-8 rune is one event.
		size := runeByteSize(buf[i:])
		if size == 0 || i+size > len(buf) {
			return int64(i), events
		}
		events = append(events, string(buf[i:i+size]))
		i += size
	}
	return int64(i), events
}

// splitInputIntoEvents is like consumeKeyEvents but takes a string and
// discards any incomplete trailing escape (used for pre-paste flushes).
func splitInputIntoEvents(s string) []string {
	_, ev := consumeKeyEvents([]byte(s))
	return ev
}

// ansiSeqComplete returns true if buf[i:i+adv] is a syntactically complete
// ANSI escape sequence (per the skipAnsiSeq rules).
func ansiSeqComplete(buf []byte, i int, adv int) bool {
	if adv <= 1 {
		return true
	}
	if i+adv > len(buf) {
		return false
	}
	b := buf[i+1]
	switch b {
	case '[':
		last := buf[i+adv-1]
		return last >= 0x40 && last <= 0x7E
	case ']', '_', 'P', '^':
		// Terminated by BEL or ESC '\\'
		tail := buf[i+adv-1]
		if tail == 0x07 {
			return true
		}
		if adv >= 2 && buf[i+adv-2] == 0x1B && tail == '\\' {
			return true
		}
		return false
	case 'N', 'O':
		return adv >= 3
	default:
		return adv >= 2
	}
}

// runeByteSize returns the byte length of the UTF-8 rune at the start of b,
// or 0 if b is empty.
func runeByteSize(b []byte) int {
	if len(b) == 0 {
		return 0
	}
	c := b[0]
	switch {
	case c < 0x80:
		return 1
	case c&0xE0 == 0xC0:
		return 2
	case c&0xF0 == 0xE0:
		return 3
	case c&0xF8 == 0xF0:
		return 4
	default:
		return 1
	}
}

// indexBytes returns the index of needle in haystack, or -1.
func indexBytes(haystack, needle []byte) int {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return -1
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := 0; j < len(needle); j++ {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// indexByteFrom returns the index of the first byte equal to a or b in
// haystack[from:], or -1.
func indexByteFrom(haystack []byte, from int, a, b byte) int {
	for i := from; i < len(haystack); i++ {
		if haystack[i] == a || haystack[i] == b {
			return i
		}
	}
	return -1
}

// parseX11Mouse decodes an X11-style mouse event from the Cb byte and
// (cx, cy) coordinates (1-based).
func parseX11Mouse(cb int, cx, cy int64) core.MouseMsg {
	m := core.MouseMsg{Col: cx - 1, Row: cy - 1}
	m.Shift = cb&0x04 != 0
	m.Alt = cb&0x08 != 0
	m.Ctrl = cb&0x10 != 0

	button := cb & 0x03
	switch {
	case cb&0x40 != 0:
		switch button {
		case 0:
			m.Action = core.MouseWheelUp
		case 1:
			m.Action = core.MouseWheelDown
		}
	case cb&0x20 != 0:
		m.Action = core.MouseMotion
		m.Button = int64(button)
	default:
		switch button {
		case 0, 1, 2:
			m.Action = core.MousePress
			m.Button = int64(button)
		case 3:
			m.Action = core.MouseRelease
		}
	}
	return m
}

// parseSGRMouse decodes an SGR-style mouse event from the parameter string
// (the part between "<" and "M"/m", including the terminator).
func parseSGRMouse(seq string) (core.MouseMsg, bool) {
	var cb, cx, cy int
	var release bool

	n, err := fmt.Sscanf(seq, "%d;%d;%d", &cb, &cx, &cy)
	if err != nil || n != 3 {
		return core.MouseMsg{}, false
	}

	// Check terminator: 'm' = release, 'M' = press
	if len(seq) > 0 {
		release = seq[len(seq)-1] == 'm'
	}

	m := core.MouseMsg{Col: int64(cx) - 1, Row: int64(cy) - 1}
	m.Shift = cb&0x04 != 0
	m.Alt = cb&0x08 != 0
	m.Ctrl = cb&0x10 != 0

	button := cb & 0x03
	switch {
	case cb&0x40 != 0:
		switch button {
		case 0:
			m.Action = core.MouseWheelUp
		case 1:
			m.Action = core.MouseWheelDown
		}
	case cb&0x20 != 0:
		m.Action = core.MouseMotion
		m.Button = int64(button)
	default:
		if release {
			m.Action = core.MouseRelease
		} else {
			m.Action = core.MousePress
			m.Button = int64(button)
		}
	}
	return m, true
}
