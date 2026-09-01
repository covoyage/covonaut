package component

import "time"

// Timing for unbracketed paste streams (rapid Char/Enter key events).
// Human typing is typically 50ms+ between keys; paste bursts are much faster.
const (
	pasteBurstCharInterval   = 8 * time.Millisecond
	pasteEnterSuppressWindow = 120 * time.Millisecond
	pasteBurstMinChars       = 3
)

// pasteBurst classifies rapid key streams so Enter inside a paste becomes a
// newline instead of submit. It does not buffer text; insertion still happens
// immediately. Bracketed pastes never go through this path.
type pasteBurst struct {
	now           func() time.Time
	lastChar      time.Time
	consecutive   int
	suppressUntil time.Time
}

func (b *pasteBurst) clock() time.Time {
	if b != nil && b.now != nil {
		return b.now()
	}
	return time.Now()
}

func (b *pasteBurst) notePlain() {
	if b == nil {
		return
	}
	now := b.clock()
	if !b.lastChar.IsZero() && now.Sub(b.lastChar) <= pasteBurstCharInterval {
		b.consecutive++
	} else {
		b.consecutive = 1
	}
	b.lastChar = now
	if b.consecutive >= pasteBurstMinChars {
		b.suppressUntil = now.Add(pasteEnterSuppressWindow)
	}
}

func (b *pasteBurst) consumeEnterAsNewline() bool {
	if b == nil {
		return false
	}
	now := b.clock()
	inWindow := now.Before(b.suppressUntil)
	inInterval := !b.lastChar.IsZero() && now.Sub(b.lastChar) <= pasteBurstCharInterval && b.consecutive >= pasteBurstMinChars
	if inWindow || inInterval {
		b.suppressUntil = now.Add(pasteEnterSuppressWindow)
		b.lastChar = now
		b.consecutive++
		return true
	}
	b.consecutive = 0
	return false
}

func (b *pasteBurst) reset() {
	if b == nil {
		return
	}
	b.lastChar = time.Time{}
	b.consecutive = 0
	b.suppressUntil = time.Time{}
}
