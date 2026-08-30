package component

import (
	"fmt"
	"strings"

	"github.com/covoyage/covonaut/tui/core"
	apitheme "github.com/covoyage/covonaut/tui/theme"
)

func renderMarkdown(src string, width int64, theme MarkdownTheme) []string {
	if theme.SkipIncomplete {
		src = clipIncompleteMarkdown(src)
	}
	blocks := parseMarkdown(src)
	return renderBlocks(blocks, width, theme, 0)
}

func renderBlocks(blocks []mdBlock, width int64, theme MarkdownTheme, indent int) []string {
	var out []string
	for i, b := range blocks {
		if b.Kind == mdBlank {
			if len(out) == 0 || isBlankRendered(out[len(out)-1]) {
				continue
			}
			out = append(out, renderBlock(b, width, theme, indent)...)
			continue
		}
		out = append(out, renderBlock(b, width, theme, indent)...)
		if i+1 < len(blocks) && blocks[i+1].Kind != mdBlank && blockLeavesGap(b) {
			out = append(out, core.PadToWidth("", width))
		}
	}
	return out
}

func blockLeavesGap(b mdBlock) bool {
	switch b.Kind {
	case mdHeading, mdFence, mdTable, mdQuote, mdHR, mdMath, mdImage, mdList, mdDefList, mdFootnote:
		return true
	default:
		return false
	}
}

func isBlankRendered(s string) bool {
	return strings.TrimSpace(core.StripAnsi(s)) == ""
}

func renderBlock(b mdBlock, width int64, theme MarkdownTheme, indent int) []string {
	switch b.Kind {
	case mdBlank:
		return []string{core.PadToWidth("", width)}
	case mdHR:
		rule := strings.Repeat("─", maxInt(int(width)-indent, 1))
		line := strings.Repeat(" ", indent) + theme.HRFn(rule)
		return []string{core.PadToWidth(line, width)}
	case mdHeading:
		level := b.Level - 1
		if level < 0 {
			level = 0
		}
		if level > 5 {
			level = 5
		}
		text := renderInlineNodes(b.Inlines, theme)
		fn := theme.HeadingFn[level]
		innerW := maxWidth(width, indent)
		wrapped := core.WrapAnsi(fn(text), innerW)
		out := padLines(wrapped, width, indent)
		if b.Level == 1 {
			ruleW := int(core.VisibleWidth(text))
			if len(wrapped) > 0 {
				if w := int(core.VisibleWidth(wrapped[0])); w > ruleW {
					ruleW = w
				}
			}
			if innerW > 0 && ruleW > int(innerW) {
				ruleW = int(innerW)
			}
			if ruleW < 3 {
				ruleW = 3
			}
			rule := strings.Repeat("─", ruleW)
			out = append(out, core.PadToWidth(strings.Repeat(" ", indent)+theme.HRFn(rule), width))
		}
		return out
	case mdPara:
		text := renderInlineNodes(b.Inlines, theme)
		wrapped := core.WrapAnsi(text, maxWidth(width, indent))
		return padLines(wrapped, width, indent)
	case mdMath:
		body := renderMathText(b.Text)
		if theme.MathFn != nil {
			body = theme.MathFn(body)
		} else {
			body = theme.QuoteFn("⟨ ") + body + theme.QuoteFn(" ⟩")
		}
		wrapped := core.WrapAnsi(body, maxWidth(width, indent))
		return padLines(wrapped, width, indent)
	case mdFence:
		return renderFence(b, width, theme, indent)
	case mdQuote:
		return renderQuote(b, width, theme, indent)
	case mdList:
		return renderList(b, width, theme, indent)
	case mdTable:
		return renderTableBlock(b, width, theme, indent)
	case mdFootnote:
		return renderFootnote(b, width, theme, indent)
	case mdDefList:
		return renderDefList(b, width, theme, indent)
	case mdImage:
		return renderImageBlock(b, width, theme, indent)
	default:
		return nil
	}
}

func renderImageBlock(b mdBlock, width int64, theme MarkdownTheme, indent int) []string {
	alt, url := b.Text, b.Info
	innerW := maxWidth(width, indent)
	if theme.ImageRenderer != nil {
		if lines := theme.ImageRenderer(alt, url, innerW); len(lines) > 0 {
			return padLines(lines, width, indent)
		}
	}
	label := "🖼 " + alt
	if alt == "" {
		label = "🖼 " + url
	}
	var text string
	if theme.LinkRendererFn != nil && url != "" {
		text = theme.LinkRendererFn(label, url)
	} else {
		text = theme.LinkLabelFn(label)
		if url != "" {
			text += " " + theme.LinkURLFn("("+url+")")
		}
	}
	return padLines(core.WrapAnsi(text, innerW), width, indent)
}

