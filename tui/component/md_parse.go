package component

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/yuin/goldmark/v2/ast"
	"github.com/yuin/goldmark/v2/extension"
	eastast "github.com/yuin/goldmark/v2/extension/ast"
	"github.com/yuin/goldmark/v2/parser"
	"github.com/yuin/goldmark/v2/text"
	"github.com/yuin/goldmark/v2/util"
)

var (
	mdParserOnce sync.Once
	mdParser     parser.Parser
)

func markdownParser() parser.Parser {
	mdParserOnce.Do(func() {
		mdParser = parser.New(
			parser.WithExtensions(
				extension.GFMParser,
				extension.FootnoteParser,
				extension.DefinitionListParser,
				extension.NewTypographerParser(
					extension.WithTypographicSubstitutions(map[extension.TypographicPunctuation]string{
						extension.LeftSingleQuote:  "‘",
						extension.RightSingleQuote: "’",
						extension.LeftDoubleQuote:  "“",
						extension.RightDoubleQuote: "”",
						extension.EnDash:           "–",
						extension.EmDash:           "—",
						extension.Ellipsis:         "…",
						extension.LeftAngleQuote:   "«",
						extension.RightAngleQuote:  "»",
						extension.Apostrophe:       "’",
					}),
				),
			),
			parser.WithBlockParsers(util.Prioritized[parser.BlockParser](&mathBlockParser{}, 650)),
			parser.WithInlineParsers(
				util.Prioritized[parser.InlineParser](&mathInlineParser{}, 50),
				util.Prioritized[parser.InlineParser](&markInlineParser{}, 40),
			),
		)
	})
	return mdParser
}

func parseMarkdown(src string) []mdBlock {
	if src == "" {
		return nil
	}
	source := []byte(src)
	doc := markdownParser().Parse(source)
	return unwrapMarkdownFences(convertBlocks(doc, source))
}

// unwrapMarkdownFences re-parses fenced bodies tagged as markdown when they
// contain a GFM table. Models often wrap tables in ```md fences; rendering
// those as code hides the table.
func unwrapMarkdownFences(blocks []mdBlock) []mdBlock {
	var out []mdBlock
	for _, b := range blocks {
		if len(b.Children) > 0 {
			b.Children = unwrapMarkdownFences(b.Children)
		}
		if b.Kind == mdFence && isRenderedMarkdownLang(b.Lang) && fenceContainsGFMTable(b.Lines) {
			inner := strings.Join(b.Lines, "\n")
			out = append(out, parseMarkdown(inner)...)
			continue
		}
		out = append(out, b)
	}
	return out
}

func isRenderedMarkdownLang(lang string) bool {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "md", "markdown", "gfm":
		return true
	default:
		return false
	}
}

func fenceContainsGFMTable(lines []string) bool {
	prev := ""
	for _, ln := range lines {
		trim := strings.TrimSpace(ln)
		if trim == "" {
			continue
		}
		if prev != "" && isGFMTableDelimiter(trim) && strings.Contains(prev, "|") {
			return true
		}
		prev = trim
	}
	return false
}

func looksLikeTableRow(s string) bool {
	if strings.HasPrefix(s, "|") || strings.HasSuffix(s, "|") {
		return true
	}
	return strings.Count(s, "|") >= 2
}

func isGFMTableDelimiter(s string) bool {
	if !strings.Contains(s, "---") {
		return false
	}
	for _, r := range s {
		switch r {
		case '|', '-', ':', ' ', '\t':
		default:
			return false
		}
	}
	return true
}

func convertBlocks(parent ast.Node, source []byte) []mdBlock {
	var out []mdBlock
	for c := parent.FirstChild(); c != nil; c = c.NextSibling() {
		if b, ok := c.(ast.BlockNode); ok && b.HasBlankPreviousLines() && len(out) > 0 {
			out = append(out, mdBlock{Kind: mdBlank})
		}
		out = append(out, convertNode(c, source)...)
	}
	return out
}

