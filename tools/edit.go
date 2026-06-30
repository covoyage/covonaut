package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

// EditOperations defines pluggable filesystem operations for the edit tool.
type EditOperations interface {
	ReadFile(path string) ([]byte, error)
	WriteFile(path string, content []byte) error
	Access(path string) error
}

// DefaultEditOperations uses the local filesystem.
type DefaultEditOperations struct{}

func (d DefaultEditOperations) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }
func (d DefaultEditOperations) WriteFile(path string, content []byte) error {
	return os.WriteFile(path, content, 0644)
}
func (d DefaultEditOperations) Access(path string) error {
	_, err := os.Stat(path)
	return err
}

// EditToolConfig configures the edit tool.
type EditToolConfig struct {
	Operations EditOperations
}

func (c *EditToolConfig) defaults() {
	if c.Operations == nil {
		c.Operations = DefaultEditOperations{}
	}
}

// Edit represents a single replacement operation.
type Edit struct {
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
}

// EditToolInput is the JSON arguments for the edit tool.
type EditToolInput struct {
	Path  string `json:"path"`
	Edits []Edit `json:"edits"`
}

// EditToolDetails carries diff metadata.
type EditToolDetails struct {
	Diff             string `json:"diff"`
	FirstChangedLine *int   `json:"first_changed_line,omitempty"`
	FuzzyMatch       bool   `json:"fuzzy_match,omitempty"`
}

// NewEditTool creates a file edit tool.
func NewEditTool(cwd string, cfg *EditToolConfig) *agentcore.Tool {
	if cfg == nil {
		cfg = &EditToolConfig{}
	}
	cfg.defaults()

	return &agentcore.Tool{
		Name: "edit",
		Description: "Edit a single file using exact text replacement. Every edits[].oldText must match a unique, non-overlapping region of the original file. " +
			"If two changes affect the same block or nearby lines, merge them into one edit instead of emitting overlapping edits. " +
			"Do not include large unchanged regions just to connect distant changes.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "Path to the file to edit (relative or absolute)"},
				"edits": map[string]any{
					"type":        "array",
					"description": "One or more targeted replacements. Each edit is matched against the original file, not incrementally.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"oldText": map[string]any{"type": "string", "description": "Exact text for one targeted replacement. It must be unique in the original file and must not overlap with any other edits[].oldText in the same call."},
							"newText": map[string]any{"type": "string", "description": "Replacement text for this targeted edit."},
						},
						"required": []any{"oldText", "newText"},
					},
				},
			},
			"required": []any{"path", "edits"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var input EditToolInput
			if err := json.Unmarshal(args, &input); err != nil {
				return resultErrf("invalid arguments: %w", err)
			}

			if len(input.Edits) == 0 {
				return resultErrf("edits must contain at least one replacement")
			}

			resolved := resolveReadPath(input.Path, cwd)
			if err := cfg.Operations.Access(resolved); err != nil {
				return resultErrf("file not found: %s", input.Path)
			}

			data, err := cfg.Operations.ReadFile(resolved)
			if err != nil {
				return resultErrf("failed to read file: %w", err)
			}

			// Strip BOM if present.
			bom, content := stripBOM(string(data))
			originalEnding := detectLineEnding(content)
			normalized := normalizeToLF(content)

			baseContent, newContent, usedFuzzy, err := applyEdits(normalized, input.Edits, input.Path)
			if err != nil {
				return nil, err
			}

			finalContent := bom + restoreLineEndings(newContent, originalEnding)
			if err := cfg.Operations.WriteFile(resolved, []byte(finalContent)); err != nil {
				return resultErrf("failed to write file: %w", err)
			}

			diff, firstLine := generateDiff(baseContent, newContent)
			return result(
				fmt.Sprintf("Successfully replaced %d block(s) in %s.", len(input.Edits), input.Path),
				EditToolDetails{Diff: diff, FirstChangedLine: firstLine, FuzzyMatch: usedFuzzy},
			)
		},
	}
}

