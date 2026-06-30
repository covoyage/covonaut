package fuzzy

import (
	"testing"
)

func TestNormalizeForMatch(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "hello"},
		{"  spaced  ", "  spaced"},
		{"smart'quote", "smart'quote"},
		{"\u2018curly\u2019", "'curly'"},
		{"\u201Cdouble\u201D", `"double"`},
		{"\u2013dash\u2014", "-dash-"},
		{"line1\nline2  ", "line1\nline2"},
		{"\u00A0nbsp", " nbsp"},
		{"\rskip", "skip"},
	}
	for _, tc := range tests {
		got := NormalizeForMatch(tc.input)
		if got != tc.expected {
			t.Errorf("NormalizeForMatch(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestFind(t *testing.T) {
	content := "The quick brown fox"

	// exact match
	start, end, found := Find(content, "brown")
	if !found || content[start:end] != "brown" {
		t.Errorf("Find exact: got %d,%d,%v", start, end, found)
	}

	// no match
	if _, _, found := Find(content, "purple"); found {
		t.Error("Find no-match: should be false")
	}

	// normalized match with smart quotes
	content2 := "He said \u201Chello\u201D world"
	start, end, found = Find(content2, `"hello"`)
	if !found || content2[start:end] != "\u201Chello\u201D" {
		t.Errorf("Find normalized quotes: got %d,%d,%v", start, end, found)
	}
}

func TestReplace(t *testing.T) {
	// exact replace
	result, ok := Replace("hello world", "world", "there")
	if !ok || result != "hello there" {
		t.Errorf("Replace exact: got %q, %v", result, ok)
	}

	// fuzzy replace with normalized quote
	result, ok = Replace("smart\u2019quote", "'quote", "replaced")
	if !ok || result != "smartreplaced" {
		t.Errorf("Replace fuzzy: got %q, want %q", result, "smartreplaced")
	}

	// no match
	result, ok = Replace("hello", "xyz", "abc")
	if ok || result != "hello" {
		t.Errorf("Replace no-match: got %q, %v", result, ok)
	}
}

func TestLevenshteinDistance(t *testing.T) {
	tests := []struct {
		a, b string
		dist int64
	}{
		{"", "", 0},
		{"a", "", 1},
		{"", "a", 1},
		{"abc", "abc", 0},
		{"kitten", "sitting", 3},
		{"abc", "xyz", 3},
		{"hello", "HELLO", 5}, // case-sensitive
	}
	for _, tc := range tests {
		got := LevenshteinDistance(tc.a, tc.b)
		if got != tc.dist {
			t.Errorf("LevenshteinDistance(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.dist)
		}
	}
}

func TestMapNormalizedOffset(t *testing.T) {
	original := "hello\r\nworld"
	normalized := NormalizeForMatch(original)
	// "hello\nworld"
	offset := mapNormalizedOffset(original, normalized, 7) // after "hello\nw"
	if offset != 8 {                                       // original has \r so "hello\r\nw" = 8
		t.Errorf("mapNormalizedOffset: got %d, want %d", offset, 8)
	}
}
