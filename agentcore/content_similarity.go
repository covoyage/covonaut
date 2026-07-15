package agentcore

// contentSimilar reports whether a and b are similar enough to be considered
// the same underlying content, using the Ratcliff/Obershelp "gestalt pattern
// matching" ratio — the same algorithm behind Python's
// difflib.SequenceMatcher.ratio(). This catches near-duplicate text that
// differs only in a few words or characters (e.g. minor wording variation),
// not just byte-identical matches, which a plain == comparison would miss.
//
// ratio is the similarity threshold in [0, 1]; a and b are considered
// similar when 2*M/(len(a)+len(b)) >= ratio, where M is the total number of
// matching characters found by recursively locating the longest common
// contiguous substring.
func contentSimilar(a, b string, ratio float64) bool {
	if a == "" && b == "" {
		return true
	}
	if a == "" || b == "" {
		return false
	}
	m := matchingCharCount(a, b)
	r := 2 * float64(m) / float64(len(a)+len(b))
	return r >= ratio
}

// matchingCharCount returns the total number of matching characters between
// a and b: the length of their longest common contiguous substring, plus
// (recursively) the matching characters in the substrings before and after
// that match on both sides.
func matchingCharCount(a, b string) int {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	aStart, bStart, length := longestMatch(a, b)
	if length == 0 {
		return 0
	}
	total := length
	total += matchingCharCount(a[:aStart], b[:bStart])
	total += matchingCharCount(a[aStart+length:], b[bStart+length:])
	return total
}

// longestMatch finds the longest contiguous substring common to a and b,
// returning its start index in a, its start index in b, and its length.
// Uses the standard O(len(a)*avg-matches-per-char) dynamic-programming
// approach (the core of the Ratcliff/Obershelp algorithm): for each position
// in a, extend any match ending at the previous position in b by one.
func longestMatch(a, b string) (aStart, bStart, length int) {
	bPositions := make(map[byte][]int, len(b))
	for i := 0; i < len(b); i++ {
		bPositions[b[i]] = append(bPositions[b[i]], i)
	}

	bestLen := 0
	bestA, bestB := 0, 0
	j2len := make(map[int]int)
	for i := 0; i < len(a); i++ {
		newJ2len := make(map[int]int, len(j2len))
		for _, j := range bPositions[a[i]] {
			k := j2len[j-1] + 1
			newJ2len[j] = k
			if k > bestLen {
				bestLen = k
				bestA = i - k + 1
				bestB = j - k + 1
			}
		}
		j2len = newJ2len
	}
	return bestA, bestB, bestLen
}