func renderQuote(b mdBlock, width int64, theme MarkdownTheme, indent int) []string {
	innerW := nestedWidth(width, indent+2)
	inner := renderBlocks(b.Children, innerW, theme, 0)
	out := make([]string, 0, len(inner)+1)
	bar := theme.QuoteFn("│ ")
	prefix := strings.Repeat(" ", indent)
	if b.Alert != "" {
		label := alertLabel(b.Alert)
		out = append(out, core.PadToWidth(prefix+theme.QuoteFn("│ ")+theme.StrongFn(label), width))
	}
	for _, ln := range inner {
		out = append(out, core.PadToWidth(prefix+bar+ln, width))
	}
	if len(out) == 0 {
		out = append(out, core.PadToWidth(prefix+bar, width))
	}
	return out
}

func alertLabel(kind string) string {
	switch kind {
	case "NOTE":
		return "ⓘ NOTE"
	case "TIP":
		return "✦ TIP"
	case "IMPORTANT":
		return "★ IMPORTANT"
	case "WARNING":
		return "⚠ WARNING"
	case "CAUTION":
		return "‼ CAUTION"
	default:
		return kind
	}
}

func renderFootnote(b mdBlock, width int64, theme MarkdownTheme, indent int) []string {
	label := b.Text
	if label == "" {
		label = "*"
	}
	marker := theme.ListBulletFn("[" + label + "] ")
	markerW := core.VisibleWidth("[" + label + "] ")
	innerW := nestedWidth(width, indent+int(markerW))
	prefix := strings.Repeat(" ", indent)
	cont := prefix + strings.Repeat(" ", int(markerW))
	children := b.Children
	if len(children) == 0 {
		return []string{core.PadToWidth(prefix+marker, width)}
	}
	var out []string
	first := true
	for _, ch := range children {
		if ch.Kind == mdBlank {
			if !first {
				out = append(out, core.PadToWidth("", width))
			}
			continue
		}
		lines := renderBlock(ch, innerW, theme, 0)
		for i, ln := range lines {
			head := cont
			if first && i == 0 {
				head = prefix + marker
				first = false
			}
			out = append(out, core.PadToWidth(head+ln, width))
		}
		first = false
	}
	return out
}

func renderDefList(b mdBlock, width int64, theme MarkdownTheme, indent int) []string {
	var out []string
	for _, item := range b.Children {
		if item.Kind != mdDefItem {
			out = append(out, renderBlock(item, width, theme, indent)...)
			continue
		}
		term := renderInlineNodes(item.Inlines, theme)
		if term != "" {
			wrapped := core.WrapAnsi(theme.StrongFn(term), maxWidth(width, indent))
			out = append(out, padLines(wrapped, width, indent)...)
		}
		descIndent := indent + 2
		for _, ch := range item.Children {
			if ch.Kind == mdBlank {
				out = append(out, core.PadToWidth("", width))
				continue
			}
			out = append(out, renderBlock(ch, width, theme, descIndent)...)
		}
	}
	return out
}

func renderList(b mdBlock, width int64, theme MarkdownTheme, indent int) []string {
	var out []string
	num := b.Start
	if num <= 0 {
		num = 1
	}
	for i, item := range b.Children {
		if i > 0 && !b.Tight {
			out = append(out, core.PadToWidth("", width))
		}
		out = append(out, renderListItem(item, b.Ordered, num, width, theme, indent)...)
		num++
	}
	return out
}

func renderListItem(item mdBlock, ordered bool, num int, width int64, theme MarkdownTheme, indent int) []string {
	bullet := "• "
	if item.Checked != nil {
		if *item.Checked {
			bullet = "☑ "
		} else {
			bullet = "☐ "
		}
	} else if ordered {
		bullet = fmt.Sprintf("%d. ", num)
	}
	styled := theme.ListBulletFn(bullet)
	markerW := core.VisibleWidth(bullet)
	prefix0 := strings.Repeat(" ", indent) + styled
	cont := strings.Repeat(" ", indent+int(markerW))
	innerW := nestedWidth(width, indent+int(markerW))

	children := item.Children
	if len(children) == 0 {
		return []string{core.PadToWidth(prefix0, width)}
	}

	var out []string
	first := true
	for _, ch := range children {
		if ch.Kind == mdBlank {
			if !first {
				out = append(out, core.PadToWidth("", width))
			}
			continue
		}
		lines := renderBlock(ch, innerW, theme, 0)
		for i, ln := range lines {
			head := cont
			if first {
				head = prefix0
				first = false
			}
			if i > 0 && strings.HasPrefix(head, prefix0) {
				head = cont
			}
			out = append(out, core.PadToWidth(head+ln, width))
		}
		first = false
	}
	return out
}

