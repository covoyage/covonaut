package terminal

import "testing"

func TestMatchesKeyBasic(t *testing.T) {
	cases := []struct {
		data string
		key  KeyID
		want bool
	}{
		{"\r", "enter", true},
		{"\n", "enter", true},
		{"\x1b", "escape", true},
		{"\t", "tab", true},
		{"\x7f", "backspace", true},
		{"\x03", "ctrl+c", true},
		{"\x1b[A", "up", true},
		{"\x1b[B", "down", true},
		{"\x1b[C", "right", true},
		{"\x1b[D", "left", true},
		{"\x1b[H", "home", true},
		{"\x1b[F", "end", true},
		{"\x1b[3~", "delete", true},
		{"\x1b[5~", "pageUp", true},
		{"\x1b[6~", "pageDown", true},
		{"\x1bOP", "f1", true},
		{"a", "a", true},
		{"A", "A", true},
		{"\x1bb", "alt+b", true},
	}
	for _, c := range cases {
		if got := MatchesKey(c.data, c.key); got != c.want {
			t.Errorf("MatchesKey(%q, %q) = %v, want %v", c.data, c.key, got, c.want)
		}
	}
}

func TestKittyCSIu(t *testing.T) {
	// CSI 13 u = enter (Kitty format)
	if !MatchesKey("\x1b[13u", "enter") {
		t.Error("expected kitty CSI 13 u → enter")
	}
	// CSI 99 ; 5 u = ctrl+c
	if !MatchesKey("\x1b[99;5u", "ctrl+c") {
		t.Error("expected kitty CSI 99;5 u → ctrl+c")
	}
}

func TestParseKeysPrintable(t *testing.T) {
	keys := ParseKeys("hi中")
	if len(keys) != 3 {
		t.Fatalf("want 3 keys, got %d", len(keys))
	}
	if keys[0].Name != "h" || keys[1].Name != "i" || keys[2].Name != "中" {
		t.Errorf("unexpected names: %v %v %v", keys[0].Name, keys[1].Name, keys[2].Name)
	}
}

// Modifier-noise tolerance: Caps/NumLk/Meta/Hyper bits reported by macOS
// terminals and exotic emulators must not break a clean shortcut match.
func TestMatchesKeyModifierNoise(t *testing.T) {
	// CSI <code>;<mod+1>u — the listed mod codes include noise bits.
	pos := []struct {
		data string
		key  KeyID
	}{
		{"\x1b[99;5u", "ctrl+c"},    // kitty ctrl+c, no noise
		{"\x1b[99;69u", "ctrl+c"},   // + caps lock (64)
		{"\x1b[99;133u", "ctrl+c"},  // + num lock (128)
		{"\x1b[99;37u", "ctrl+c"},   // + meta (32)
		{"\x1b[99;21u", "ctrl+c"},   // + hyper (16)
		{"\x1b[99;85u", "ctrl+c"},   // + caps(64)+hyper(16)
	}
	for _, c := range pos {
		if !MatchesKey(c.data, c.key) {
			t.Errorf("MatchesKey(%q, %q) = false, want true", c.data, c.key)
		}
	}
	// Real modifier differences must still be enforced (multi-char names,
	// where Shift is not folded into the rune case).
	neg := []struct {
		data string
		key  KeyID
	}{
		{"\x1b[13;3u", "enter"},     // alt+enter, not enter
		{"\x1b[13;6u", "ctrl+enter"}, // ctrl+shift+enter, not ctrl+enter
		{"\x1b[9;2u", "tab"},         // shift+tab, not tab
		{"\x1b[1;5p", "ctrl+p"},      // unknown final byte, no Name match
	}
	for _, c := range neg {
		if MatchesKey(c.data, c.key) {
			t.Errorf("MatchesKey(%q, %q) = true, want false", c.data, c.key)
		}
	}
	// A pure caps-lock bit collapses to the plain key.
	if !MatchesKey("\x1b[99;65u", "c") {
		t.Error("caps-lock-bit-only codepoint 99 must match plain \"c\"")
	}
	if MatchesKey("\x1b[99;65u", "ctrl+c") {
		t.Error("caps-lock-bit-only must not match ctrl+c")
	}
}

// Bindings that explicitly request Meta must not collapse to the unchorded
// key. Stripping Meta from both sides made "meta+a" match "a" and
// "ctrl+meta+a" match ctrl+a, which stole typing and emacs line-start.
func TestMatchesKeyExplicitMetaNotPlain(t *testing.T) {
	neg := []struct {
		data string
		key  KeyID
	}{
		{"a", "meta+a"},
		{"a", "alt+a"},
		{"a", "super+a"},
		{"\x01", "ctrl+meta+a"}, // ctrl+a must stay line-start, not select-all
		{"\x01", "meta+a"},
		{"\x01", "alt+a"},
	}
	for _, c := range neg {
		if MatchesKey(c.data, c.key) {
			t.Errorf("MatchesKey(%q, %q) = true, want false", c.data, c.key)
		}
	}
	pos := []struct {
		data string
		key  KeyID
	}{
		{"a", "a"},
		{"\x01", "ctrl+a"},
		{"\x1ba", "alt+a"},
		{"\x1b[97;33u", "meta+a"},      // kitty 'a' + meta
		{"\x1b[97;37u", "ctrl+meta+a"}, // kitty 'a' + ctrl+meta
		{"\x1b[97;9u", "super+a"},      // kitty 'a' + super
	}
	for _, c := range pos {
		if !MatchesKey(c.data, c.key) {
			t.Errorf("MatchesKey(%q, %q) = false, want true", c.data, c.key)
		}
	}
}

// C0 canonical mapping: the rare control bytes beyond ctrl+a..z resolve to
// their canonical escape names.
func TestParseC0Controls(t *testing.T) {
	cases := []struct {
		byte rune
		key  KeyID
	}{
		{0x1C, "ctrl+\\"},
		{0x1D, "ctrl+]"},
		{0x1E, "ctrl+^"},
		{0x1F, "ctrl+_"},
		{0x00, "ctrl+ "},
		{0x14, "ctrl+t"},
		{0x01, "ctrl+a"},
		{0x1A, "ctrl+z"},
	}
	for _, c := range cases {
		if !MatchesKey(string(c.byte), c.key) {
			t.Errorf("MatchesKey(%U, %q) = false, want true", c.byte, c.key)
		}
	}
}

// macOS terminals sometimes deliver Alt+Enter as \x1b\r and Alt+backspace as
// \x1b\x7f; both must stay predictable.
func TestParseMetaControlEscapes(t *testing.T) {
	if !MatchesKey("\x1b\r", "alt+enter") {
		t.Error("\\x1b\\r must match alt+enter")
	}
	if !MatchesKey("\x1b\x7f", "alt+backspace") {
		t.Error("\\x1b\\x7f must match alt+backspace")
	}
	if !MatchesKey("\x1b\x1c", "alt+ctrl+\\") {
		t.Error("\\x1b\\x1c must match alt+ctrl+\\")
	}
}
