package component

import (
	"fmt"
	"strings"

	core "github.com/covoyage/covonaut/tui/core"
)

// MarkdownPlain 把 markdown 源码解析成纯文本：段落不硬换行（软换行折叠为
// 空格）、代码块保留原文、列表加 "- "/"N. " 标记、引用加 "> " 前缀、表格
// 按行拼接单元格、链接保留 URL。用于复制「解析后」的内容——与终端渲染
// 的语义一致，但不带样式、边框和按列宽折行。
func MarkdownPlain(src string) string {
	var lines []string
	appendPlainBlocks(&lines, parseMarkdown(src), "", true)
	// 去掉首尾空行。
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// appendPlainBlocks 把一块 block 的纯文本行追加到 lines。prefix 是每行的
// 缩进/引用前缀（引用与嵌套列表会加深）。sep 控制块与块之间是否插入空行
// （紧凑列表的条目之间不插）。
func appendPlainBlocks(lines *[]string, blocks []mdBlock, prefix string, sep bool) {
	for i, b := range blocks {
		if sep && i > 0 && len(*lines) > 0 && (*lines)[len(*lines)-1] != "" {
			*lines = append(*lines, "")
		}
		appendPlainBlock(lines, b, prefix)
	}
}

func appendPlainBlock(lines *[]string, b mdBlock, prefix string) {
	switch b.Kind {
	case mdPara:
		appendPrefixed(lines, plainInlines(b.Inlines), prefix)
	case mdHeading:
		appendPrefixed(lines, plainInlines(b.Inlines), prefix)
	case mdFence:
		for _, ln := range b.Lines {
			*lines = append(*lines, prefix+ln)
		}
	case mdMath:
		txt := strings.TrimSpace(b.Text)
		txt = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(txt, "$$"), "$$"))
		appendPrefixed(lines, txt, prefix)
	case mdHR:
		*lines = append(*lines, prefix+"---")
	case mdImage:
		appendPrefixed(lines, plainRefLabel(b.Text, b.Info), prefix)
	case mdQuote:
		if b.Alert != "" {
			*lines = append(*lines, prefix+"> "+alertLabel(b.Alert))
		}
		appendPlainBlocks(lines, b.Children, prefix+"> ", true)
	case mdList:
		appendPlainList(lines, b, prefix)
	case mdTable:
		appendPlainTable(lines, b, prefix)
	case mdFootnote:
		appendPlainBlocks(lines, b.Children, prefix, true)
	case mdDefList, mdDefItem:
		appendPlainBlocks(lines, b.Children, prefix, true)
	case mdBlank:
		// 块间距由 sep 逻辑统一控制。
	}
}

// appendPlainList 渲染列表：条目带标记，首块内容跟在标记后，后续块按标记
// 宽度缩进；嵌套列表整体缩进。紧凑列表条目之间不插空行。
func appendPlainList(lines *[]string, b mdBlock, prefix string) {
	n := b.Start
	if n < 1 {
		n = 1
	}
	for _, item := range b.Children {
		if item.Kind != mdListItem {
			appendPlainBlock(lines, item, prefix)
			continue
		}
		marker := "- "
		if b.Ordered {
			marker = fmt.Sprintf("%d. ", n)
			n++
		}
		if item.Checked != nil {
			if *item.Checked {
				marker += "[x] "
			} else {
				marker += "[ ] "
			}
		}
		if len(item.Children) == 0 {
			continue
		}
		first, rest := item.Children[0], item.Children[1:]
		// 首块是段落时直接跟在标记后面；否则（嵌套列表等）另起一行。
		if first.Kind == mdPara {
			appendPrefixed(lines, plainInlines(first.Inlines), prefix+marker)
		} else {
			*lines = append(*lines, prefix+strings.TrimRight(marker, " "))
			appendPlainBlock(lines, first, prefix+strings.Repeat(" ", int(core.VisibleWidth(marker))))
		}
		indent := prefix + strings.Repeat(" ", int(core.VisibleWidth(marker)))
		if len(rest) > 0 && !b.Tight && len(*lines) > 0 {
			*lines = append(*lines, "")
		}
		appendPlainBlocks(lines, rest, indent, true)
	}
}

func appendPlainTable(lines *[]string, b mdBlock, prefix string) {
	join := func(cells [][]inlineNode) string {
		parts := make([]string, len(cells))
		for i, c := range cells {
			parts[i] = strings.TrimSpace(plainInlines(c))
		}
		return prefix + strings.Join(parts, " | ")
	}
	if len(b.HeaderCells) > 0 {
		*lines = append(*lines, join(b.HeaderCells))
	}
	for _, row := range b.RowCells {
		*lines = append(*lines, join(row))
	}
}

// appendPrefixed 把 text（可能含硬换行）按行拆开追加，每行加前缀。
func appendPrefixed(lines *[]string, text, prefix string) {
	if text == "" {
		return
	}
	for _, ln := range strings.Split(text, "\n") {
		*lines = append(*lines, prefix+ln)
	}
}

// plainInlines 把 inline 节点序列摊平成纯文本：样式节点只保留内容，
// 链接/图片补 " (url)"，硬换行转 "\n"。
func plainInlines(inlines []inlineNode) string {
	var sb strings.Builder
	for _, n := range inlines {
		switch n.Kind {
		case inText:
			sb.WriteString(plainTextCleanup(n.Text))
		case inCode, inMath:
			sb.WriteString(n.Text)
		case inStrong, inEmph, inStrike, inMark:
			sb.WriteString(plainInlines(n.Kids))
		case inLink:
			sb.WriteString(plainRefLabel(plainInlines(n.Kids), n.URL))
		case inImage:
			sb.WriteString(plainRefLabel(n.Text, n.URL))
		case inBreak:
			sb.WriteString("\n")
		case inFootnote:
			sb.WriteString(n.Text)
		}
	}
	return sb.String()
}

// plainRefLabel 组合「标签 (url)」；url 为空或与标签相同时只留标签。
func plainRefLabel(label, url string) string {
	if url == "" || url == label {
		return label
	}
	return label + " (" + url + ")"
}

// plainTextCleanup 清掉解析器吃不掉的语法残留：未闭合/带空格/相邻堆叠的
// "**"" ~~" 会原样留在 Text 节点里；不在独立行上的 "$$...$$" 不会被解析成
// 数学块。code span 与代码块里的内容走 inCode/Lines，不受影响。
// 单个 "*"、"$"、"__"（如 __init__）有合法用途，不动。
func plainTextCleanup(text string) string {
	if !strings.Contains(text, "**") && !strings.Contains(text, "~~") &&
		!strings.Contains(text, "$$") {
		return text
	}
	text = strings.ReplaceAll(text, "**", "")
	text = strings.ReplaceAll(text, "~~", "")
	// 成对的 "$$"：保留内层，去掉定界符；不成对则原样保留。
	for {
		i := strings.Index(text, "$$")
		if i < 0 {
			break
		}
		rest := text[i+2:]
		j := strings.Index(rest, "$$")
		if j < 0 {
			break
		}
		text = text[:i] + rest[:j] + rest[j+2:]
	}
	return text
}
