package core

import (
	"sort"
	"strings"
	"unicode"
)

// ---------------------------------------------------------------------------
// Autocomplete-style fuzzy matcher.
//
// A lightweight implementation inspired by fzf / Sublime / VSCode. The
// existing repo-level fuzzy package is for whitespace-tolerant content diff
// search, which is a different problem.
// ---------------------------------------------------------------------------

// FuzzyMatch describes how a query matched a candidate.
type FuzzyMatch struct {
	Score    int64
	Indexes  []int64 // positions of matched runes in the candidate
	Original string
}

// FuzzyMatchOne scores `query` against `candidate`. Returns a zero-value
// match with Score < 0 when no match is possible.
//
// Scoring rewards:
//   - earlier matches
//   - case matches
//   - consecutive runs
//   - word-boundary or CamelCase starts
//
// Scoring penalises:
//   - longer distances between matched characters
func FuzzyMatchOne(query, candidate string) FuzzyMatch {
	if query == "" {
		return FuzzyMatch{Score: 0, Original: candidate}
	}
	q := []rune(query)
	c := []rune(candidate)
	qi := 0
	var indexes []int64
	var score int64 = 0
	lastMatch := int64(-1)

	for i := 0; i < len(c) && qi < len(q); i++ {
		cr := c[i]
		qr := q[qi]
		if equalFold(cr, qr) {
			s := int64(16)
			if cr == qr {
				s += 4
			}
			if lastMatch == int64(i)-1 {
				s += 12
			}
			if i == 0 || isBoundary(c, i) {
				s += 10
			}
			// penalise gap since last match
			if lastMatch >= 0 {
				gap := int64(i) - lastMatch - 1
				if gap > 0 {
					s -= gap
				}
			} else {
				s -= int64(i) // earlier is better
			}
			score += s
			indexes = append(indexes, int64(i))
			lastMatch = int64(i)
			qi++
		}
	}
	if qi < len(q) {
		return FuzzyMatch{Score: -1}
	}
	return FuzzyMatch{Score: score, Indexes: indexes, Original: candidate}
}

// FuzzyFilter filters and sorts candidates by score against the query.
// Candidates with no match are excluded. Results are sorted descending by
// score (ties broken by candidate length then original order).
func FuzzyFilter(query string, candidates []string) []FuzzyMatch {
	out := make([]FuzzyMatch, 0, len(candidates))
	type scored struct {
		m     FuzzyMatch
		index int64
	}
	all := make([]scored, 0, len(candidates))
	for i, c := range candidates {
		m := FuzzyMatchOne(query, c)
		if m.Score < 0 {
			continue
		}
		all = append(all, scored{m: m, index: int64(i)})
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].m.Score != all[j].m.Score {
			return all[i].m.Score > all[j].m.Score
		}
		if len(all[i].m.Original) != len(all[j].m.Original) {
			return len(all[i].m.Original) < len(all[j].m.Original)
		}
		return all[i].index < all[j].index
	})
	for _, s := range all {
		out = append(out, s.m)
	}
	return out
}

// HighlightMatches emits the candidate with matched characters wrapped by
// markFn. `indexes` comes from a FuzzyMatch produced against the same
// candidate.
func HighlightMatches(candidate string, indexes []int64, markFn func(string) string) string {
	if markFn == nil || len(indexes) == 0 {
		return candidate
	}
	set := make(map[int64]bool, len(indexes))
	for _, i := range indexes {
		set[i] = true
	}
	var b strings.Builder
	for i, r := range candidate {
		if set[int64(i)] {
			b.WriteString(markFn(string(r)))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func equalFold(a, b rune) bool {
	return unicode.ToLower(a) == unicode.ToLower(b)
}

func isBoundary(runes []rune, i int) bool {
	if i == 0 {
		return true
	}
	prev := runes[i-1]
	cur := runes[i]
	if prev == ' ' || prev == '-' || prev == '_' || prev == '.' || prev == '/' {
		return true
	}
	if unicode.IsLower(prev) && unicode.IsUpper(cur) {
		return true
	}
	return false
}
