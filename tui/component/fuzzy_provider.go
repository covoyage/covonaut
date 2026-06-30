package component

import (
	"strings"

	"github.com/covoyage/covonaut/fuzzy"
	core "github.com/covoyage/covonaut/tui/core"
)

// ---------------------------------------------------------------------------
// Bridge from the top-level fuzzy package into TUI constructs.
//
// The fuzzy package (fuzzy/fuzzy.go) specialises in whitespace-tolerant
// substring search — useful for letting the user paste a snippet and locate
// its exact position inside a larger document, even if whitespace differs.
//
// This file exposes that capability as TUI primitives:
//   - NormalizeForMatch: passthrough so components can normalise inputs.
//   - SubstringFuzzyMatch: reports whether query appears inside candidate.
//   - SubstringFuzzyFilter: filters candidates that contain query.
//   - FuzzyContentProvider: AutocompleteProvider that searches inside a
//     corpus of content (e.g. chat history, file body) and surfaces the
//     matched excerpt.
// ---------------------------------------------------------------------------

// NormalizeForMatch re-exports fuzzy.NormalizeForMatch so TUI consumers
// don't have to import the fuzzy package directly.
func NormalizeForMatch(s string) string { return fuzzy.NormalizeForMatch(s) }

// SubstringFuzzyMatch reports whether `query` appears inside `candidate`
// after normalising both sides (trailing whitespace collapsed, smart
// punctuation replaced with ASCII, etc.). Returns the matched byte range
// and ok=true on success.
func SubstringFuzzyMatch(candidate, query string) (start, end int64, ok bool) {
	return fuzzy.Find(candidate, query)
}

// SubstringFuzzyFilter filters candidates that contain `query` under the
// normalised substring rules, preserving original order.
func SubstringFuzzyFilter(query string, candidates []string) []string {
	if query == "" {
		out := make([]string, len(candidates))
		copy(out, candidates)
		return out
	}
	var out []string
	for _, c := range candidates {
		if _, _, ok := fuzzy.Find(c, query); ok {
			out = append(out, c)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// FuzzyContentProvider — Autocomplete provider that searches a corpus.
// ---------------------------------------------------------------------------

// ContentEntry describes one searchable document.
type ContentEntry struct {
	// Key is the label used for display.
	Key string
	// Body is the searchable text.
	Body string
	// Tag is opaque metadata surfaced in the Suggestion.
	Tag any
}

// FuzzyContentProvider implements AutocompleteProvider by searching bodies.
type FuzzyContentProvider struct {
	TriggerStr string
	Entries    []ContentEntry
	// MaxPreview caps the excerpt length; 0 = 48 characters.
	MaxPreview int64
}

// Trigger returns the configured prefix (default "#").
func (p *FuzzyContentProvider) Trigger() string {
	if p.TriggerStr == "" {
		return "#"
	}
	return p.TriggerStr
}

// Complete scans every entry for a normalised substring match.
func (p *FuzzyContentProvider) Complete(token string) []core.Suggestion {
	if token == "" {
		out := make([]core.Suggestion, 0, len(p.Entries))
		for _, e := range p.Entries {
			out = append(out, core.Suggestion{
				Label:       e.Key,
				InsertText:  e.Key,
				Description: previewText(e.Body, p.previewLen()),
				Tag:         e.Tag,
			})
		}
		return out
	}
	var out []core.Suggestion
	for _, e := range p.Entries {
		start, end, ok := fuzzy.Find(e.Body, token)
		if !ok {
			continue
		}
		excerpt := makeExcerpt(e.Body, start, end, p.previewLen())
		out = append(out, core.Suggestion{
			Label:       e.Key,
			InsertText:  e.Key,
			Description: excerpt,
			Tag:         e.Tag,
		})
	}
	return out
}

func (p *FuzzyContentProvider) previewLen() int64 {
	if p.MaxPreview > 0 {
		return p.MaxPreview
	}
	return 48
}

func previewText(s string, max int64) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\t", " ")
	if int64(len(s)) <= max {
		return s
	}
	return s[:max] + "…"
}

func makeExcerpt(body string, start, end, max int64) string {
	if end-start > max {
		end = start + max
	}
	b := int64(len(body))
	lo := start - max/2
	if lo < 0 {
		lo = 0
	}
	hi := end + max/2
	if hi > b {
		hi = b
	}
	out := body[lo:hi]
	out = strings.ReplaceAll(strings.ReplaceAll(out, "\n", " "), "\t", " ")
	if lo > 0 {
		out = "…" + out
	}
	if hi < b {
		out += "…"
	}
	return out
}
