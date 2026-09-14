package tui

import (
	"testing"

	"github.com/covoyage/covonaut/tui/core"
)

// TestMergeTopLayerNarrowAndBlank 验证基本合并：非空白单元格覆盖下层，
// 空白单元格透出下层内容。
func TestMergeTopLayerNarrowAndBlank(t *testing.T) {
	base := core.ParseLine("hello world")
	rows := []core.Row{base}
	// col 6 处一个 X，其余空白。
	top := "      X"
	mergeTopLayer(rows, 0, []string{top})
	got := string([]rune{rows[0].Cells[0].Rune, rows[0].Cells[1].Rune, rows[0].Cells[2].Rune,
		rows[0].Cells[3].Rune, rows[0].Cells[4].Rune, rows[0].Cells[5].Rune, rows[0].Cells[6].Rune})
	if got != "hello X" {
		t.Fatalf("merged row = %q, want %q", got, "hello X")
	}
}

// TestMergeTopLayerWideChar 验证宽字符成对搬运。
func TestMergeTopLayerWideChar(t *testing.T) {
	base := core.ParseLine("abcdef")
	rows := []core.Row{base}
	top := "  中" // col 2 起一个宽字符（占 2、3 两列）
	mergeTopLayer(rows, 0, []string{top})
	if rows[0].Cells[2].Rune != '中' || rows[0].Cells[2].Width != 2 {
		t.Fatalf("wide char not merged at col 2: %+v", rows[0].Cells[2])
	}
	if !rows[0].Cells[3].IsContinuation() {
		t.Fatalf("wide char continuation not merged at col 3: %+v", rows[0].Cells[3])
	}
	// col 4、5 应保持下层内容。
	if rows[0].Cells[4].Rune != 'e' || rows[0].Cells[5].Rune != 'f' {
		t.Fatalf("underlying cells corrupted: %q", string([]rune{rows[0].Cells[4].Rune, rows[0].Cells[5].Rune}))
	}
}

// TestMergeTopLayerClearsPartialWide 验证覆盖点落在宽字符中间时，
// 残缺的另一半被清成空白而不是留下半字符。
func TestMergeTopLayerClearsPartialWide(t *testing.T) {
	// 底图：a b 中 d e —— 宽字符占 2、3 两列。
	base := core.ParseLine("ab中de")
	rows := []core.Row{base}
	// 顶层只在 col 3 写 X（宽字符右半位置）。
	top := "   X"
	mergeTopLayer(rows, 0, []string{top})
	if rows[0].Cells[3].Rune != 'X' {
		t.Fatalf("overlay cell not written: %+v", rows[0].Cells[3])
	}
	// 左半（col 2）应被清空，避免显示残缺宽字符。
	if rows[0].Cells[2].Rune != 0 && rows[0].Cells[2].Rune != ' ' {
		t.Fatalf("partial wide left half not cleared: %+v", rows[0].Cells[2])
	}
}

// TestMergeTopLayerStyledBlankCovers 验证带样式的空格（如浮层投影的
// 暗背景）算非空白，会覆盖下层。
func TestMergeTopLayerStyledBlankCovers(t *testing.T) {
	base := core.ParseLine("abcde")
	rows := []core.Row{base}
	// 256 色背景的空格：rail 浮层投影就是这种「有样式的空白」。
	top := "\x1b[48;5;235m \x1b[0m  Z"
	mergeTopLayer(rows, 0, []string{top})
	if rows[0].Cells[0].Style.Bg == core.DefaultStyle.Bg {
		t.Fatalf("styled blank not merged: %+v", rows[0].Cells[0].Style)
	}
	if rows[0].Cells[3].Rune != 'Z' {
		t.Fatalf("trailing cell not merged at col 3: %+v", rows[0].Cells[3])
	}
	if rows[0].Cells[2].Rune != 'c' {
		t.Fatalf("blank passthrough broken at col 2: %+v", rows[0].Cells[2])
	}
}

// TestMergeTopLayerAlignment 验证 start 偏移对齐：top 层行写入指定起始行。
func TestMergeTopLayerAlignment(t *testing.T) {
	rows := []core.Row{
		core.ParseLine("........."),
		core.ParseLine("........."),
		core.ParseLine("........."),
	}
	top := "XX"
	mergeTopLayer(rows, 2, []string{top})
	if rows[2].Cells[0].Rune != 'X' {
		t.Fatalf("top layer not merged at start row 2: %+v", rows[2].Cells[0])
	}
	if rows[0].Cells[0].Rune != '.' || rows[1].Cells[0].Rune != '.' {
		t.Fatal("rows before start were modified")
	}
	// 超出帧底部的行安全忽略。
	mergeTopLayer(rows, 2, []string{"YY", "ZZ", "WW"})
	if rows[2].Cells[1].Rune != 'Y' {
		t.Fatalf("second line not merged: %+v", rows[2].Cells[1])
	}
}

// TestTopLayerInterfaceSatisfied 编译期确认接口形状。
func TestTopLayerInterfaceSatisfied(t *testing.T) {
	var _ TopLayer = (*topLayerFake)(nil)
}

type topLayerFake struct {
	core.Component
}

func (f *topLayerFake) RenderTopLayer(cols int64) []string { return nil }
