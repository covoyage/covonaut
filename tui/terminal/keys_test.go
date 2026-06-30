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