func renderFence(b mdBlock, width int64, theme MarkdownTheme, indent int) []string {
	lang := strings.ToLower(strings.TrimSpace(b.Lang))
	source := strings.Join(b.Lines, "\n")

	innerW := nestedWidth(width, indent+2)
	if innerW > 0 && innerW < 8 {
		innerW = 8
	}
	if theme.FenceRenderer != nil {
		if custom := theme.FenceRenderer(lang, source, innerW); len(custom) > 0 {
			// Mermaid already draws its own boxes; extra chrome also truncates wide diagrams.
			if lang == "mermaid" {
				return padLines(custom, width, indent)
			}
			return wrapFence(custom, lang, width, theme, indent)
		}
	}

	var rendered []string
	switch {
	case lang == "diff":
		rendered = renderDiffFence(b.Lines, theme)
	case !theme.DisableSyntax && theme.HighlightFence != nil && lang != "":
		if hl := theme.HighlightFence(source, lang); hl != "" && hl != source {
			rendered = strings.Split(hl, "\n")
		}
	}
	if rendered == nil {
		if spec := LookupLanguage(lang); spec != nil && !theme.DisableSyntax {
			rendered = Highlight(source, lang, syntaxThemeFromMarkdown(theme))
		} else {
			rendered = make([]string, len(b.Lines))
			for i, cl := range b.Lines {
				rendered[i] = theme.CodeBlockFn(cl)
			}
		}
	}

	return wrapFence(rendered, lang, width, theme, indent)
}

func wrapFence(body []string, lang string, width int64, theme MarkdownTheme, indent int) []string {
	prefix := strings.Repeat(" ", indent)
	label := strings.TrimSpace(lang)
	unlimited := width <= 0
	innerW := width - int64(indent) - 2
	if unlimited {
		innerW = 4
		for _, cl := range body {
			if w := core.VisibleWidth(cl) + 1; w > innerW {
				innerW = w
			}
		}
		if label != "" {
			if w := core.VisibleWidth(label) + 1; w > innerW {
				innerW = w
			}
		}
	}
	if innerW < 4 {
		innerW = 4
	}
	contentW := innerW - 1
	if contentW < 1 {
		contentW = 1
	}
	top := fenceTopRule(label, innerW)
	bot := "└" + strings.Repeat("─", int(innerW)) + "┘"
	out := make([]string, 0, len(body)+2)
	out = append(out, core.PadToWidth(prefix+theme.CodeFenceFn(top), width))
	for _, cl := range body {
		chunks := []string{cl}
		if !unlimited {
			chunks = core.WrapAnsi(cl, contentW)
			if len(chunks) == 0 {
				chunks = []string{""}
			}
		}
		for _, chunk := range chunks {
			padded := " " + padVisible(chunk, contentW)
			out = append(out, core.PadToWidth(prefix+theme.CodeFenceFn("│")+padded+theme.CodeFenceFn("│"), width))
		}
	}
	out = append(out, core.PadToWidth(prefix+theme.CodeFenceFn(bot), width))
	return out
}

func fenceTopRule(label string, innerW int64) string {
	if label == "" {
		return "┌" + strings.Repeat("─", int(innerW)) + "┐"
	}
	runes := []rune(label)
	maxLab := int(innerW) - 1
	if maxLab < 1 {
		maxLab = 1
	}
	if len(runes) > maxLab {
		label = string(runes[:maxLab])
		runes = []rune(label)
	}
	rest := int(innerW) - 1 - len(runes)
	if rest < 0 {
		rest = 0
	}
	return "┌─" + label + strings.Repeat("─", rest) + "┐"
}

func padVisible(s string, w int64) string {
	if w <= 0 {
		return s
	}
	vw := core.VisibleWidth(s)
	if vw >= w {
		return s
	}
	return s + strings.Repeat(" ", int(w-vw))
}

