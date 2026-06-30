package core

import "testing"

func TestFuzzyFilterSortsByScore(t *testing.T) {
	cands := []string{"helloWorld", "hello", "hi-world", "abc"}
	res := FuzzyFilter("hw", cands)
	if len(res) < 2 {
		t.Fatalf("expected >=2 matches, got %d", len(res))
	}
	// Top match should be one that starts with 'h' and contains 'w'.
	top := res[0].Original
	if top != "helloWorld" && top != "hi-world" {
		t.Fatalf("unexpected top match: %q (res=%v)", top, res)
	}
	// 'abc' should be filtered out.
	for _, m := range res {
		if m.Original == "abc" {
			t.Fatalf("abc shouldn't match")
		}
	}
}

func TestFuzzyFilterEmptyQuery(t *testing.T) {
	cands := []string{"a", "b"}
	res := FuzzyFilter("", cands)
	if len(res) != 2 {
		t.Fatalf("empty query should keep all, got %d", len(res))
	}
}
