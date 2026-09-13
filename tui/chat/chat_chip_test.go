package chat

import (
	"strings"
	"testing"
	"time"

	core "github.com/covoyage/covonaut/tui/core"
	component "github.com/covoyage/covonaut/tui/component"
)

// TestChipCopyButton 验证 turn footer chip 的复制流程：渲染（竖线分隔）、
// 点击「复制」展开格式菜单（Markdown / 解析后）、选择格式后复制该 turn
// 的助手输出、复制后短暂显示「已复制」、点在菜单外收起菜单。
func TestChipCopyButton(t *testing.T) {
	h := NewChatHistory()
	h.SetMaxRows(railTestMaxRows)
	var copied string
	h.SetOnCopy(func(text string) { copied = text })

	const md = "# 标题\n\n第一段 **加粗** 文本。\n\n- 甲\n- 乙\n\n```go\nfmt.Println()\n```\n"
	h.Append(ChatMessage{Role: RoleUser, Text: "上海天气"})
	h.Append(ChatMessage{
		Role:       RoleAssistant,
		Text:       md,
		ID:         "asst-1",
		FooterChip: "general · gpt-x · 5.5s",
	})
	lines := h.Render(railTestWidth)

	// chip 行：▸ chip │ ⧉ 复制
	chipRow := -1
	for r, ln := range lines {
		if strings.Contains(ln, chipCopyLabel) && strings.Contains(ln, "│") {
			chipRow = r
		}
	}
	if chipRow < 0 {
		t.Fatalf("chip copy button not rendered:\n%s", strings.Join(lines, "\n"))
	}

	// 点击 chip 行按钮段：列 = "  ▸ "(4) + chip 宽 + " │ "(3) 起。
	const chip = "general · gpt-x · 5.5s"
	labelCol := int64(4 + len(chip) + 3)
	h.Update(core.MouseMsg{Action: core.MousePress, Row: int64(chipRow), Col: labelCol})
	h.Update(core.MouseMsg{Action: core.MouseRelease, Row: int64(chipRow), Col: labelCol})

	// 第一次点击只展开格式菜单，不复制。
	if copied != "" {
		t.Fatalf("first click should only open the format menu, copied=%q", copied)
	}
	lines = h.Render(railTestWidth)
	menuRow := -1
	for r, ln := range lines {
		if strings.Contains(ln, chipOptMD) && strings.Contains(ln, chipOptPlain) {
			menuRow = r
		}
	}
	if menuRow < 0 {
		t.Fatalf("format menu not rendered after first click:\n%s", strings.Join(lines, "\n"))
	}

	// 点击「⧉ Markdown」：复制 markdown 源码。
	h.Update(core.MouseMsg{Action: core.MousePress, Row: int64(menuRow), Col: labelCol})
	h.Update(core.MouseMsg{Action: core.MouseRelease, Row: int64(menuRow), Col: labelCol})
	if copied != strings.TrimSpace(md) {
		t.Fatalf("markdown copy mismatch:\n got %q\nwant %q", copied, md)
	}

	// 复制后短暂显示「已复制」。
	lines = h.Render(railTestWidth)
	found := false
	for _, ln := range lines {
		if strings.Contains(ln, chipCopiedLabel) {
			found = true
		}
	}
	if !found {
		t.Fatalf("copied feedback label not rendered after click")
	}

	// 反馈与菜单过期后恢复普通按钮（模拟到期定时器：改时刻 + 置 dirty 触发重建）。
	h.mu.Lock()
	h.copiedAt = time.Now().Add(-3 * time.Second)
	h.chipMenuAt = time.Now().Add(-2 * chipMenuFor)
	h.dirty = true
	for i := range h.messages {
		if h.messages[i].ID == "asst-1" {
			h.messages[i].cachedLines = nil
		}
	}
	h.mu.Unlock()
	lines = h.Render(railTestWidth)
	for _, ln := range lines {
		if strings.Contains(ln, chipCopiedLabel) {
			t.Fatalf("copied feedback should expire")
		}
	}

	// 重新打开菜单并点击「⧉ 解析后」：复制解析后的纯文本（无 markdown 标记）。
	chipRow = -1
	for r, ln := range lines {
		if strings.Contains(ln, chipCopyLabel) && strings.Contains(ln, "│") {
			chipRow = r
		}
	}
	if chipRow < 0 {
		t.Fatalf("chip copy button not restored after expiry")
	}
	h.Update(core.MouseMsg{Action: core.MousePress, Row: int64(chipRow), Col: labelCol})
	h.Update(core.MouseMsg{Action: core.MouseRelease, Row: int64(chipRow), Col: labelCol})
	lines = h.Render(railTestWidth)
	menuRow = -1
	plainCol := int64(-1)
	for r, ln := range lines {
		if strings.Contains(ln, chipOptMD) && strings.Contains(ln, chipOptPlain) {
			menuRow = r
			// 解析后选项列 = 前缀 + "⎘ Markdown" 宽 + " │ "。
			plainCol = labelCol + core.VisibleWidth(chipOptMD) + 3
		}
	}
	if menuRow < 0 {
		t.Fatalf("format menu not rendered on second open")
	}
	copied = ""
	h.Update(core.MouseMsg{Action: core.MousePress, Row: int64(menuRow), Col: plainCol})
	h.Update(core.MouseMsg{Action: core.MouseRelease, Row: int64(menuRow), Col: plainCol})
	plain := component.MarkdownPlain(md)
	if copied != plain {
		t.Fatalf("plain copy mismatch:\n got %q\nwant %q", copied, plain)
	}
	if strings.Contains(copied, "# ") || strings.Contains(copied, "**") || strings.Contains(copied, "```") {
		t.Fatalf("plain copy should not contain markdown markers: %q", copied)
	}

	// 点击 chip 行按钮之外（chip 文本上）不触发复制、不启动选区。
	h.mu.Lock()
	h.chipMenuID = ""
	h.chipMenuAt = time.Now().Add(-2 * chipMenuFor)
	h.dirty = true
	for i := range h.messages {
		if h.messages[i].ID == "asst-1" {
			h.messages[i].cachedLines = nil
		}
	}
	h.mu.Unlock()
	lines = h.Render(railTestWidth)
	chipRow = -1
	for r, ln := range lines {
		if strings.Contains(ln, chipCopyLabel) && strings.Contains(ln, "│") {
			chipRow = r
		}
	}
	copied = ""
	h.Update(core.MouseMsg{Action: core.MousePress, Row: int64(chipRow), Col: 2})
	h.Update(core.MouseMsg{Action: core.MouseRelease, Row: int64(chipRow), Col: 2})
	if copied != "" {
		t.Fatalf("click outside button copied unexpectedly")
	}
}