func renderDiffFence(codeLines []string, theme MarkdownTheme) []string {
	rendered := make([]string, len(codeLines))
	hlContent := func(line string) string { return theme.CodeBlockFn(line) }
	if !theme.DisableSyntax {
		if fileLang := diffFileLanguage(codeLines); fileLang != "" {
			if theme.HighlightFence != nil {
				hlContent = func(line string) string {
					out := theme.HighlightFence(line, fileLang)
					if out == "" {
						return theme.CodeBlockFn(line)
					}
					if i := strings.IndexByte(out, '\n'); i >= 0 {
						out = out[:i]
					}
					return out
				}
			} else if LookupLanguage(fileLang) != nil {
				st := syntaxThemeFromMarkdown(theme)
				hlContent = func(line string) string {
					out := Highlight(line, fileLang, st)
					if len(out) == 0 {
						return theme.CodeBlockFn(line)
					}
					return out[0]
				}
			}
		}
	}
	var oldLine, newLine int
	for k, cl := range codeLines {
		switch {
		case strings.HasPrefix(cl, "@@ "):
			rendered[k] = apitheme.CurrentPalette().Accent.Render(theme.CodeBlockFn(cl))
			if _, err := fmt.Sscanf(cl, "@@ -%d", &oldLine); err == nil {
				newLine = oldLine
				if idx := strings.Index(cl, "+"); idx > 0 {
					if _, err2 := fmt.Sscanf(cl[idx:], "+%d", &newLine); err2 != nil {
						newLine = oldLine
					}
				}
			}
		case strings.HasPrefix(cl, "+++ ") || strings.HasPrefix(cl, "--- "):
			rendered[k] = theme.CodeBlockFn(cl)
		case strings.HasPrefix(cl, "+") && !strings.HasPrefix(cl, "++"):
			rendered[k] = apitheme.CurrentPalette().Success.Render(fmt.Sprintf("%4d +", newLine)) + hlContent(strings.TrimPrefix(cl, "+"))
			newLine++
		case strings.HasPrefix(cl, "-") && !strings.HasPrefix(cl, "--"):
			rendered[k] = apitheme.CurrentPalette().Error.Render(fmt.Sprintf("%4d -", oldLine)) + hlContent(strings.TrimPrefix(cl, "-"))
			oldLine++
		default:
			rendered[k] = fmt.Sprintf("      %s", hlContent(strings.TrimPrefix(cl, " ")))
			oldLine++
			newLine++
		}
	}
	return rendered
}

const maxTableBodyRows = 200

func renderTableBlock(b mdBlock, width int64, theme MarkdownTheme, indent int) []string {
	cols := len(b.HeaderCells)
	if cols == 0 {
		return nil
	}
	header := make([]string, cols)
	for i, h := range b.HeaderCells {
		header[i] = renderInlineNodes(h, theme)
	}
	body := make([][]string, len(b.RowCells))
	for r, row := range b.RowCells {
		body[r] = make([]string, cols)
		for i := 0; i < cols && i < len(row); i++ {
			body[r][i] = renderInlineNodes(row[i], theme)
		}
	}
	omitted := 0
	if len(body) > maxTableBodyRows {
		omitted = len(body) - maxTableBodyRows
		body = body[:maxTableBodyRows]
	}
	avail := nestedWidth(width, indent)
	colW := make([]int64, cols)
	for i, h := range header {
		colW[i] = core.VisibleWidth(h)
	}
	for _, row := range body {
		for i := 0; i < cols && i < len(row); i++ {
			if w := core.VisibleWidth(row[i]); w > colW[i] {
				colW[i] = w
			}
		}
	}
	const minCol int64 = 2
	for i := range colW {
		if colW[i] < minCol {
			colW[i] = minCol
		}
	}
	chrome := int64(cols)*3 + 1
	stacked := avail > 0 && chrome+minCol*int64(cols) > avail
	if !stacked && avail > 0 {
		total := chrome
		for _, w := range colW {
			total += w
		}
		if total > avail {
			excess := total - avail
			for excess > 0 {
				idx := 0
				for i := range colW {
					if colW[i] > colW[idx] {
						idx = i
					}
				}
				if colW[idx] <= minCol {
					break
				}
				colW[idx]--
				excess--
			}
		}
		total = chrome
		for _, w := range colW {
			total += w
		}
		if total > avail {
			stacked = true
		}
	}
	var out []string
	if stacked {
		out = renderTableStacked(header, body, width, theme, indent)
	} else {
		out = renderTableGrid(header, body, b.Align, colW, avail, width, theme, indent)
	}
	if omitted > 0 {
		prefix := strings.Repeat(" ", indent)
		note := theme.TableBorderFn(fmt.Sprintf("… %d more rows", omitted))
		out = append(out, core.PadToWidth(prefix+note, width))
	}
	return out
}