func convertNode(n ast.Node, source []byte) []mdBlock {
	switch t := n.(type) {
	case *ast.Heading:
		return []mdBlock{{Kind: mdHeading, Level: t.Level, Inlines: convertInlines(t, source)}}
	case *ast.Paragraph:
		inlines := convertInlines(t, source)
		if len(inlines) == 0 {
			return nil
		}
		return splitBlocksWithImages(mdPara, inlines)
	case *ast.ThematicBreak:
		return []mdBlock{{Kind: mdHR}}
	case *ast.CodeBlock:
		lang, _ := t.Language(source)
		return []mdBlock{convertFence(t.Value.Str(source), lang)}
	case *ast.Blockquote:
		children := convertBlocks(t, source)
		alert, children := peelAlert(children)
		return []mdBlock{{Kind: mdQuote, Alert: alert, Children: children}}
	case *ast.List:
		return []mdBlock{convertList(t, source)}
	case *ast.HTMLBlock:
		body := strings.TrimSpace(stripHTMLTags(t.Value.Str(source)))
		if body == "" {
			return nil
		}
		return []mdBlock{{Kind: mdPara, Inlines: []inlineNode{{Kind: inText, Text: body}}}}
	case *eastast.Table:
		return []mdBlock{convertTable(t, source)}
	case *eastast.FootnoteDefinition:
		return []mdBlock{convertFootnoteDef(t, source)}
	case *eastast.DefinitionList:
		return []mdBlock{convertDefList(t, source)}
	case *mdMathBlock:
		return []mdBlock{{Kind: mdMath, Text: strings.TrimSpace(t.Value.Str(source))}}
	case *ast.ListItem:
		return []mdBlock{convertListItem(t, source)}
	default:
		if n.HasChildren() {
			return convertBlocks(n, source)
		}
		return nil
	}
}

func splitBlocksWithImages(kind mdBlockKind, inlines []inlineNode) []mdBlock {
	if !inlineHasImage(inlines) {
		return []mdBlock{{Kind: kind, Inlines: inlines}}
	}
	var out []mdBlock
	var cur []inlineNode
	flush := func() {
		if inlineOnlySpace(cur) {
			cur = nil
			return
		}
		out = append(out, mdBlock{Kind: kind, Inlines: cur})
		cur = nil
	}
	for _, n := range inlines {
		if n.Kind == inImage {
			flush()
			out = append(out, mdBlock{Kind: mdImage, Text: n.Text, Info: n.URL})
			continue
		}
		cur = append(cur, n)
	}
	flush()
	return out
}

func inlineHasImage(inlines []inlineNode) bool {
	for _, n := range inlines {
		if n.Kind == inImage {
			return true
		}
	}
	return false
}

func inlineOnlySpace(inlines []inlineNode) bool {
	if len(inlines) == 0 {
		return true
	}
	for _, n := range inlines {
		switch n.Kind {
		case inText:
			if strings.TrimSpace(n.Text) != "" {
				return false
			}
		case inBreak:
			continue
		default:
			return false
		}
	}
	return true
}

func convertFence(raw, lang string) mdBlock {
	raw = strings.TrimSuffix(raw, "\n")
	var lines []string
	if raw == "" {
		lines = nil
	} else {
		lines = strings.Split(raw, "\n")
	}
	return mdBlock{Kind: mdFence, Lang: lang, Lines: lines}
}

func convertList(n *ast.List, source []byte) mdBlock {
	blk := mdBlock{
		Kind:    mdList,
		Ordered: n.IsOrdered(),
		Start:   n.Start,
		Tight:   n.IsTight,
	}
	if blk.Start <= 0 && blk.Ordered {
		blk.Start = 1
	}
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		item, ok := c.(*ast.ListItem)
		if !ok {
			continue
		}
		blk.Children = append(blk.Children, convertListItem(item, source))
	}
	return blk
}

