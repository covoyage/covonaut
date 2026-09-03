package component

import (
	"strings"

	"github.com/covoyage/covonaut/tui/core"
)

// Private-use marks stand in for paste chips so the buffer holds one rune
// while render/GetValue expand the label. Cursor and delete then treat the
// chip as a single character.
const (
	chipMarkBase          rune = 0xE000
	chipMarkLast          rune = 0xE0FF
	editorPasteChipPrefix      = "[Pasted ~"
)

func isChipMark(r rune) bool {
	return r >= chipMarkBase && r <= chipMarkLast
}

func (e *Editor) allocChipLocked(label string) rune {
	if e.chips == nil {
		e.chips = make(map[rune]string)
		e.nextChip = chipMarkBase
	}
	for i := 0; i < int(chipMarkLast-chipMarkBase)+1; i++ {
		mark := e.nextChip
		e.nextChip++
		if e.nextChip > chipMarkLast {
			e.nextChip = chipMarkBase
		}
		if _, used := e.chips[mark]; !used {
			e.chips[mark] = label
			return mark
		}
	}
	mark := e.nextChip
	e.chips[mark] = label
	return mark
}

func (e *Editor) encodeChipsLocked(s string) string {
	if !strings.Contains(s, editorPasteChipPrefix) {
		return s
	}
	var b strings.Builder
	from := 0
	for {
		i := strings.Index(s[from:], editorPasteChipPrefix)
		if i < 0 {
			b.WriteString(s[from:])
			return b.String()
		}
		i += from
		j := strings.Index(s[i:], "]")
		if j < 0 {
			b.WriteString(s[from:])
			return b.String()
		}
		j += i + 1
		b.WriteString(s[from:i])
		b.WriteRune(e.allocChipLocked(s[i:j]))
		from = j
	}
}

func (e *Editor) decodeChipsLocked(s string) string {
	if e.chips == nil || !strings.ContainsFunc(s, isChipMark) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if label, ok := e.chips[r]; ok {
			b.WriteString(label)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func (e *Editor) expandLineLocked(line []rune) string {
	if len(line) == 0 {
		return ""
	}
	return e.decodeChipsLocked(string(line))
}

func (e *Editor) runeCellWidthLocked(r rune) int64 {
	if label, ok := e.chips[r]; ok {
		w := core.VisibleWidth(label)
		if w < 1 {
			return 1
		}
		return w
	}
	return core.RuneWidth(r)
}

func (e *Editor) cellWidthLocked(runes []rune, start, end int64) int64 {
	if start < 0 {
		start = 0
	}
	if end > int64(len(runes)) {
		end = int64(len(runes))
	}
	var w int64
	for i := start; i < end; i++ {
		w += e.runeCellWidthLocked(runes[i])
	}
	return w
}

func (e *Editor) sliceByCellsLocked(runes []rune, startCell, endCell int64) core.RuneSlice {
	var col int64
	var startR, endR int64 = -1, -1
	var b []rune
	for idx, r := range runes {
		w := e.runeCellWidthLocked(r)
		if col >= startCell && col+w <= endCell {
			if startR < 0 {
				startR = int64(idx)
			}
			b = append(b, r)
			endR = int64(idx) + 1
		}
		col += w
		if col > endCell {
			break
		}
	}
	if startR < 0 {
		startR = 0
		endR = 0
	}
	return core.RuneSlice{Text: string(b), StartR: startR, EndR: endR}
}

func (e *Editor) runeIndexAtCellLocked(runes []rune, targetCell int64) int64 {
	if targetCell <= 0 {
		return 0
	}
	var col int64
	for i, r := range runes {
		w := e.runeCellWidthLocked(r)
		if col+w > targetCell {
			return int64(i)
		}
		col += w
	}
	return int64(len(runes))
}

func cloneChipMap(m map[rune]string) map[rune]string {
	if m == nil {
		return nil
	}
	out := make(map[rune]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
