package terminal

import (
	"bytes"
	"os"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Capability probing.
//
// Terminal capabilities are usually inferred from env vars
// (TerminalSupportsKittyKeyboard). Some terminals (foot, older iTerm2, custom
// xterm builds, remote hosts under tmux passthrough) don't advertise support
// through env, so an active probe is the only reliable signal.
//
// ProbeTimeout bounds the wait for replies. The probe never blocks Start: it
// emits a few query sequences and the read loop parses replies in-band until
// the deadline. Input typed during the window is fed back to onInput
// unmodified, so nothing is ever lost.
// ---------------------------------------------------------------------------

// Capabilities describes terminal features resolved via active probing and/or
// environment heuristics.
type Capabilities struct {
	Probed        bool   // an active DA/XTVERSION probe ran
	Responded     bool   // the terminal answered at least one query
	KittyKeyboard bool   // CSI u (kitty keyboard protocol) supported
	TrueColor     bool   // 24-bit color supported
	Name          string // XTVERSION payload, e.g. "kitty-0.35.0"
	Brand         string // normalized brand: kitty/wezterm/ghostty/foot/alacritty/xterm/...
}

// CapabilitiesProvider is implemented by terminals that can report probed
// capabilities. ProcessTerminal implements it; VirtualTerminal does not.
type CapabilitiesProvider interface {
	Capabilities() Capabilities
}

// ProbeTimeout bounds the time spent waiting for capability replies.
const ProbeTimeout = 200 * time.Millisecond

// LaxProbeMaxBuf caps how much of a reply frame the probe will buffer after
// its initial job is done. A terminal that opens a reply frame and never
// terminates it must not swallow the user's keystrokes indefinitely, so once
// the buffer exceeds this the bytes are handed back as input.
const LaxProbeMaxBuf = 256

// dcsXTSeq opens the XTVERSION DCS string (\x1b P > | payload \x1b \\).
// dcsST terminates it. dcsXTSeq/dcsST match the DCS passthrough framing.
var (
	dcsXTSeq = []byte{0x1b, 'P', '>', '|'}
	dcsST    = []byte{0x1b, '\\'}
)

// terminalCapabilityQueries solicits DA1, DA2 and XTVERSION replies.
//   - CSI c          -> DA1  (terminal level)
//   - CSI > c        -> DA2  (xterm-family version)
//   - CSI > q        -> XTVERSION (name/version DCS; kitty/wezterm/ghostty/...)
func terminalCapabilityQueries() string {
	return "\x1b[c\x1b[>c\x1b[>q"
}

// probeState incrementally assembles capability replies. It is not
// thread-safe; ProcessTerminal guards it with its mutex.
type probeState struct {
	deadline  time.Time
	buf       []byte // bytes not yet classified as frame vs user input
	responded bool   // at least one reply frame matched
	name      string // XTVERSION payload
	done      bool   // a complete DA or XTVERSION reply was seen
	// lax marks a probe that has finished its job — a complete reply arrived
	// or the deadline passed — but stays installed as a passive reply filter.
	// Terminals answer the queries asynchronously (ghostty sends its DA and
	// XTVERSION replies on separate reads), so a late or fragmented reply
	// frame must be swallowed here instead of leaking into the editor as fake
	// keystrokes (the ">|ghostty 1.3.1" text polluting the composer). In lax
	// mode reply frames are consumed silently, everything else is forwarded,
	// and the probe never reports done again.
	lax bool
}

func newProbeState(deadline time.Time) *probeState {
	return &probeState{deadline: deadline}
}

// feed consumes a new chunk of stdin bytes. It returns leading bytes that are
// user input (not part of any reply frame) and whether the probe just
// completed — callers finalize capabilities on done, after which the probe
// switches to lax mode and keeps filtering instead of being torn down.
func (p *probeState) feed(data []byte) (leading []byte, done bool) {
	if len(data) > 0 {
		p.buf = append(p.buf, data...)
	}
	for {
		b := p.buf
		if len(b) == 0 {
			break
		}
		switch {
		case bytes.HasPrefix(b, dcsXTSeq):
			// DCS XTVERSION: consume until ST, record the payload.
			rest := b[len(dcsXTSeq):]
			k := bytes.Index(rest, dcsST)
			if k < 0 {
				// Truncated: wait for the ST terminator, unless the frame is
				// too big to ever be a reply — then return it as input so the
				// user's keystrokes are never swallowed indefinitely.
				if len(b) > LaxProbeMaxBuf {
					return p.drain(), !p.lax && p.done
				}
				return leading, !p.lax && p.done
			}
			if !p.lax {
				p.name = pruneXTName(string(rest[:k]))
				p.responded = true
				p.done = true
			}
			p.buf = rest[k+len(dcsST):]
		case b[0] == 0x1b:
			n, isDA := scanCSI(b)
			if n == 0 {
				// Truncated CSI, wait for more bytes (bounded as above).
				if len(b) > LaxProbeMaxBuf {
					return p.drain(), !p.lax && p.done
				}
				return leading, !p.lax && p.done
			}
			if isDA {
				p.buf = b[n:]
				if !p.lax {
					p.responded = true
					p.done = true
				}
			} else {
				// A complete, non-DA sequence (e.g. a cursor movement key the
				// user typed during the window): forward it verbatim so no
				// input is lost.
				leading = append(leading, b[:n]...)
				p.buf = b[n:]
			}
		default:
			// Not the start of a frame: hand it back to the input pipeline.
			leading = append(leading, b[0])
			p.buf = b[1:]
		}
	}
	return leading, !p.lax && p.done
}

// drain returns every remaining buffered byte as user input and clears the
// buffer. Call after feed reports done (or after the deadline) to avoid
// swallowing keystrokes.
func (p *probeState) drain() []byte {
	b := p.buf
	p.buf = nil
	return b
}

// scanCSI consumes one single-byte-final CSI sequence from b, which must start
// with 0x1b. It returns:
//   - (0, false) if the sequence is truncated and more bytes are needed;
//   - (n, isDA)  otherwise, where n is the consumed byte count and isDA
//     reports whether the final byte was 'c' with parameters (a DA reply).
//
// A bare ESC (or an ESC followed by a non-'[' byte) is consumed as a single
// lead byte so stray escape prefixes don't stall the probe.
func scanCSI(b []byte) (int, bool) {
	if len(b) == 0 {
		return 0, false
	}
	if b[0] != 0x1b {
		return 1, false
	}
	if len(b) < 2 {
		return 0, false
	}
	if b[1] != '[' {
		return 1, false // bare ESC or 8-bit alt prefix: consume the lead byte
	}
	i := 2
	sawParam := false
	for i < len(b) {
		c := b[i]
		switch {
		case c >= 0x30 && c <= 0x3F: // params / intermediates incl. '?', '>', ';'
			sawParam = true
			i++
		case c >= 0x40 && c <= 0x7E: // final byte
			return i + 1, c == 'c' && sawParam
		default:
			return 1, false // control or high byte inside CSI: treat ESC as user input
		}
	}
	return 0, false // truncated
}

// pruneXTName trims whitespace from an XTVERSION payload.
func pruneXTName(s string) string {
	return strings.TrimSpace(s)
}

// normalizeBrand maps an XTVERSION payload to a canonical brand token.
func normalizeBrand(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "ghostty"):
		return "ghostty"
	case strings.Contains(lower, "kitty"):
		return "kitty"
	case strings.Contains(lower, "wezterm"):
		return "wezterm"
	case strings.Contains(lower, "foot"):
		return "foot"
	case strings.Contains(lower, "alacritty"):
		return "alacritty"
	case strings.Contains(lower, "iterm"):
		return "iterm"
	case strings.Contains(lower, "xterm"):
		return "xterm"
	case strings.Contains(lower, "tmux"):
		return "tmux"
	case strings.Contains(lower, "screen"):
		return "screen"
	case strings.Contains(lower, "vte"):
		return "vte"
	}
	return ""
}