func convertListItem(n *ast.ListItem, source []byte) mdBlock {
	item := mdBlock{Kind: mdListItem, MarkerW: n.Offset()}
	if status, ok := extension.TaskStatusOf(n); ok {
		checked := status == extension.TaskStatusCompleted
		item.Checked = &checked
	}
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		item.Children = append(item.Children, convertNode(c, source)...)
	}
	return item
}

func convertTable(n *eastast.Table, source []byte) mdBlock {
	blk := mdBlock{Kind: mdTable}
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch row := c.(type) {
		case *eastast.TableHeader:
			for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
				if tc, ok := cell.(*eastast.TableCell); ok {
					blk.Align = append(blk.Align, tableAlign(tc.Alignment))
					blk.HeaderCells = append(blk.HeaderCells, convertInlines(cell, source))
				}
			}
		case *eastast.TableBody:
			for r := row.FirstChild(); r != nil; r = r.NextSibling() {
				if tr, ok := r.(*eastast.TableRow); ok {
					blk.RowCells = append(blk.RowCells, convertTableRow(tr, source))
				}
			}
		case *eastast.TableRow:
			blk.RowCells = append(blk.RowCells, convertTableRow(row, source))
		}
	}
	return blk
}

func convertTableRow(row *eastast.TableRow, source []byte) [][]inlineNode {
	var cells [][]inlineNode
	for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
		cells = append(cells, convertInlines(cell, source))
	}
	return cells
}

func tableAlign(a eastast.Alignment) mdAlign {
	switch a {
	case eastast.AlignLeft:
		return mdAlignLeft
	case eastast.AlignRight:
		return mdAlignRight
	case eastast.AlignCenter:
		return mdAlignCenter
	default:
		return mdAlignNone
	}
}

func convertInlines(n ast.Node, source []byte) []inlineNode {
	var out []inlineNode
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch t := c.(type) {
		case *ast.Text:
			out = append(out, inlineNode{Kind: inText, Text: t.Value.Value(source)})
			if t.HardLineBreak() {
				out = append(out, inlineNode{Kind: inBreak})
			} else if t.SoftLineBreak() {
				out = append(out, inlineNode{Kind: inText, Text: " "})
			}
		case *ast.CodeSpan:
			out = append(out, inlineNode{Kind: inCode, Text: t.Value.Value(source)})
		case *ast.Emphasis:
			out = append(out, inlineNode{Kind: inEmph, Kids: convertInlines(t, source)})
		case *ast.Strong:
			out = append(out, inlineNode{Kind: inStrong, Kids: convertInlines(t, source)})
		case *eastast.Strikethrough:
			out = append(out, inlineNode{Kind: inStrike, Kids: convertInlines(t, source)})
		case *ast.Link:
			kids := convertInlines(t, source)
			out = append(out, inlineNode{
				Kind: inLink,
				Text: inlinePlainText(kids),
				URL:  t.Destination.Value(source),
				Kids: kids,
			})
		case *ast.Image:
			kids := convertInlines(t, source)
			out = append(out, inlineNode{
				Kind: inImage,
				Text: inlinePlainText(kids),
				URL:  t.Destination.Value(source),
			})
		case *ast.AutoLink:
			label := t.Label.Value(source)
			url := t.Destination.Value(source)
			out = append(out, inlineNode{
				Kind: inLink,
				Text: label,
				URL:  url,
				Kids: []inlineNode{{Kind: inText, Text: label}},
			})
		case *mdMathInline:
			out = append(out, inlineNode{Kind: inMath, Text: t.Value.Value(source)})
		case *mdMark:
			out = append(out, inlineNode{Kind: inMark, Text: t.Value.Value(source)})
		case *eastast.FootnoteReference:
			label := strings.TrimSpace(t.Label.Value(source))
			if t.Index >= 1 {
				label = fmt.Sprintf("%d", t.Index)
			}
			out = append(out, inlineNode{Kind: inFootnote, Text: label})
		case *ast.RawHTML:
			if txt := stripHTMLTags(t.Value.Value(source)); txt != "" {
				out = append(out, inlineNode{Kind: inText, Text: txt})
			}
		default:
			if c.HasChildren() {
				out = append(out, convertInlines(c, source)...)
			}
		}
	}
	return out
}

