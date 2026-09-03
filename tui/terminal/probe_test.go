package terminal

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func feedAll(p *probeState, inputs ...string) ([]byte, bool) {
	var all []byte
	done := false
	for _, in := range inputs {
		var lead []byte
		lead, done = p.feed([]byte(in))
		all = append(all, lead...)
	}
	return all, done
}

func TestProbeStateKittyXTVERSION(t *testing.T) {
	p := newProbeState(time.Now().Add(ProbeTimeout))
	lead, done := p.feed([]byte("\x1bP>|kitty-0.35.0\x1b\\"))
	if !done {
		t.Fatalf("expected done")
	}
	if len(lead) != 0 {
		t.Errorf("unexpected user bytes: %q", lead)
	}
	if !p.responded {
		t.Errorf("responded not set")
	}
	if p.name != "kitty-0.35.0" {
		t.Errorf("name = %q, want kitty-0.35.0", p.name)
	}
	if got := normalizeBrand(p.name); got != "kitty" {
		t.Errorf("brand = %q, want kitty", got)
	}
}

func TestProbeStateWezTermXTVERSIONChunked(t *testing.T) {
	p := newProbeState(time.Now().Add(ProbeTimeout))
	// Split the DCS payload across feed calls (as real reads often do).
	var lead []byte
	for _, piece := range []string{"\x1bP>|WezTer", "m-20230428-144941-g39eb64", "\x1b\\"} {
		l, done := p.feed([]byte(piece))
		lead = append(lead, l...)
		if strings.Contains(piece, "\\") && !done {
			t.Fatalf("expected done after terminator")
		}
	}
	if !p.responded || p.name != "WezTerm-20230428-144941-g39eb64" {
		t.Fatalf("name = %q responded = %v", p.name, p.responded)
	}
	if got := normalizeBrand(p.name); got != "wezterm" {
		t.Errorf("brand = %q, want wezterm", got)
	}
}

func TestProbeStateDA2Reply(t *testing.T) {
	p := newProbeState(time.Now().Add(ProbeTimeout))
	lead, done := p.feed([]byte("\x1b[>1;2000;0c"))
	if !done {
		t.Fatalf("expected done")
	}
	if !p.responded {
		t.Errorf("responded not set for DA2")
	}
	if len(lead) != 0 {
		t.Errorf("unexpected user bytes: %q", lead)
	}
}

func TestProbeStateDA1Reply(t *testing.T) {
	p := newProbeState(time.Now().Add(ProbeTimeout))
	_, done := p.feed([]byte("\x1b[?1;2c"))
	if !done {
		t.Fatalf("expected done")
	}
	if !p.responded {
		t.Errorf("responded not set for DA1")
	}
}

// Interleaved user keystrokes must survive the probe untouched.
func TestProbeStateInterleavedInput(t *testing.T) {
	p := newProbeState(time.Now().Add(ProbeTimeout))
	lead, done := p.feed([]byte("hel\x1bP>|kitty-0.35.0\x1b\\lo"))
	if !done {
		t.Fatalf("expected done")
	}
	rest := p.drain()
	if got := string(append(lead, rest...)); got != "hello" {
		t.Errorf("user bytes = %q, want \"hello\"", got)
	}
}

func TestProbeStateArrowKeysIgnored(t *testing.T) {
	// An arrow key typed during the probe window is not a DA reply; it must
	// be forwarded verbatim (input is never lost).
	cases := []struct {
		in, wantLead string
	}{
		{"\x1b[A", "\x1b[A"},
		{"\x1b[1~", "\x1b[1~"}, // home key
	}
	for _, c := range cases {
		p := newProbeState(time.Now().Add(ProbeTimeout))
		lead, done := p.feed([]byte(c.in))
		if done {
			t.Fatalf("%q: arrow key must not complete the probe", c.in)
		}
		if got := string(lead); got != c.wantLead {
			t.Errorf("%q: leading = %q, want %q", c.in, got, c.wantLead)
		}
	}
}