func renderTableGrid(header []string, body [][]string, align []mdAlign, colW []int64, avail, width int64, theme MarkdownTheme, indent int) []string {
	prefix := strings.Repeat(" ", indent)
	top := theme.TableBorderFn(tableRule(colW, "┌", "┬", "┐", "─"))
	sep := theme.TableBorderFn(tableRule(colW, "├", "┼", "┤", "─"))
	bot := theme.TableBorderFn(tableRule(colW, "└", "┴", "┘", "─"))
	out := []string{
		core.PadToWidth(prefix+top, width),
		core.PadToWidth(prefix+renderTableRow(header, colW, align, avail, true, theme), width),
		core.PadToWidth(prefix+sep, width),
	}
	for _, row := range body {
		for _, visual := range wrapTableRow(row, colW, align, avail, theme) {
			out = append(out, core.PadToWidth(prefix+visual, width))
		}
	}
	out = append(out, core.PadToWidth(prefix+bot, width))
	return out
}

func tableRule(colW []int64, left, mid, right, fill string) string {
	var b strings.Builder
	b.WriteString(left)
	for i, w := range colW {
		if i > 0 {
			b.WriteString(mid)
		}
		b.WriteString(strings.Repeat(fill, int(w)+2))
	}
	b.WriteString(right)
	return b.String()
}

func wrapTableRow(cells []string, colW []int64, align []mdAlign, avail int64, theme MarkdownTheme) []string {
	cols := len(colW)
	wrapped := make([][]string, cols)
	height := 1
	for i := 0; i < cols; i++ {
		cell := ""
		if i < len(cells) {
			cell = cells[i]
		}
		lines := wrapTableCell(cell, colW[i], avail)
		wrapped[i] = lines
		if len(lines) > height {
			height = len(lines)
		}
	}
	out := make([]string, 0, height)
	for y := 0; y < height; y++ {
		parts := make([]string, cols)
		for i := 0; i < cols; i++ {
			text := ""
			if y < len(wrapped[i]) {
				text = wrapped[i][y]
			}
			parts[i] = text
		}
		out = append(out, renderTableRow(parts, colW, align, avail, false, theme))
	}
	return out
}