func convertFootnoteDef(n *eastast.FootnoteDefinition, source []byte) mdBlock {
	label := strings.TrimSpace(n.Label.Value(source))
	return mdBlock{
		Kind:     mdFootnote,
		Text:     label,
		Children: convertBlocks(n, source),
	}
}

func convertDefList(n *eastast.DefinitionList, source []byte) mdBlock {
	blk := mdBlock{Kind: mdDefList}
	var current mdBlock
	flush := func() {
		if current.Kind == mdDefItem {
			blk.Children = append(blk.Children, current)
			current = mdBlock{}
		}
	}
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch t := c.(type) {
		case *eastast.DefinitionTerm:
			flush()
			current = mdBlock{Kind: mdDefItem, Inlines: convertInlines(t, source)}
		case *eastast.DefinitionDescription:
			if current.Kind != mdDefItem {
				current = mdBlock{Kind: mdDefItem}
			}
			current.Children = append(current.Children, convertBlocks(t, source)...)
		default:
			flush()
			blk.Children = append(blk.Children, convertNode(c, source)...)
		}
	}
	flush()
	return blk
}

// peelAlert detects a GitHub-style alert marker as the first paragraph of a
// blockquote: [!NOTE], [!TIP], [!IMPORTANT], [!WARNING], [!CAUTION].
func peelAlert(children []mdBlock) (string, []mdBlock) {
	if len(children) == 0 {
		return "", children
	}
	idx := 0
	if children[0].Kind == mdBlank {
		if len(children) < 2 {
			return "", children
		}
		idx = 1
	}
	first := children[idx]
	if first.Kind != mdPara {
		return "", children
	}
	kind, rest, ok := peelAlertInlines(first.Inlines)
	if !ok {
		return "", children
	}
	out := make([]mdBlock, 0, len(children)-idx)
	if len(rest) > 0 {
		first.Inlines = rest
		out = append(out, first)
	}
	out = append(out, children[idx+1:]...)
	return kind, out
}

func peelAlertInlines(inlines []inlineNode) (kind string, rest []inlineNode, ok bool) {
	i := 0
	for i < len(inlines) && inlines[i].Kind == inText && strings.TrimSpace(inlines[i].Text) == "" {
		i++
	}
	if i >= len(inlines) {
		return "", inlines, false
	}
	// Link parsers split "[!NOTE]" into "[", "!NOTE", "]" text nodes.
	var joined strings.Builder
	j := i
	for j < len(inlines) && inlines[j].Kind == inText {
		joined.WriteString(inlines[j].Text)
		j++
		if joined.Len() >= 4 && strings.ContainsRune(joined.String(), ']') {
			break
		}
	}
	text := strings.TrimLeft(joined.String(), " \t")
	if len(text) < 4 || text[0] != '[' || text[1] != '!' {
		return "", inlines, false
	}
	end := strings.IndexByte(text, ']')
	if end < 3 {
		return "", inlines, false
	}
	kind = strings.ToUpper(text[2:end])
	switch kind {
	case "NOTE", "TIP", "IMPORTANT", "WARNING", "CAUTION":
	default:
		return "", inlines, false
	}
	remain := strings.TrimSpace(text[end+1:])
	if remain != "" {
		rest = append(rest, inlineNode{Kind: inText, Text: remain})
	}
	rest = append(rest, inlines[j:]...)
	for len(rest) > 0 && rest[0].Kind == inText && strings.TrimSpace(rest[0].Text) == "" {
		rest = rest[1:]
	}
	if len(rest) > 0 && rest[0].Kind == inText {
		rest[0].Text = strings.TrimLeft(rest[0].Text, " \t")
		if rest[0].Text == "" {
			rest = rest[1:]
		}
	}
	return kind, rest, true
}