func TestProbeStateBareESCPassedThrough(t *testing.T) {
	p := newProbeState(time.Now().Add(ProbeTimeout))
	lead, done := p.feed([]byte("\x1b\x1bX"))
	if done {
		t.Fatalf("bare ESC must not complete the probe")
	}
	if got := string(lead); got != "\x1b\x1bX" {
		t.Errorf("leading = %q, want ESC ESC X", got)
	}
}

func TestProbeStateBareESCPendingUntilMoreInput(t *testing.T) {
	// A lone ESC is ambiguous (could start a CSI), so it waits for the next
	// chunk; the deadline drain then hands it back.
	p := newProbeState(time.Now().Add(-time.Second))
	lead, done := p.feed([]byte("\x1b"))
	if done {
		t.Fatalf("bare ESC must not complete the probe")
	}
	if len(lead) != 0 {
		t.Errorf("leading = %q, want empty", lead)
	}
	if got := string(p.drain()); got != "\x1b" {
		t.Errorf("drained = %q, want ESC", got)
	}
}

func TestProbeStateTimeoutDrainsUserInput(t *testing.T) {
	p := newProbeState(time.Now().Add(-time.Second)) // expired
	lead, done := p.feed([]byte("abc"))
	if done {
		t.Fatalf("expired probe feed must report nothing new")
	}
	if got := string(lead); got != "abc" {
		t.Errorf("leading = %q, want abc", got)
	}
}

// Ghostty answers each query on a separate read: DA1 arrives first and
// completes the probe, then DA2 and the XTVERSION DCS land afterwards. The
// DCS payload ("|ghostty 1.3.1") previously leaked into the editor as fake
// keystrokes. In lax mode, late frames must be swallowed, not forwarded.
func TestProbeLaxSwallowsLateGhosttyReplies(t *testing.T) {
	p := newProbeState(time.Now().Add(ProbeTimeout))

	// Frame 1 completes the probe (caller flips to lax).
	lead, done := p.feed([]byte("\x1b[?1;2c"))
	if !done {
		t.Fatal("expected probe to complete on DA1")
	}
	if len(lead) != 0 {
		t.Fatalf("DA1 reply leaked as user bytes: %q", lead)
	}
	p.lax = true

	// Late DA2 replay.
	lead, done = p.feed([]byte("\x1b[>0;356;0c"))
	if done {
		t.Fatal("lax probe must not report done again")
	}
	if len(lead) != 0 {
		t.Fatalf("late DA2 leaked as user bytes: %q", lead)
	}

	// Late, fragmented XTVERSION DCS — the payload that became ">|ghostty".
	payload := []byte("\x1bP>|ghostty 1.3.1\x1b\\")
	var all []byte
	for _, piece := range [][]byte{payload[:8], payload[8:]} {
		lead, done = p.feed(piece)
		if done {
			t.Fatal("lax probe must not report done on fragmented XTVERSION")
		}
		all = append(all, lead...)
	}
	if len(all) != 0 {
		t.Fatalf("late XTVERSION DCS leaked as user bytes: %q", all)
	}
}

// The lax probe is transparent to real input: keystrokes typed after the
// probe completed still reach the editor untouched, including ESC-prefixed
// key sequences.
func TestProbeLaxForwardsUserInputAfterCompletion(t *testing.T) {
	p := newProbeState(time.Now().Add(-time.Second))
	p.lax = true

	lead, _ := p.feed([]byte("hello"))
	if got := string(lead); got != "hello" {
		t.Fatalf("plain text = %q, want hello", got)
	}
	lead, _ = p.feed([]byte("\x1b[A")) // arrow key after completion
	if got := string(lead); got != "\x1b[A" {
		t.Fatalf("arrow key = %q, want ESC [ A", got)
	}
}

