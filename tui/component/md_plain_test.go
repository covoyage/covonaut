package component

import "testing"

func TestMarkdownPlain(t *testing.T) {
	src := "# 标题\n\n第一段 **加粗** 与 `code` 文本。\n\n软换行\n折叠为空格。\n\n- 甲\n- 乙\n  - 嵌套\n\n1. 第一\n2. 第二\n\n> 引用内容\n\n```go\nfmt.Println()\n```\n\n| 列一 | 列二 |\n|---|---|\n| a | b |\n\n[链接](https://example.com)\n"
	want := "标题\n\n第一段 加粗 与 code 文本。\n\n软换行 折叠为空格。\n\n- 甲\n- 乙\n  - 嵌套\n\n1. 第一\n2. 第二\n\n> 引用内容\n\nfmt.Println()\n\n列一 | 列二\na | b\n\n链接 (https://example.com)"
	got := MarkdownPlain(src)
	if got != want {
		t.Fatalf("MarkdownPlain mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	// 硬换行保留为换行。
	got = MarkdownPlain("第一行  \n第二行")
	if got != "第一行\n第二行" {
		t.Fatalf("hard break should be preserved, got %q", got)
	}
	// 语法残留清理：未闭合/带空格的 **、堆叠 ****、行内 $$..$$、块级 $$ 定界符。
	got = MarkdownPlain("未闭合 **加粗 与 空格 ** 加粗 ** 与 **相邻****粗体**")
	if got != "未闭合 加粗 与 空格  加粗  与 相邻粗体" {
		t.Fatalf("unparsed bold residue should be cleaned, got %q", got)
	}
	got = MarkdownPlain("行内 $$a^2$$ 数学")
	if got != "行内 a^2 数学" {
		t.Fatalf("inline $$ should be stripped, got %q", got)
	}
	got = MarkdownPlain("$$\nx = 1\n$$")
	if got != "x = 1" {
		t.Fatalf("block math delimiters should be stripped, got %q", got)
	}
	// 代码块内的 markdown 语法原样保留。
	got = MarkdownPlain("```go\ns := \"**原样** + $$原样$$\"\n```")
	if got != "s := \"**原样** + $$原样$$\"" {
		t.Fatalf("fence body must stay verbatim, got %q", got)
	}
	// 空输入。
	if got := MarkdownPlain(""); got != "" {
		t.Fatalf("empty input should produce empty output, got %q", got)
	}
}