// supportsTrueColorEnv reports 24-bit color support from the environment.
func supportsTrueColorEnv() bool {
	switch os.Getenv("COLORTERM") {
	case "truecolor", "24bit":
		return true
	}
	switch os.Getenv("TERM") {
	case "xterm-truecolor", "screen-truecolor", "tmux-truecolor",
		"xterm-kitty", "xterm-ghostty", "alacritty-direct":
		return true
	}
	return false
}

// terminalBrandFromEnv derives the normalized brand token from the
// environment. It only recognizes terminals that identify themselves via
// dedicated env variables; otherwise it returns "".
func terminalBrandFromEnv() string {
	switch {
	case os.Getenv("KITTY_WINDOW_ID") != "":
		return "kitty"
	case os.Getenv("TERM_PROGRAM") == "WezTerm":
		return "wezterm"
	case os.Getenv("TERM_PROGRAM") == "ghostty" ||
		os.Getenv("GHOSTTY_RESOURCES_DIR") != "":
		return "ghostty"
	case os.Getenv("FOOT_VERSION") != "":
		return "foot"
	}
	switch os.Getenv("TERM") {
	case "xterm-kitty":
		return "kitty"
	case "xterm-ghostty":
		return "ghostty"
	}
	return ""
}

// capabilitiesFromEnv resolves capabilities from env heuristics only.
func capabilitiesFromEnv() Capabilities {
	c := Capabilities{
		KittyKeyboard: TerminalSupportsKittyKeyboard(),
		TrueColor:     supportsTrueColorEnv(),
		Brand:         terminalBrandFromEnv(),
	}
	return c
}

// capabilitiesFromProbe builds the final capability set from a completed
// probe, keeping env-derived values for features the probe cannot observe.
func capabilitiesFromProbe(p *probeState, env Capabilities) Capabilities {
	caps := env
	caps.Probed = true
	caps.Responded = p.responded
	caps.Name = p.name
	if b := normalizeBrand(p.name); b != "" {
		caps.Brand = b
	}
	// Terminals that answer XTVERSION with a known modern brand advertise
	// kitty keyboard protocol support and 24-bit color. Muxes are the
	// exception: kitty keyboard cannot be negotiated through tmux/screen
	// without a passthrough configuration, so a mux brand downgrades the env
	// conclusion rather than confirming it.
	switch caps.Brand {
	case "kitty", "wezterm", "ghostty", "foot", "alacritty", "iterm":
		caps.KittyKeyboard = true
		caps.TrueColor = true
	case "tmux", "screen":
		caps.KittyKeyboard = false
	}
	return caps
}