// --- edit helpers ---

func stripBOM(s string) (string, string) {
	if strings.HasPrefix(s, "\uFEFF") {
		return "\uFEFF", s[3:] // UTF-8 BOM is 3 bytes
	}
	return "", s
}

func detectLineEnding(s string) string {
	crlfIdx := strings.Index(s, "\r\n")
	lfIdx := strings.Index(s, "\n")
	if lfIdx == -1 {
		return "\n"
	}
	if crlfIdx == -1 {
		return "\n"
	}
	if crlfIdx < lfIdx {
		return "\r\n"
	}
	return "\n"
}

func normalizeToLF(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

func restoreLineEndings(s, ending string) string {
	if ending == "\r\n" {
		return strings.ReplaceAll(s, "\n", "\r\n")
	}
	return s
}

func applyEdits(content string, edits []Edit, path string) (string, string, bool, error) {
	normalizedEdits := make([]Edit, len(edits))
	for i, e := range edits {
		normalizedEdits[i] = Edit{
			OldText: normalizeToLF(e.OldText),
			NewText: normalizeToLF(e.NewText),
		}
	}

	for i, e := range normalizedEdits {
		if e.OldText == "" {
			return "", "", false, fmt.Errorf("oldText must not be empty (edits[%d] in %s)", i, path)
		}
	}

	type match struct {
		index   int
		length  int
		newText string
		fuzzy   bool
	}
	matches := make([]match, len(normalizedEdits))
	anyFuzzy := false
	for i, e := range normalizedEdits {
		idx := strings.Index(content, e.OldText)
		if idx >= 0 && strings.Count(content, e.OldText) == 1 {
			matches[i] = match{index: idx, length: len(e.OldText), newText: e.NewText}
			continue
		}

		// 9-strategy progressive fuzzy matching (from MiMo-Code)
		m := editMatcher{content: content, old: e.OldText, new: e.NewText}
		if !m.find() {
			return "", "", false, fmt.Errorf("could not find edits[%d] in %s", i, path)
		}
		matchedStr := m.matchedStr
		idx = strings.Index(content, matchedStr)
		if idx < 0 {
			return "", "", false, fmt.Errorf("could not find matched edit[%d] in %s", i, path)
		}
		matches[i] = match{index: idx, length: len(matchedStr), newText: e.NewText, fuzzy: true}
		anyFuzzy = true
	}

	for i := 0; i < len(matches); i++ {
		for j := i + 1; j < len(matches); j++ {
			if matches[i].index > matches[j].index {
				matches[i], matches[j] = matches[j], matches[i]
			}
		}
	}
	for i := 1; i < len(matches); i++ {
		if matches[i-1].index+matches[i-1].length > matches[i].index {
			return "", "", false, fmt.Errorf("edits overlap in %s", path)
		}
	}

	newContent := content
	for i := len(matches) - 1; i >= 0; i-- {
		m := matches[i]
		newContent = newContent[:m.index] + m.newText + newContent[m.index+m.length:]
	}

	if content == newContent {
		return "", "", false, fmt.Errorf("no changes made to %s", path)
	}

	return content, newContent, anyFuzzy, nil
}

func generateDiff(oldContent, newContent string) (string, *int) {
	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")
	maxLine := len(oldLines)
	if len(newLines) > maxLine {
		maxLine = len(newLines)
	}
	width := len(fmt.Sprintf("%d", maxLine))

	// Simple line-by-line diff.
	var output []string
	oldIdx, newIdx := 0, 0
	var firstChanged *int

	for oldIdx < len(oldLines) || newIdx < len(newLines) {
		if oldIdx < len(oldLines) && newIdx < len(newLines) && oldLines[oldIdx] == newLines[newIdx] {
			lineNum := fmt.Sprintf("%*d", width, newIdx+1)
			output = append(output, fmt.Sprintf(" %s %s", lineNum, oldLines[oldIdx]))
			oldIdx++
			newIdx++
		} else if newIdx < len(newLines) && (oldIdx >= len(oldLines) || !lineInOld(oldLines[oldIdx:], newLines[newIdx])) {
			lineNum := fmt.Sprintf("%*d", width, newIdx+1)
			output = append(output, fmt.Sprintf("+%s %s", lineNum, newLines[newIdx]))
			if firstChanged == nil {
				v := newIdx + 1
				firstChanged = &v
			}
			newIdx++
		} else if oldIdx < len(oldLines) {
			lineNum := fmt.Sprintf("%*d", width, oldIdx+1)
			output = append(output, fmt.Sprintf("-%s %s", lineNum, oldLines[oldIdx]))
			if firstChanged == nil {
				v := newIdx + 1
				firstChanged = &v
			}
			oldIdx++
		}
	}

	return strings.Join(output, "\n"), firstChanged
}

func lineInOld(oldLines []string, line string) bool {
	for _, l := range oldLines {
		if l == line {
			return true
		}
	}
	return false
}

// --- 9-strategy fuzzy edit matching (from MiMo-Code) ---

type editMatcher struct {
	content, old, new string
	replaceAll        bool
	isMatch           bool
	matchedStr        string
}

func (m *editMatcher) find() bool {
	strategies := []func() bool{
		m.simple,
		m.unicodeNormalized,
		m.lineTrimmed,
		m.blockAnchor,
		m.whitespaceNormalized,
		m.indentationFlexible,
		m.escapeNormalized,
		m.trimmedBoundary,
		m.contextAware,
		m.multiOccurrence,
	}
	for _, s := range strategies {
		if s() {
			// Verify matched string is unique in content
			if !m.replaceAll && strings.Count(m.content, m.matchedStr) > 1 {
				continue
			}
			return true
		}
	}
	return false
}

func (m *editMatcher) isUnique() bool {
	return strings.Count(m.content, m.old) <= 1
}

func (m *editMatcher) simple() bool {
	if idx := strings.Index(m.content, m.old); idx >= 0 {
		m.matchedStr = m.old
		return true
	}
	return false
}

func (m *editMatcher) unicodeNormalized() bool {
	return matchByLines(m.content, m.old, normalizeUnicode, &m.matchedStr)
}

func (m *editMatcher) lineTrimmed() bool {
	return matchByLines(m.content, m.old, strings.TrimSpace, &m.matchedStr)
}

func (m *editMatcher) blockAnchor() bool {
	cl := strings.Split(m.content, "\n")
	fl := strings.Split(m.old, "\n")
	if len(fl) < 3 {
		return false
	}
	if len(fl) > 0 && fl[len(fl)-1] == "" {
		fl = fl[:len(fl)-1]
	}
	first, last := strings.TrimSpace(fl[0]), strings.TrimSpace(fl[len(fl)-1])
	for i := 0; i <= len(cl)-len(fl); i++ {
		if strings.TrimSpace(cl[i]) != first {
			continue
		}
		if i+len(fl)-1 >= len(cl) {
			continue
		}
		if strings.TrimSpace(cl[i+len(fl)-1]) != last {
			continue
		}
		m.matchedStr = strings.Join(cl[i:i+len(fl)], "\n")
		return true
	}
	return false
}

func (m *editMatcher) whitespaceNormalized() bool {
	return matchByLines(m.content, m.old, collapseWS, &m.matchedStr)
}

func (m *editMatcher) indentationFlexible() bool {
	return matchByLines(m.content, m.old, stripCommonInd, &m.matchedStr)
}

func (m *editMatcher) escapeNormalized() bool {
	unesc := unescapeStr(m.old)
	if strings.Contains(m.content, unesc) {
		m.matchedStr = unesc
		return true
	}
	cl := strings.Split(m.content, "\n")
	fl := strings.Split(unesc, "\n")
	for i := 0; i <= len(cl)-len(fl); i++ {
		if unescapeStr(strings.Join(cl[i:i+len(fl)], "\n")) == unesc {
			m.matchedStr = strings.Join(cl[i:i+len(fl)], "\n")
			return true
		}
	}
	return false
}

func (m *editMatcher) trimmedBoundary() bool {
	trimmed := strings.TrimSpace(m.old)
	if trimmed == m.old {
		return false
	}
	m.matchedStr = trimmed
	return strings.Contains(m.content, trimmed)
}

func (m *editMatcher) contextAware() bool {
	cl := strings.Split(m.content, "\n")
	fl := strings.Split(m.old, "\n")
	if len(fl) > 0 && fl[len(fl)-1] == "" {
		fl = fl[:len(fl)-1]
	}
	if len(fl) < 2 {
		return false
	}
	for i := 0; i <= len(cl)-len(fl); i++ {
		match := 0
		for j := 0; j < len(fl); j++ {
			if strings.TrimSpace(cl[i+j]) == strings.TrimSpace(fl[j]) {
				match++
			}
		}
		if float64(match) >= float64(len(fl))*0.5 {
			m.matchedStr = strings.Join(cl[i:i+len(fl)], "\n")
			return true
		}
	}
	return false
}

func (m *editMatcher) multiOccurrence() bool {
	if strings.Count(m.content, m.old) >= 1 {
		m.matchedStr = m.old
		return true
	}
	return false
}

func findChangeIndex(orig, changed string) int {
	if len(orig) == 0 || len(changed) == 0 {
		return -1
	}
	minLen := len(orig)
	if len(changed) < minLen {
		minLen = len(changed)
	}
	for i := 0; i < minLen; i++ {
		if orig[i] != changed[i] {
			return i
		}
	}
	return -1
}

func matchByLines(content, find string, transform func(string) string, out *string) bool {
	cl := strings.Split(content, "\n")
	fl := strings.Split(find, "\n")
	if len(fl) > 0 && fl[len(fl)-1] == "" {
		fl = fl[:len(fl)-1]
	}
	for i := 0; i <= len(cl)-len(fl); i++ {
		ok := true
		for j := 0; j < len(fl); j++ {
			if transform(cl[i+j]) != transform(fl[j]) {
				ok = false
				break
			}
		}
		if ok {
			*out = strings.Join(cl[i:i+len(fl)], "\n")
			return true
		}
	}
	return false
}

func collapseWS(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func stripCommonInd(s string) string {
	lines := strings.Split(s, "\n")
	var nonEmpty []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			nonEmpty = append(nonEmpty, l)
		}
	}
	if len(nonEmpty) == 0 {
		return s
	}
	minIndent := 1 << 30
	for _, l := range nonEmpty {
		sp := 0
		for _, ch := range l {
			if ch == ' ' || ch == '\t' {
				sp++
			} else {
				break
			}
		}
		if sp < minIndent {
			minIndent = sp
		}
	}
	var result []string
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			result = append(result, l)
		} else if len(l) >= minIndent {
			result = append(result, l[minIndent:])
		} else {
			result = append(result, l)
		}
	}
	return strings.Join(result, "\n")
}

func unescapeStr(s string) string {
	r := strings.NewReplacer(
		`\n`, "\n", `\t`, "\t", `\r`, "\r",
		`\"`, `"`, `\'`, "'", `\\`, `\`,
	)
	return r.Replace(s)
}

func normalizeUnicode(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\u2018', '\u2019', '\u201A', '\u201B': // curly single quotes
			b.WriteByte('\'')
		case '\u201C', '\u201D', '\u201E', '\u201F': // curly double quotes
			b.WriteByte('"')
		case '\u2013', '\u2014', '\u2015': // en/em/horizontal dash
			b.WriteByte('-')
		case '\u00A0', '\u2000', '\u2001', '\u2002', '\u2003',
			'\u2004', '\u2005', '\u2006', '\u2007', '\u2008',
			'\u2009', '\u200A', '\u202F', '\u205F', '\u3000': // various spaces
			b.WriteByte(' ')
		case '\r':
			// skip
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
