package tui

import (
	"github.com/covoyage/covonaut/tui/core"
)

// TopLayer 是组件的可选接口：实现它的组件会在所有 overlay 合成完成之后
// 再绘制一层内容——整帧 z 序的最高层。典型用途是输入记录横条这类必须
// 悬浮于一切元素（键位帮助、会话选择等 overlay）之上的贴边装饰。
//
// 合同约定：
//   - 只有当本帧存在 overlay 时 TUI 才会调用 RenderTopLayer（无 overlay
//     时组件自身的 Render 输出已经是最高层，跳过可省一次重绘）。
//   - 返回的行与该组件本帧 Render 输出按行对齐（TUI 在渲染 children 时
//     记录了每个 child 的起始行偏移）。
//   - 只返回需要覆盖的单元格，其余位置保持空白——合并时空白单元格会
//     透出下层（overlay 或底图）。
type TopLayer interface {
	core.Component
	RenderTopLayer(cols int64) []string
}

// mergeTopLayer 把 top 层的非空白单元格合并进 rows（自 start 行起按行
// 对齐）。空白单元格不写入，透出下层内容；宽字符成对搬运，覆盖点两侧
// 的残缺宽字符会被清成空白，避免留下半字符。
func mergeTopLayer(rows []core.Row, start int64, lines []string) {
	if len(lines) == 0 {
		return
	}
	from := int(start)
	if from < 0 {
		from = 0
		if start < 0 {
			// 负偏移意味着 top 层前几行落在帧外，按对齐关系跳过。
			lines = lines[-int(start):]
		}
	}
	for i, ln := range lines {
		r := from + i
		if r >= len(rows) {
			return
		}
		dst := &rows[r]
		if dst.IsRaw() {
			continue // 原始字符串行无法按单元格合并，整行跳过
		}
		top := core.ParseLine(ln)
		if top.IsRaw() {
			continue
		}
		if len(top.Cells) == 0 {
			continue
		}
		prevWide := false // 上一个写入的单元格是宽字符左半
		for c := 0; c < len(top.Cells) && c < len(dst.Cells); c++ {
			cell := top.Cells[c]
			if cell.IsContinuation() {
				// 宽字符右半：仅当左半刚被写入时跟随写入，
				// 否则跳过（避免凭空出现半字符）。continuation 是
				// 宽字符的一部分，不能按空白跳过。
				if prevWide {
					dst.Cells[c] = cell
				}
				prevWide = false
				continue
			}
			if isBlankTopCell(cell) {
				prevWide = false
				continue
			}
			clearPartialWide(dst, c)
			dst.Cells[c] = cell
			prevWide = cell.Width == 2
		}
	}
}

// isBlankTopCell 报告一个单元格是否为「空白」：无字形、无 combining、
// 无样式。只有非空白单元格才会覆盖下层。
func isBlankTopCell(c core.Cell) bool {
	if len(c.Combining) > 0 {
		return false
	}
	if c.Rune != 0 && c.Rune != ' ' {
		return false
	}
	return c.Style.Equal(core.DefaultStyle)
}

// clearPartialWide 清掉目标位置上残缺的宽字符：若该位置是宽字符左半，
// 连右半一起清；若是右半，连左半一起清。
func clearPartialWide(row *core.Row, c int) {
	if row.Cells[c].Width == 2 && c+1 < len(row.Cells) && row.Cells[c+1].IsContinuation() {
		row.Cells[c+1] = core.Cell{}
	} else if row.Cells[c].IsContinuation() && c > 0 {
		row.Cells[c-1] = core.Cell{}
	}
}