// An unterminated reply frame in lax mode must not swallow keystrokes
// forever: once the buffered junk exceeds the cap it is handed back wholesale.
func TestProbeLaxFlushesOversizedJunk(t *testing.T) {
	p := newProbeState(time.Now().Add(-time.Second))
	p.lax = true

	// A DCS XTVERSION frame whose payload never terminates and is far larger
	// than any real reply could be.
	frame := append(append([]byte(nil), dcsXTSeq...), bytes.Repeat([]byte{'x'}, LaxProbeMaxBuf)...)
	lead, done := p.feed(frame)
	if done {
		t.Fatal("junk must not complete the probe")
	}
	if !bytes.Equal(lead, frame) {
		t.Fatalf("oversized junk must be returned wholesale, got %d of %d bytes",
			len(lead), len(frame))
	}
}

func TestScanCSI(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		isDA bool
	}{
		{"\x1bA", 1, false},    // ESC + non-'[' byte: only the ESC is a lead byte
		{"\x1b[A", 3, false},   // CSI arrow key
		{"\x1b[?", 0, false},   // truncated
		{"\x1b[1;2c", 6, true}, // DA1 reply
		{"\x1b[>c", 4, true},   // DA2 minimal
		{"\x1b[>", 0, false},   // truncated DA2
		{"\x1b", 0, false},     // bare ESC waits
		{"\x1b[0m", 4, false},  // SGR (terminal-side color reset)
	}
	for _, c := range cases {
		n, isDA := scanCSI([]byte(c.in))
		if n != c.n || isDA != c.isDA {
			t.Errorf("scanCSI(%q) = (%d,%v), want (%d,%v)", c.in, n, isDA, c.n, c.isDA)
		}
	}
}

func TestNormalizeBrand(t *testing.T) {
	cases := map[string]string{
		"kitty-0.35.0":                    "kitty",
		"ghostty-1.0.1":                   "ghostty",
		"WezTerm-20230428-144941-g39eb64": "wezterm",
		"XTerm(369)":                      "xterm",
		"tmux 3.5a":                       "tmux",
		"":                                "",
	}
	for in, want := range cases {
		if got := normalizeBrand(in); got != want {
			t.Errorf("normalizeBrand(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCapabilitiesFromProbe(t *testing.T) {
	p := newProbeState(time.Now())
	p.feed([]byte("\x1bP>|kitty-0.35.0\x1b\\"))
	caps := capabilitiesFromProbe(p, capabilitiesFromEnv())
	if !caps.Probed || !caps.Responded {
		t.Fatalf("probed/responded not forwarded")
	}
	if !caps.KittyKeyboard {
		t.Errorf("kitty keyboard not derived from kitty brand")
	}
	if !caps.TrueColor {
		t.Errorf("truecolor not derived from kitty brand")
	}
	if caps.Brand != "kitty" {
		t.Errorf("brand = %q", caps.Brand)
	}

	// tmux/screen must not claim KKP just because they answer XTVERSION.
	ptm := newProbeState(time.Now())
	ptm.feed([]byte("\x1bP>|tmux 3.5a\x1b\\"))
	cm := capabilitiesFromProbe(ptm, capabilitiesFromEnv())
	if cm.KittyKeyboard {
		t.Errorf("tmux brand must not enable KKP")
	}
}

func TestCapabilityQueriesWellFormed(t *testing.T) {
	q := terminalCapabilityQueries()
	// Every fragment is a complete CSI; no trailing partial sequence.
	for i := 0; i+1 < len(q); i++ {
		if q[i] == 0x1b {
			if q[i+1] != '[' {
				t.Fatalf("query byte %d: 0x1b not followed by '['", i)
			}
		}
	}
	if !strings.HasPrefix(q, "\x1b[") || !bytes.Equal([]byte(q), []byte("\x1b[c\x1b[>c\x1b[>q")) {
		t.Fatalf("unexpected query: %q", q)
	}
}