func inlinePlainText(nodes []inlineNode) string {
	var b strings.Builder
	for _, n := range nodes {
		if n.Text != "" {
			b.WriteString(n.Text)
		}
		if len(n.Kids) > 0 {
			b.WriteString(inlinePlainText(n.Kids))
		}
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Incomplete fences / math (streaming). Unclosed fences are closed at
// EOF by the parser, so SkipIncomplete is decided by a source scan.
// ---------------------------------------------------------------------------

func IncompleteMarkdown(src string) (fence, math bool) {
	_, fence, math = scanIncomplete(src)
	return fence, math
}

func clipIncompleteMarkdown(src string) string {
	cut, fence, math := scanIncomplete(src)
	if fence || math {
		if cut <= 0 {
			src = ""
		} else {
			src = src[:cut]
		}
	}
	if tableCut, ok := incompleteTableCut(src); ok {
		if tableCut <= 0 {
			return ""
		}
		return src[:tableCut]
	}
	return src
}

func scanIncomplete(src string) (cut int, fence, math bool) {
	if src == "" {
		return 0, false, false
	}
	start := 0
	offset := 0
	inFence := false
	inMath := false
	var fenceMark byte
	fenceLen := 0
	for {
		line, rest, ok := nextLine(src[offset:])
		lineStart := offset
		if !ok {
			break
		}
		offset += len(line)
		if inFence {
			if fenceCloseLine(line, fenceMark, fenceLen) {
				inFence = false
			}
			if !rest {
				break
			}
			continue
		}
		if inMath {
			if strings.Contains(line, "$$") {
				inMath = false
			}
			if !rest {
				break
			}
			continue
		}
		if mark, n, ok := fenceOpenLine(line); ok {
			inFence = true
			fenceMark = mark
			fenceLen = n
			start = lineStart
			if !rest {
				break
			}
			continue
		}
		if isMathOpenLine(line) {
			inMath = true
			start = lineStart
		}
		if !rest {
			break
		}
	}
	if inFence || inMath {
		return start, inFence, inMath
	}
	return 0, false, false
}

func incompleteTableCut(src string) (int, bool) {
	if src == "" {
		return 0, false
	}
	offset := 0
	pending := -1
	sawHeader := false
	for {
		line, rest, ok := nextLine(src[offset:])
		if !ok {
			break
		}
		lineStart := offset
		offset += len(line)
		trim := strings.TrimSpace(strings.TrimRight(line, "\r\n"))
		if trim == "" {
			if pending >= 0 && !sawHeader {
				pending = -1
			}
			if !rest {
				break
			}
			continue
		}
		if looksLikeTableRow(trim) {
			if pending < 0 {
				pending = lineStart
				sawHeader = false
			}
			if isGFMTableDelimiter(trim) {
				sawHeader = true
			}
			if !rest {
				break
			}
			continue
		}
		if pending >= 0 && !sawHeader {
			pending = -1
		}
		if !rest {
			break
		}
	}
	if pending >= 0 && !sawHeader {
		return pending, true
	}
	return 0, false
}

func nextLine(s string) (line string, more bool, ok bool) {
	if s == "" {
		return "", false, false
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i+1], true, true
	}
	return s, false, true
}

func fenceOpenLine(line string) (mark byte, n int, ok bool) {
	s := strings.TrimLeft(line, " ")
	if indent := len(line) - len(s); indent > 3 {
		return 0, 0, false
	}
	if s == "" {
		return 0, 0, false
	}
	ch := s[0]
	if ch != '`' && ch != '~' {
		return 0, 0, false
	}
	i := 0
	for i < len(s) && s[i] == ch {
		i++
	}
	if i < 3 {
		return 0, 0, false
	}
	info := strings.TrimSpace(strings.TrimRight(s[i:], "\r\n"))
	if ch == '`' && strings.Contains(info, "`") {
		return 0, 0, false
	}
	return ch, i, true
}

func fenceCloseLine(line string, mark byte, n int) bool {
	s := strings.TrimLeft(line, " ")
	if indent := len(line) - len(s); indent > 3 {
		return false
	}
	if s == "" || s[0] != mark {
		return false
	}
	i := 0
	for i < len(s) && s[i] == mark {
		i++
	}
	if i < n {
		return false
	}
	return strings.TrimSpace(strings.TrimRight(s[i:], "\r\n")) == ""
}

func isMathOpenLine(line string) bool {
	s := strings.TrimSpace(line)
	if !strings.HasPrefix(s, "$$") {
		return false
	}
	rest := strings.TrimPrefix(s, "$$")
	return !strings.Contains(rest, "$$")
}

// ---------------------------------------------------------------------------
// Math nodes / parsers ($...$ and $$...$$)
// ---------------------------------------------------------------------------

var (
	kindMathBlock  = ast.NewNodeKind("MathBlock")
	kindMathInline = ast.NewNodeKind("MathInline")
)

type mdMathBlock struct {
	ast.BaseBlock
	Value  text.Lines
	closed bool
}

func newMathBlock() *mdMathBlock {
	n := &mdMathBlock{}
	n.Init(n)
	return n
}

func (n *mdMathBlock) Dump(_ []byte) *ast.NodeDump {
	return ast.NewNodeDump(n, map[string]any{"Value": n.Value})
}
func (n *mdMathBlock) Kind() ast.NodeKind { return kindMathBlock }

type mdMathInline struct {
	ast.BaseInline
	Value text.SingleLineValue
}

func newMathInline(v text.SingleLineValue) *mdMathInline {
	n := &mdMathInline{Value: v}
	n.Init(n)
	return n
}

func (n *mdMathInline) Dump(_ []byte) *ast.NodeDump {
	return ast.NewNodeDump(n, map[string]any{"Value": n.Value})
}
func (n *mdMathInline) Kind() ast.NodeKind { return kindMathInline }

type mathBlockParser struct{}

func (b *mathBlockParser) Trigger() []byte { return []byte{'$'} }

func (b *mathBlockParser) Open(_ ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	line, segment := reader.PeekLine()
	pos := pc.BlockOffset()
	if pos < 0 || pos+1 >= len(line) || line[pos] != '$' || line[pos+1] != '$' {
		return nil, parser.NoChildren
	}
	node := newMathBlock()
	rest := line[pos+2:]
	rest = bytes.TrimRight(rest, "\r\n")
	if idx := bytes.Index(rest, []byte("$$")); idx >= 0 {
		start := segment.Start + pos + 2
		if idx > 0 {
			node.Value.AppendSegment(text.NewSegment(start, start+idx))
		}
		node.closed = true
		reader.AdvanceToEOL()
		return node, parser.NoChildren
	}
	if len(bytes.TrimSpace(rest)) > 0 {
		start := segment.Start + pos + 2
		stop := segment.Stop
		for stop > start && (sourceAt(reader, stop-1) == '\n' || sourceAt(reader, stop-1) == '\r') {
			stop--
		}
		if stop > start {
			node.Value.AppendSegment(text.NewSegment(start, stop))
		}
	}
	reader.AdvanceToEOL()
	return node, parser.NoChildren
}

func sourceAt(reader text.Reader, i int) byte {
	src := reader.Source()
	if i < 0 || i >= len(src) {
		return 0
	}
	return src[i]
}

func (b *mathBlockParser) Continue(node ast.Node, reader text.Reader, _ parser.Context) parser.State {
	n := node.(*mdMathBlock)
	if n.closed {
		return parser.Close
	}
	line, segment := reader.PeekLine()
	if idx := bytes.Index(line, []byte("$$")); idx >= 0 {
		if idx > 0 {
			n.Value.AppendSegment(text.NewSegment(segment.Start, segment.Start+idx))
		}
		n.closed = true
		reader.AdvanceToEOL()
		return parser.Close
	}
	n.Value.AppendSegment(segment)
	reader.AdvanceToEOL()
	return parser.Continue | parser.NoChildren
}

func (b *mathBlockParser) Close(ast.Node, text.Reader, parser.Context) {}
func (b *mathBlockParser) CanInterruptParagraph() bool                 { return true }
func (b *mathBlockParser) CanAcceptIndentedLine() bool                 { return false }

type mathInlineParser struct{}

func (s *mathInlineParser) Trigger() []byte { return []byte{'$'} }

func (s *mathInlineParser) Parse(_ ast.Node, block text.Reader, _ parser.Context) ast.Node {
	line, segment := block.PeekLine()
	if len(line) < 2 || line[0] != '$' || line[1] == '$' {
		return nil
	}
	before := block.PrecedingCharacter()
	if before != 0 && !unicode.IsSpace(before) && before != '(' && before != '[' && before != '{' {
		return nil
	}
	j := 1
	for j < len(line) {
		if line[j] == '\\' && j+1 < len(line) {
			j += 2
			continue
		}
		if line[j] == '\n' {
			return nil
		}
		if line[j] == '$' {
			inner := line[1:j]
			trim := bytes.TrimSpace(inner)
			if len(trim) == 0 || unicode.IsDigit(rune(trim[0])) {
				return nil
			}
			after := j + 1
			if after < len(line) {
				r, _ := utf8.DecodeRune(line[after:])
				if unicode.IsLetter(r) || unicode.IsDigit(r) {
					return nil
				}
			}
			start := segment.Start + 1
			stop := segment.Start + j
			block.Advance(j + 1)
			return newMathInline(text.NewSingleLineValueFromIndex(text.NewIndex(start, stop), text.IdentityDecoder))
		}
		j++
	}
	return nil
}

var kindMark = ast.NewNodeKind("Mark")

type mdMark struct {
	ast.BaseInline
	Value text.SingleLineValue
}

func newMark(v text.SingleLineValue) *mdMark {
	n := &mdMark{Value: v}
	n.Init(n)
	return n
}

func (n *mdMark) Dump(_ []byte) *ast.NodeDump {
	return ast.NewNodeDump(n, map[string]any{"Value": n.Value})
}
func (n *mdMark) Kind() ast.NodeKind { return kindMark }

type markInlineParser struct{}

func (s *markInlineParser) Trigger() []byte { return []byte{'='} }

func (s *markInlineParser) Parse(_ ast.Node, block text.Reader, _ parser.Context) ast.Node {
	line, segment := block.PeekLine()
	if len(line) < 4 || line[0] != '=' || line[1] != '=' {
		return nil
	}
	before := block.PrecedingCharacter()
	if before == '=' {
		return nil
	}
	j := 2
	for j < len(line)-1 {
		if line[j] == '\n' {
			return nil
		}
		if line[j] == '=' && line[j+1] == '=' {
			inner := bytes.TrimSpace(line[2:j])
			if len(inner) == 0 {
				return nil
			}
			start := segment.Start + 2
			stop := segment.Start + j
			block.Advance(j + 2)
			return newMark(text.NewSingleLineValueFromIndex(text.NewIndex(start, stop), text.IdentityDecoder))
		}
		j++
	}
	return nil
}

func stripHTMLTags(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	inTag := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '<':
			inTag = true
		case c == '>' && inTag:
			inTag = false
		case !inTag:
			b.WriteByte(c)
		}
	}
	out := htmlUnescape(b.String())
	return strings.TrimSpace(out)
}

func htmlUnescape(s string) string {
	repl := []struct{ from, to string }{
		{"&amp;", "&"},
		{"&lt;", "<"},
		{"&gt;", ">"},
		{"&quot;", `"`},
		{"&#39;", "'"},
		{"&apos;", "'"},
		{"&nbsp;", " "},
		{"&mdash;", "\u2014"},
		{"&ndash;", "\u2013"},
		{"&hellip;", "\u2026"},
		{"&lsquo;", "\u2018"},
		{"&rsquo;", "\u2019"},
		{"&ldquo;", "\u201c"},
		{"&rdquo;", "\u201d"},
		{"&laquo;", "\u00ab"},
		{"&raquo;", "\u00bb"},
	}
	for _, p := range repl {
		s = strings.ReplaceAll(s, p.from, p.to)
	}
	return s
}
