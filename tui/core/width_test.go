package core

import "testing"

func TestVisibleWidth(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"", 0},
		{"hello", 5},
		{"中文", 4},
		{"a中b", 4},
		{"\x1b[31mhello\x1b[0m", 5},
		{"emoji: 🌍", 9}, // 7 ASCII + space=1 is already counted, emoji=2
		{"\x1b]8;;http://a\x07link\x1b]8;;\x07", 4},
	}
	for _, c := range cases {
		got := VisibleWidth(c.in)
		if got != c.want {
			t.Errorf("VisibleWidth(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestTruncateToWidth(t *testing.T) {
	if got := TruncateToWidth("hello world", 8, "…"); VisibleWidth(got) > 8 {
		t.Errorf("truncation exceeded width: %q (width=%d)", got, VisibleWidth(got))
	}
	if got := TruncateToWidth("中文测试字符串", 6, "…"); VisibleWidth(got) > 6 {
		t.Errorf("cjk truncation exceeded width: %q (width=%d)", got, VisibleWidth(got))
	}
	if got := TruncateToWidth("short", 100, "…"); got != "short" {
		t.Errorf("expected unchanged, got %q", got)
	}
}

func TestPadToWidth(t *testing.T) {
	got := PadToWidth("hi", 5)
	if VisibleWidth(got) != 5 {
		t.Errorf("PadToWidth visible width = %d, want 5 (got %q)", VisibleWidth(got), got)
	}
}

func TestWrapAnsi(t *testing.T) {
	lines := WrapAnsi("the quick brown fox jumps over the lazy dog", 10)
	for i, l := range lines {
		if VisibleWidth(l) > 10 {
			t.Errorf("line %d exceeds width: %q (%d cells)", i, l, VisibleWidth(l))
		}
	}
	if len(lines) < 2 {
		t.Errorf("expected multiple wrapped lines, got %d", len(lines))
	}
}

func TestSliceByColumn(t *testing.T) {
	if got := SliceByColumn("hello world", 6, 11); got != "world" {
		t.Errorf("SliceByColumn(6,11) = %q, want %q", got, "world")
	}
	if got := SliceByColumn("中文abc", 2, 5); got != "文a" {
		// "中"=2, "文"=2, "a"=1 -> columns [0,2)=中 [2,4)=文 [4,5)=a
		t.Errorf("SliceByColumn cjk = %q, want %q", got, "文a")
	}
}