func wrapTableCell(cell string, w, avail int64) []string {
	if avail <= 0 || w <= 0 {
		if cell == "" {
			return []string{""}
		}
		return []string{cell}
	}
	lines := core.WrapAnsi(cell, w)
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func renderTableRow(cells []string, colW []int64, align []mdAlign, avail int64, header bool, theme MarkdownTheme) string {
	var b strings.Builder
	b.WriteString(theme.TableBorderFn("│"))
	for i, w := range colW {
		cell := ""
		if i < len(cells) {
			cell = cells[i]
		}
		if avail > 0 && core.VisibleWidth(cell) > w {
			cell = core.TruncateToWidth(cell, w, "…")
		}
		a := mdAlignNone
		if i < len(align) {
			a = align[i]
		}
		padded := alignPad(cell, w, a)
		if header && theme.TableHeaderFn != nil {
			padded = theme.TableHeaderFn(padded)
		}
		b.WriteString(" ")
		b.WriteString(padded)
		b.WriteString(" ")
		b.WriteString(theme.TableBorderFn("│"))
	}
	return b.String()
}

func alignPad(s string, w int64, align mdAlign) string {
	vw := core.VisibleWidth(s)
	pad := w - vw
	if pad < 0 {
		pad = 0
	}
	switch align {
	case mdAlignRight:
		return strings.Repeat(" ", int(pad)) + s
	case mdAlignCenter:
		left := pad / 2
		return strings.Repeat(" ", int(left)) + s + strings.Repeat(" ", int(pad-left))
	default:
		return s + strings.Repeat(" ", int(pad))
	}
}

func renderTableStacked(header []string, body [][]string, width int64, theme MarkdownTheme, indent int) []string {
	if len(body) == 0 {
		body = [][]string{make([]string, len(header))}
	}
	prefix := strings.Repeat(" ", indent)
	innerW := maxWidth(width, indent)
	var out []string
	for r, row := range body {
		if r > 0 {
			out = append(out, core.PadToWidth("", width))
		}
		for i, key := range header {
			val := ""
			if i < len(row) {
				val = row[i]
			}
			if strings.TrimSpace(core.StripAnsi(key)) == "" && strings.TrimSpace(core.StripAnsi(val)) == "" {
				continue
			}
			keyLine := key
			if theme.TableHeaderFn != nil {
				keyLine = theme.TableHeaderFn(key)
			}
			out = append(out, padLines(core.WrapAnsi(keyLine, innerW), width, indent)...)
			if strings.TrimSpace(core.StripAnsi(val)) == "" {
				continue
			}
			for _, ln := range core.WrapAnsi(val, maxWidth(width, indent+2)) {
				out = append(out, core.PadToWidth(prefix+"  "+ln, width))
			}
		}
	}
	return out
}

func renderInlineNodes(nodes []inlineNode, t MarkdownTheme) string {
	var b strings.Builder
	for _, n := range nodes {
		switch n.Kind {
		case inText:
			b.WriteString(n.Text)
		case inBreak:
			b.WriteByte('\n')
		case inCode:
			b.WriteString(t.CodeInlineFn(n.Text))
		case inStrong:
			b.WriteString(t.StrongFn(renderInlineNodes(n.Kids, t)))
		case inEmph:
			b.WriteString(t.EmphasisFn(renderInlineNodes(n.Kids, t)))
		case inStrike:
			b.WriteString(t.StrikeFn(renderInlineNodes(n.Kids, t)))
		case inMark:
			body := n.Text
			if len(n.Kids) > 0 {
				body = renderInlineNodes(n.Kids, identityTheme(t))
			}
			if t.MarkFn != nil {
				b.WriteString(t.MarkFn(body))
			} else {
				b.WriteString(t.StrongFn(body))
			}
		case inMath:
			body := renderMathText(n.Text)
			if t.MathFn != nil {
				b.WriteString(t.MathFn(body))
			} else {
				b.WriteString(t.CodeInlineFn("⟨" + body + "⟩"))
			}
		case inFootnote:
			label := n.Text
			if label == "" {
				label = "*"
			}
			b.WriteString(t.LinkLabelFn("[" + label + "]"))
		case inLink:
			label := n.Text
			if len(n.Kids) > 0 {
				label = renderInlineNodes(n.Kids, identityTheme(t))
			}
			if t.LinkRendererFn != nil {
				b.WriteString(t.LinkRendererFn(label, n.URL))
			} else if label == "" || label == n.URL {
				b.WriteString(t.LinkLabelFn(n.URL))
			} else {
				b.WriteString(t.LinkLabelFn(label))
			}
		case inImage:
			alt := n.Text
			if alt == "" {
				alt = n.URL
			}
			label := "🖼 " + alt
			if t.LinkRendererFn != nil && n.URL != "" {
				b.WriteString(t.LinkRendererFn(label, n.URL))
			} else {
				b.WriteString(t.LinkLabelFn(label))
				if n.URL != "" {
					b.WriteString(" ")
					b.WriteString(t.LinkURLFn("(" + n.URL + ")"))
				}
			}
		}
	}
	return b.String()
}

// identityTheme copies t but strips nested style functions so link labels
// keep their own inner markup without double-wrapping OSC8.
func identityTheme(t MarkdownTheme) MarkdownTheme {
	id := func(s string) string { return s }
	t.EmphasisFn = id
	t.StrongFn = id
	t.StrikeFn = id
	t.MarkFn = id
	t.CodeInlineFn = id
	t.LinkLabelFn = id
	t.LinkURLFn = id
	t.LinkRendererFn = nil
	t.MathFn = id
	return t
}

func padLines(lines []string, width int64, indent int) []string {
	if indent <= 0 {
		out := make([]string, len(lines))
		for i, ln := range lines {
			out[i] = core.PadToWidth(ln, width)
		}
		return out
	}
	prefix := strings.Repeat(" ", indent)
	out := make([]string, len(lines))
	for i, ln := range lines {
		out[i] = core.PadToWidth(prefix+ln, width)
	}
	return out
}

func maxWidth(width int64, indent int) int64 {
	if width <= 0 {
		return 0
	}
	w := width - int64(indent)
	if w < 1 {
		return 1
	}
	return w
}

// nestedWidth is the inner width for quotes/lists/tables. Zero means
// unlimited (no wrap / no truncation), matching PadToWidth / WrapAnsi.
func nestedWidth(width int64, consumed int) int64 {
	if width <= 0 {
		return 0
	}
	w := width - int64(consumed)
	if w < 1 {
		return 1
	}
	return w
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
