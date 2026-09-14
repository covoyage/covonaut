package chat

import (
	"math"
	"strings"
	"time"

	core "github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/theme"
)

// railShadowStyle 浮层投影色：与 tui/overlay.go 的暗化背景同一色阶
//（256 色 235）。只设背景不设前景——投影是空格串。
var railShadowStyle = theme.NewStyle().WithBgParams("48;5;235")

// ---------------------------------------------------------------------------
// Input Rail — 右缘输入记录刻度条。
//
// 右侧贴边竖向排列的 tick 序列，每个 tick 代表一次用户输入。分布策略：
// 少量输入聚拢在上下方向的中间，随 prompt 数量增多逐渐向两边扩散，
// 超出视口高度后等距压缩（同一行多个输入合并为一个 tick）。
//
// 绘制为悬浮语义：tick 只落在右缘空白单元格上，内容顶到右缘的行让位，
// 绝不裁切或覆盖下层内容。
//
// 交互：
//   - 悬停：当前 tick 及上下相邻 tick 变长（当前最长），并弹出浮层展示
//     该次输入（markdown 渲染）与助手回复预览。变长与浮层滑入均带缓动
//     动画，由 invalidate 回调驱动的帧定时器逐帧推进。
//   - 点击：JumpToAbsoluteLine 定位到该输入的起始行。
//
// 数据全部来自 ChatHistory 自身（messages / cachedMsgRanges），宿主只需
// SetRailEnabled(true)（或 ChatAppConfig.InputRail）开启。
//
// 鼠标事件在 ChatHistory.Update 的 MouseMsg 分支最先做 rail 命中测试，
// 命中即 consume，避免在 rail 列上误启动文本选区。
// ---------------------------------------------------------------------------

const (
	railHitCols   = 2  // 右缘命中区宽度（列）
	railDrawCols  = 6  // tick 绘制区最大宽度（列，右对齐）
	railPopupGap  = 1  // 浮层与 tick 之间的间隔列
	railHoverTol  = 2  // hover 命中时允许的行偏差
	railPopupMaxW = 48 // 浮层最大宽度（列）
	railPopupMaxH = 12 // 浮层最大高度（行，含边框）
	railUserMaxL  = 4  // 浮层中用户输入最多展示行数
	railAsstMaxL  = 4  // 浮层中助手回复最多展示行数
)

// tick 变长动画参数（var 便于测试注入更快的节奏）。
var (
	railAnimInterval = 33 * time.Millisecond // 帧间隔（约 30fps）
	railAnimTau      = 55.0                  // 缓动时间常数（毫秒），越小越快
)

// railTick 是视口中的一个可交互刻度。
type railTick struct {
	row       int   // 视口行号（0 基，与 Render 输出行对齐）
	msgIndex  int   // 对应用户消息索引
	startLine int64 // 虚拟布局绝对行号（跳转目标）
	runFrom   int   // 同一行合并的连续输入的首个消息索引
	runTo     int   // 最后一个消息索引
}

// railState 保存 rail 的运行时状态。所有字段仅在持有 h.mu 时读写。
type railState struct {
	enabled bool
	hover   int        // 悬停 tick 索引，-1 = 无
	press   int        // 已按下未释放的 tick 索引，-1 = 无
	ticks   []railTick // 最近一次 Render 计算的刻度（视口行坐标）
	animLens []float64 // 每个 tick 的当前动画长度（浮点格数）
	lastAnim time.Time // 上次动画步进时刻
	animScheduled bool // 已有待触发的动画帧定时器
	popupT   float64   // 浮层滑入进度 0..1（1 = 完全展开）
	popupFrom int      // 浮层内容来源 tick 索引（滑出动画期间仍需内容），-1 = 无

	// 浮层几何与内容快照（最近一次实际绘制的帧），供命中测试与选区提取。
	popupTop   int      // 浮层首行视口行号
	popupLeft  int64    // 浮层左列（完全展开位置）
	popupH     int      // 浮层行数
	popupBoxW  int64    // 浮层宽度（列）
	popupLines []string // 浮层内容快照（含样式）

	// 浮层局部文本选区：press-drag-release 语义与历史选区一致，
	// 但坐标是浮层局部（行 = popupLines 下标，列 = 浮层可见列）。
	popupDragging  bool
	popupSelActive bool
	popupSelStart  popupSelPos
	popupSelEnd    popupSelPos

	turnRunning bool // 宿主上报的 turn 运行态（ChatApp.Busy/Idle 同步）

	// 顶层图层（TopLayer）支持：Render 在叠加 rail 前存下的输出与宽度，
	// 供 RenderRailLayer 做 tick 让位判定（内容顶到右缘的行仍让位，
	// overlay 区域在这里是空白，tick/浮层会画上去盖住 overlay）。
	preLines []string
	preWidth int64
}

// popupSelPos 是浮层局部选区的一个端点。
type popupSelPos struct {
	row int
	col int
}

// SetRailEnabled 开启/关闭右缘输入记录刻度条。
func (h *ChatHistory) SetRailEnabled(on bool) {
	h.mu.Lock()
	if h.rail.enabled == on {
		h.mu.Unlock()
		return
	}
	h.rail.enabled = on
	h.rail.hover = -1
	h.rail.press = -1
	h.rail.animLens = nil
	h.rail.animScheduled = false
	h.rail.popupT = 0
	h.rail.popupFrom = -1
	h.mu.Unlock()
	h.invalidate()
}

// RailEnabled 返回 rail 是否开启。
func (h *ChatHistory) RailEnabled() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.rail.enabled
}

// SetTurnRunning 由宿主在 turn 开始/结束时上报（ChatApp.Busy/Idle）。
// 运行中的 turn 不产生横条——包括 assistant 消息尚未进入 history 的
// thinking 阶段，此时单靠 Pending 消息判定会漏。
func (h *ChatHistory) SetTurnRunning(on bool) {
	h.mu.Lock()
	if h.rail.turnRunning == on {
		h.mu.Unlock()
		return
	}
	h.rail.turnRunning = on
	h.mu.Unlock()
	h.invalidate()
}

// railSpreadRows 计算 n 个 tick 在视口中的行号：少量输入时聚拢在上下
// 方向的中间，随数量增多逐渐向两边扩散；超出视口高度后等距压缩并允许
// 多个 tick 合并到同一行。顺序保持 oldest 在上。
func railSpreadRows(n, viewRows int) []int {
	rows := make([]int, n)
	if n == 0 || viewRows <= 0 {
		return rows
	}
	if n == 1 {
		rows[0] = viewRows / 2
		return rows
	}
	span := n
	if span > viewRows {
		span = viewRows
	}
	top := (viewRows - span) / 2
	for i := 0; i < n; i++ {
		rows[i] = top + i*(span-1)/(n-1)
	}
	return rows
}

// railUserStarts 收集当前布局中所有用户输入：消息索引、起始行、顺序。
// 一个消息可能拆成多个 span（footer chip 等），取首个 span 的起始行。
//
// 最后一条用户输入只有在其 turn 结束（正常完成或中止）后才会出现——
// 即它之后不再有 Pending 的流式消息。正在运行的任务不产生横条。
// 调用方须持有 h.mu。
func (h *ChatHistory) railUserStarts() [][2]int {
	var starts [][2]int // [msgIndex, startLine]
	seen := make(map[int]bool)
	for _, r := range h.cachedMsgRanges {
		if r.msgIndex < 0 || r.msgIndex >= len(h.messages) || seen[r.msgIndex] {
			continue
		}
		if h.messages[r.msgIndex].Role != RoleUser {
			continue
		}
		seen[r.msgIndex] = true
		starts = append(starts, [2]int{r.msgIndex, r.startLine})
	}
	// 最后一条输入的 turn 还在跑：丢弃它，等结束（Idle/Finalize，完成
	// 与中止都会走到）后的下一次重绘再出现。两个信号任一命中即扣住：
	// turnRunning 覆盖 thinking 阶段（assistant 消息还没 Append），Pending
	// 覆盖流式输出阶段。
	if n := len(starts); n > 0 {
		hold := h.rail.turnRunning
		if !hold {
			last := starts[n-1][0]
			for i := last; i < len(h.messages); i++ {
				if h.messages[i].Pending {
					hold = true
					break
				}
			}
		}
		if hold {
			starts = starts[:n-1]
		}
	}
	return starts
}

// railComputeTicksLocked 基于当前视口高度重算 ticks（含同行合并）。
// 调用方须持有 h.mu。
func (h *ChatHistory) railComputeTicksLocked(viewRows int64) {
	h.rail.ticks = h.rail.ticks[:0]
	total := h.cachedTotal
	if !h.rail.enabled || total <= 0 || viewRows <= 0 {
		return
	}
	starts := h.railUserStarts()
	if len(starts) == 0 {
		return
	}
	rows := railSpreadRows(len(starts), int(viewRows))
	lastRow := -1
	cur := -1
	for i, s := range starts {
		row := rows[i]
		if row == lastRow && cur >= 0 {
			h.rail.ticks[cur].runTo = s[0]
			continue
		}
		h.rail.ticks = append(h.rail.ticks, railTick{
			row:       row,
			msgIndex:  s[0],
			startLine: int64(s[1]),
			runFrom:   s[0],
			runTo:     s[0],
		})
		cur = len(h.rail.ticks) - 1
		lastRow = row
	}
	// clamp hover/press 到有效范围
	if h.rail.hover >= len(h.rail.ticks) {
		h.rail.hover = -1
	}
	if h.rail.press >= len(h.rail.ticks) {
		h.rail.press = -1
	}
}

// railNearestTick 返回距离 row 最近的 tick 索引（偏差 ≤ railHoverTol），
// 没有则返回 -1。调用方须持有 h.mu。
func (h *ChatHistory) railNearestTick(row int64) int {
	best := -1
	bestDist := int64(railHoverTol + 1)
	for i, t := range h.rail.ticks {
		d := int64(t.row) - row
		if d < 0 {
			d = -d
		}
		if d <= railHoverTol && d < bestDist {
			best = i
			bestDist = d
		}
	}
	return best
}

// railPressLocked 处理 rail 列上的按下事件。返回 true 表示事件已被
// rail consume（不应再启动文本选区）。调用方须持有 h.mu。
// 命中坐标是屏幕全宽坐标系（rail 贴窗口右缘，不受内容边距影响）。
func (h *ChatHistory) railPressLocked(col, row int64) bool {
	if !h.rail.enabled || h.suppressGesture || h.fullW <= railHitCols {
		return false
	}
	if col < h.fullW-railHitCols {
		return false
	}
	h.rail.press = h.railNearestTick(row)
	return true
}

// railMotionLocked 处理鼠标移动。handled=true 表示事件已被 rail/浮层
// consume（不应再进入选区逻辑）；changed=true 表示状态变化、需要重绘。
// 调用方须持有 h.mu。
func (h *ChatHistory) railMotionLocked(col, row int64) (handled, changed bool) {
	if !h.rail.enabled || h.fullW <= railHitCols {
		return false, false
	}
	// 浮层选区拖拽中：无论指针是否仍在浮层内都继续更新选区（钳到浮层内），
	// 与历史选区的拖拽语义一致。
	if h.rail.popupDragging {
		h.rail.popupSelEnd = h.rail.popupClampSel(row, col)
		return true, true
	}
	// 指针在浮层上：保持浮层展开并 consume 事件——浮层不能因为鼠标
	// 移进去就消失，否则无法移入其中选择文本。
	if h.rail.popupAtLocked(row, col) {
		if h.rail.hover < 0 && h.rail.popupFrom >= 0 && h.rail.popupFrom < len(h.rail.ticks) {
			// 滑出途中被"追上"：恢复悬停，浮层重新展开。
			h.rail.hover = h.rail.popupFrom
			return true, true
		}
		return true, false
	}
	if col < h.fullW-railHitCols {
		// 离开 rail 列：清除悬停
		if h.rail.hover != -1 {
			h.rail.hover = -1
			return false, true
		}
		return false, false
	}
	next := h.railNearestTick(row)
	changed = next != h.rail.hover
	h.rail.hover = next
	return true, changed
}

// railReleaseLocked 处理释放事件。返回跳转目标行（-1 表示无跳转）。
// 无论命中与否都会清掉 pending press。调用方须持有 h.mu。
func (h *ChatHistory) railReleaseLocked() int64 {
	if !h.rail.enabled || h.rail.press < 0 {
		return -1
	}
	idx := h.rail.press
	h.rail.press = -1
	if idx >= len(h.rail.ticks) {
		return -1
	}
	return h.rail.ticks[idx].startLine
}

// popupAtLocked 报告 (row, col) 是否落在浮层矩形内。调用方须持有 h.mu。
func (r *railState) popupAtLocked(row, col int64) bool {
	if r.popupT <= 0 || r.popupH == 0 || r.popupBoxW <= 0 || len(r.popupLines) == 0 {
		return false
	}
	return row >= int64(r.popupTop) && row < int64(r.popupTop+r.popupH) &&
		col >= int64(r.popupLeft) && col < int64(r.popupLeft)+r.popupBoxW
}

// popupClampSel 把视口坐标换算成浮层局部选区坐标，并钳制到浮层范围内。
func (r *railState) popupClampSel(row, col int64) popupSelPos {
	p := popupSelPos{row: int(row) - r.popupTop, col: int(col - r.popupLeft)}
	if p.row < 0 {
		p.row = 0
	}
	if p.row >= r.popupH {
		p.row = r.popupH - 1
	}
	if p.col < 0 {
		p.col = 0
	}
	if int64(p.col) >= r.popupBoxW {
		p.col = int(r.popupBoxW) - 1
	}
	return p
}

// railPopupPressLocked 处理浮层内的按下：启动浮层局部选区拖拽。
// 返回 true 表示事件已被浮层 consume（不应再启动历史选区或 rail 跳转）。
// 调用方须持有 h.mu。
func (h *ChatHistory) railPopupPressLocked(col, row int64) bool {
	if !h.rail.enabled || h.suppressGesture {
		return false
	}
	if !h.rail.popupAtLocked(row, col) {
		return false
	}
	p := h.rail.popupClampSel(row, col)
	h.rail.popupDragging = true
	h.rail.popupSelActive = true
	h.rail.popupSelStart = p
	h.rail.popupSelEnd = p
	// 浮层内选择时清掉历史选区，避免两个选区同时处于激活态。
	h.selActive = false
	h.selDragging = false
	return true
}

// railPopupReleaseLocked 结束浮层选区拖拽。空选区（原地点击）直接清除。
// 返回 true 表示事件被浮层 consume。调用方须持有 h.mu。
func (h *ChatHistory) railPopupReleaseLocked() bool {
	if !h.rail.popupDragging {
		return false
	}
	h.rail.popupDragging = false
	if h.rail.popupSelStart == h.rail.popupSelEnd {
		h.rail.popupSelActive = false
	}
	return true
}

// railClearPopupSelLocked 清除浮层选区（点击浮层外时调用）。
func (h *ChatHistory) railClearPopupSelLocked() {
	h.rail.popupSelActive = false
	h.rail.popupDragging = false
}

// railPopupSelectedTextLocked 提取浮层选区文本（剥离 ANSI，行尾空格去除）。
func (h *ChatHistory) railPopupSelectedTextLocked() string {
	if !h.rail.popupSelActive || len(h.rail.popupLines) == 0 {
		return ""
	}
	top, bot := h.rail.popupSelStart, h.rail.popupSelEnd
	if top.row > bot.row || (top.row == bot.row && top.col > bot.col) {
		top, bot = bot, top
	}
	if top.row >= len(h.rail.popupLines) {
		return ""
	}
	var parts []string
	for i := top.row; i <= bot.row && i < len(h.rail.popupLines); i++ {
		line := h.rail.popupLines[i]
		var part string
		switch {
		case i == top.row && i == bot.row:
			part = core.SliceByColumn(line, int64(top.col), int64(bot.col))
		case i == top.row:
			part = core.SliceByColumn(line, int64(top.col), h.rail.popupBoxW)
		case i == bot.row:
			part = core.SliceByColumn(line, 0, int64(bot.col))
		default:
			part = line
		}
		part = strings.TrimRight(core.StripAnsi(part), " ")
		parts = append(parts, part)
	}
	return strings.Join(parts, "\n")
}

// railHighlightSelection 给浮层行中选区范围内的单元格套上选区背景色，
// 返回高亮后的行切片。与 applySelectionHighlightLocked 同一画法
//（ParseLine → 逐单元格替换样式 → SerializeRow）。
func railHighlightSelection(lines []string, start, end popupSelPos, selBg string) []string {
	top, bot := start, end
	if top.row > bot.row || (top.row == bot.row && top.col > bot.col) {
		top, bot = bot, top
	}
	if selBg == "" {
		selBg = "\x1b[48;5;33m"
	}
	selStyle := core.ParseLine(selBg + " " + "\x1b[0m")
	if selStyle.IsRaw() || len(selStyle.Cells) == 0 {
		return lines
	}
	selectedStyle := selStyle.Cells[0].Style
	if selectedStyle.Bg.IsDefault() {
		return lines
	}
	out := make([]string, len(lines))
	copy(out, lines)
	for i := range out {
		if i < top.row || i > bot.row {
			continue
		}
		row := core.ParseLine(out[i])
		if row.IsRaw() {
			continue
		}
		lineW := row.VisibleWidth()
		from, to := int64(0), lineW
		if i == top.row {
			from = int64(top.col)
		}
		if i == bot.row {
			to = int64(bot.col)
		}
		if from < 0 {
			from = 0
		}
		if to > lineW {
			to = lineW
		}
		if to < from {
			continue
		}
		for c := from; c < to && int(c) < len(row.Cells); c++ {
			row.Cells[c].Style = selectedStyle
		}
		out[i] = core.SerializeRow(row)
	}
	return out
}

// railTargetCells 返回某个 tick 状态的目标长度（格数）。
// kind: 0 普通 / 1 相邻 / 2 悬停。梯度必须一眼可辨：1 < 3 < 6（占满）。
func railTargetCells(kind int) int {
	switch kind {
	case 2:
		return railDrawCols
	case 1:
		return 3
	default:
		return 1
	}
}

// railEaseLen 指数缓动：把当前长度 cur 向 target 推进 dtMs 毫秒。
// dt 超过一帧时按一帧计，避免悬停离开很久后突然跳变。
func railEaseLen(cur, target, dtMs float64) float64 {
	if dtMs <= 0 {
		return cur
	}
	if dtMs > float64(railAnimInterval.Milliseconds()) {
		dtMs = float64(railAnimInterval.Milliseconds())
	}
	k := 1 - math.Exp(-dtMs/railAnimTau)
	return cur + (target-cur)*k
}

// railTickSeg 渲染单个 tick 字符串（右对齐到 railDrawCols 列）。
// cells 为动画后的当前长度，钳制在 [1, railDrawCols]。
func railTickSeg(style theme.Style, cells int) string {
	if cells < 1 {
		cells = 1
	}
	if cells > railDrawCols {
		cells = railDrawCols
	}
	return strings.Repeat(" ", railDrawCols-cells) + style.Render(strings.Repeat("─", cells))
}

// railAnimTick 是动画帧定时器回调：清掉调度标记并触发一次重绘，
// 下一帧在 applyRailOverlay 里继续推进。
func (h *ChatHistory) railAnimTick() {
	h.mu.Lock()
	h.rail.animScheduled = false
	h.mu.Unlock()
	h.invalidate()
}

// railTailBlank 报告一行最右侧 railDrawCols 列是否全为空白。
// 只有空白时整宽 tick 才落笔，保证悬浮、不入侵下层内容。
func railTailBlank(line string, width int64) bool {
	return railTailBlankRun(line, width, railDrawCols) == railDrawCols
}

// railTailBlankRun 返回一行从最右列向左的连续空白列数（最多数 maxCols
// 列）。带样式的空格不算空白；宽字符按两列计。内容行右缘的连续空白
// 决定了 tick 的可用宽度——判定按实际空白收缩，而不是要求整个 tick
// 绘制区全空：内容边距小于 railDrawCols 时，顶满内容区的行（如 turn
// 分割线）在最右 hMargin 列仍是空白，普通 tick 应照常落笔。
func railTailBlankRun(line string, width, maxCols int64) int64 {
	if maxCols <= 0 || width <= 0 {
		return 0
	}
	if maxCols > width {
		maxCols = width
	}
	if line == "" {
		return maxCols // 空画布行：整个判定窗都可用
	}
	// 从整行解析：SliceByColumn 在宽字符边界会补空格，切出来的尾段
	// 丢失「起点是宽字符 continuation」的信息，会把被切开的宽字符
	// 误判成空白。
	row := core.ParseLine(line)
	if row.IsRaw() || len(row.Cells) == 0 {
		return 0
	}
	lastNonBlank := int64(-1) // 全局列坐标（Cells 下标即列号）
	col := int64(0)
	prevBlank := false // 上一个非 continuation 单元格是否空白
	for _, c := range row.Cells {
		if c.IsContinuation() {
			if !prevBlank {
				lastNonBlank = col // 宽字符右半随左半算非空白
			}
			col++
			continue
		}
		if isRailBlankCell(c) {
			prevBlank = true
		} else {
			lastNonBlank = col + int64(c.Width) - 1 // 宽字符左半覆盖两列
			prevBlank = false
		}
		col++
	}
	run := width - (lastNonBlank + 1)
	if run > maxCols {
		run = maxCols
	}
	return run
}

// isRailBlankCell 报告一个单元格是否为无字形、无 combining、无样式的
// 空白（与 tui 包顶层合并的空白判定同一标准）。
func isRailBlankCell(c core.Cell) bool {
	if len(c.Combining) > 0 {
		return false
	}
	if c.Rune != 0 && c.Rune != ' ' {
		return false
	}
	return c.Style.Equal(core.DefaultStyle)
}

// applyRailOverlay 在 Render 输出上叠加 rail 刻度与悬停浮层。
// lines 是已按 width 裁齐的视口行；返回叠加后的行切片。
func (h *ChatHistory) applyRailOverlay(lines []string, width int64) []string {
	return h.applyRailOverlayWith(lines, lines, width, true)
}

// RenderRailLayer 在 n 行空白画布上绘制 rail（供 TUI 的 TopLayer 机制在
// overlay 之上重绘）。content 是让位判断的来源——history 本帧未叠加 rail
// 的输出，右缘被内容占据的行 tick 仍让位；overlay 占据的位置在 content
// 里是空白，tick/浮层会画上去并覆盖 overlay。不推进动画（动画由本帧
// Render 内的 applyRailOverlay 负责，这里复用当前帧的长度与进度）。
func (h *ChatHistory) RenderRailLayer(n int, width int64) []string {
	if n <= 0 || width < 20 {
		return nil
	}
	h.mu.Lock()
	enabled := h.rail.enabled
	h.mu.Unlock()
	if !enabled {
		return nil
	}
	canvas := make([]string, n)
	h.applyRailOverlayWith(canvas, h.railContentLines(n), width, false)
	return canvas
}

// railContentLines 返回让位判断用的内容行切片（本帧 Render 存下的未叠加
// rail 的输出；长度不足画布时补空串——空白即让位判定通过）。
func (h *ChatHistory) railContentLines(n int) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.rail.preLines) == 0 {
		return nil
	}
	content := make([]string, n)
	copy(content, h.rail.preLines)
	return content
}

// applyRailOverlayWith 在 paint 上叠加 rail 刻度与浮层；content 提供让位
// 判定（tick 只落在右缘空白的行上）。advance 为 true 时推进动画帧并调度
// 下一次重绘，false 时只按当前动画状态绘制（供顶层画布同帧复用）。
func (h *ChatHistory) applyRailOverlayWith(paint, content []string, width int64, advance bool) []string {
	lines := paint
	if width < 20 || len(lines) == 0 {
		return lines
	}
	h.mu.Lock()
	if !h.rail.enabled {
		h.mu.Unlock()
		return lines
	}
	h.railComputeTicksLocked(int64(len(lines)))
	if len(h.rail.ticks) == 0 {
		h.rail.animLens = h.rail.animLens[:0]
		h.mu.Unlock()
		return lines
	}
	ticks := make([]railTick, len(h.rail.ticks))
	copy(ticks, h.rail.ticks)
	hover := h.rail.hover
	dim := h.theme.DimStyle
	// 悬停 tick 加粗提亮，与相邻/普通刻度在长度之外再加一层区分。
	bright := h.theme.UserStyle.Bold()

	// 动画步进：把每个 tick 的当前长度向目标长度推进一帧，
	// 未收敛则调度下一帧重绘。顶层画布复用（advance=false）时只读取
	// 当前动画状态，不步进、不调度——本帧 Render 已负责推进。
	kindOf := func(idx int) int {
		if hover >= 0 {
			switch idx {
			case hover:
				return 2
			case hover - 1, hover + 1:
				return 1
			}
		}
		return 0
	}
	var cells []int
	var popupFrom int
	var popupT float64
	if advance {
		now := time.Now()
		dt := railAnimInterval.Milliseconds()
		if !h.rail.lastAnim.IsZero() {
			if d := now.Sub(h.rail.lastAnim).Milliseconds(); d > 0 && d < dt {
				dt = d
			}
		}
		if len(h.rail.animLens) != len(ticks) {
			lens := make([]float64, len(ticks))
			copy(lens, h.rail.animLens)
			for i := len(h.rail.animLens); i < len(ticks); i++ {
				lens[i] = float64(railTargetCells(kindOf(i))) // 新 tick 从静止长度起步
			}
			h.rail.animLens = lens
		}
		cells = make([]int, len(ticks))
		needMore := false
		for i := range ticks {
			cur := railEaseLen(h.rail.animLens[i], float64(railTargetCells(kindOf(i))), float64(dt))
			h.rail.animLens[i] = cur
			c := int(math.Round(cur))
			if math.Abs(float64(railTargetCells(kindOf(i)))-cur) > 0.05 {
				needMore = true
			}
			cells[i] = c
		}
		// 浮层滑入/滑出进度：hover 出现→1，清除→0；hover 在 tick 间移动时
		// 保持 1，只换内容不重播动画。
		popupFrom = h.rail.popupFrom
		if hover >= 0 {
			h.rail.popupFrom = hover
			popupFrom = hover
		}
		popupTarget := 0.0
		if hover >= 0 {
			popupTarget = 1
		}
		h.rail.popupT = railEaseLen(h.rail.popupT, popupTarget, float64(dt))
		popupT = h.rail.popupT
		if math.Abs(popupTarget-popupT) > 0.02 {
			needMore = true
		}
		h.rail.lastAnim = now
		startAnim := needMore && !h.rail.animScheduled && h.onInvalidate != nil
		if startAnim {
			h.rail.animScheduled = true
		}
		h.mu.Unlock()
		if startAnim {
			time.AfterFunc(railAnimInterval, h.railAnimTick)
		}
	} else {
		cells = make([]int, len(ticks))
		for i := range ticks {
			lens := h.rail.animLens
			if len(lens) != len(ticks) {
				cells[i] = railTargetCells(kindOf(i))
				continue
			}
			cells[i] = int(math.Round(lens[i]))
		}
		popupFrom = h.rail.popupFrom
		popupT = h.rail.popupT
		h.mu.Unlock()
	}

	// tick 样式选择：悬停最长最亮，相邻次之，其余普通。
	tickKindStyle := func(idx int) (int, theme.Style) {
		kind := kindOf(idx)
		if kind == 2 {
			return kind, bright
		}
		return kind, dim
	}

	// 1. tick 列：悬浮绘制——tick 可用宽度由该行右缘连续空白决定，
	// 按实际空白收缩（普通 1 格 tick 只需最右 1 列空白；悬停变长时
	// 最多伸到内容前），绝不裁切、不覆盖任何字符。内容完全顶到右缘
	// 的行（连续空白为 0）让位不画。
	tickAt := make(map[int]int, len(ticks))  // row -> tick index
	drawn := make(map[int]bool, len(ticks)) // row -> tick 已实际绘制
	drawnLen := make(map[int]int, len(ticks))
	for i, t := range ticks {
		tickAt[t.row] = i
	}
	for r := range lines {
		idx, ok := tickAt[r]
		if !ok {
			continue
		}
		n := cells[idx]
		if r < len(content) {
			avail := railTailBlankRun(content[r], width, railDrawCols)
			if avail <= 0 {
				continue // 该行内容顶到右缘：不画，保持悬浮
			}
			if int64(n) > avail {
				n = int(avail) // 可用空白不足时收缩，不入侵内容
			}
		}
		_, style := tickKindStyle(idx)
		// 画布行可能短于内容区宽（顶层图层是空串），先补齐再拼，
		// 保证 tick 始终右对齐到窗口右缘。截断点取 tick 实际起点，
		// 内容比整宽判定窗更靠右时也不会被裁掉。
		base := core.PadToWidth(core.TruncateToWidth(lines[r], width-int64(n), ""), width-int64(n))
		lines[r] = core.PadToWidth(base+style.Render(strings.Repeat("─", n)), width)
		drawn[r] = true
		drawnLen[r] = n
	}

	// 2. 悬停浮层：贴在 tick 左侧，滑入动画——右缘固定在 tick 列，
	// 左边界随进度向左展开；滑出时反向收回，内容沿用最后一次悬停的 tick。
	if popupT < 0.02 || popupFrom < 0 || popupFrom >= len(ticks) {
		// 浮层完全收起：清掉几何快照与选区（选区随浮层一起消失）。
		h.mu.Lock()
		h.rail.popupH = 0
		h.rail.popupLines = nil
		h.rail.popupSelActive = false
		h.rail.popupDragging = false
		h.mu.Unlock()
		return lines
	}
	popupLines, boxW := h.railPopupLines(ticks[popupFrom], width)
	if len(popupLines) == 0 {
		return lines
	}
	top := ticks[popupFrom].row - len(popupLines)/2
	if top < 0 {
		top = 0
	}
	if maxTop := len(lines) - len(popupLines); top > maxTop {
		top = maxTop
	}
	if top < 0 {
		// 视口比浮层还矮：整体放不下，放弃绘制。
		return lines
	}
	left := width - railDrawCols - railPopupGap - boxW
	if left < 0 {
		return lines
	}
	vis := int64(math.Round(float64(boxW) * popupT))
	if vis > boxW {
		vis = boxW
	}
	if vis < 2 {
		return lines
	}
	shift := boxW - vis // 左侧被遮挡的列数

	// 浮层选区高亮 + 几何/内容快照（供命中测试与复制提取）。
	h.mu.Lock()
	if h.rail.popupSelActive {
		popupLines = railHighlightSelection(popupLines, h.rail.popupSelStart, h.rail.popupSelEnd, h.theme.SelectedBg)
	}
	h.rail.popupTop, h.rail.popupLeft = top, left
	h.rail.popupH = len(popupLines)
	h.rail.popupBoxW = boxW
	h.rail.popupLines = append(h.rail.popupLines[:0], popupLines...)
	h.mu.Unlock()

	gap := strings.Repeat(" ", railPopupGap)
	for i, seg := range popupLines {
		r := top + i
		base := core.PadToWidth(core.TruncateToWidth(lines[r], left+shift, ""), left+shift)
		if shift > 0 {
			seg = core.SliceByColumn(seg, shift, boxW)
		}
		row := base + seg
		// 浮层覆盖范围内的 tick（含被悬停的那个）必须重新接回行尾，
		// 否则会被截掉——表现为"悬停的横条消失"。未绘制的 tick
		// （内容顶到右缘）补回空白，保持原状；已绘制的按底图同一
		// 收缩长度回接，右对齐到窗口右缘。
		if _, ok := tickAt[r]; ok {
			row += gap
			if drawn[r] {
				_, style := tickKindStyle(tickAt[r])
				n := drawnLen[r]
				row += strings.Repeat(" ", railDrawCols-n) + style.Render(strings.Repeat("─", n))
			}
		}
		lines[r] = core.PadToWidth(row, width)
	}

	// 3. 投影：浮层右缘外 1 列 + 底部 1 行暗背景，营造“浮在内容之上”
	// 的层次感。右缘投影落在浮层与 tick 之间的 gap 列（本来就空着），
	// 底部投影避开左下圆角两格——影子替换的是等宽空格/被覆盖区域，
	// 不改变任何行的可见宽度。
	shadowCol := left + boxW
	for i := range popupLines {
		r := top + i
		prefix := core.PadToWidth(core.TruncateToWidth(lines[r], shadowCol, ""), shadowCol)
		tail := core.SliceByColumn(lines[r], shadowCol+1, width)
		lines[r] = core.PadToWidth(prefix+railShadowStyle.Render(" ")+tail, width)
	}
	if shadowRow := top + len(popupLines); shadowRow < len(lines) {
		from := left + 2
		prefix := core.PadToWidth(core.TruncateToWidth(lines[shadowRow], from, ""), from)
		mid := railShadowStyle.Render(strings.Repeat(" ", int(boxW-2)))
		tail := core.SliceByColumn(lines[shadowRow], left+boxW, width)
		lines[shadowRow] = core.PadToWidth(prefix+mid+tail, width)
	}
	return lines
}

// railPopupLines 生成浮层各行（已含边框，宽度统一为 boxW）。
// boxW 由内容与可用宽度共同决定；无内容时返回 nil。
func (h *ChatHistory) railPopupLines(t railTick, width int64) ([]string, int64) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if t.msgIndex < 0 || t.msgIndex >= len(h.messages) {
		return nil, 0
	}
	user := h.messages[t.msgIndex]

	// 找该输入之后第一条非空助手回复（遇到下一条用户输入即停）。
	var asst *ChatMessage
	for i := t.msgIndex + 1; i < len(h.messages); i++ {
		m := &h.messages[i]
		if m.Role == RoleUser {
			break
		}
		if m.Role == RoleAssistant && strings.TrimSpace(m.Text) != "" {
			asst = m
			break
		}
	}

	boxW := railPopupMaxW
	if avail := int(width) - railDrawCols - railPopupGap - 2; boxW > avail {
		boxW = avail
	}
	inner := boxW - 4 // 左右边框 + 各 1 列内边距
	if inner < 10 {
		return nil, 0
	}

	// markdown 渲染走独立 MarkdownState：不污染消息主渲染的检查点
	// memo（浮层宽度与正文宽度不同，混用会互相失效）。
	mdAt := func(m ChatMessage, w int64) []string {
		m.mdCheckpoint = nil
		return m.mdLines(m.Text, w, h.theme.MarkdownTheme, currentPaletteRevision())
	}
	// 去掉 markdown 渲染产生的首尾空行。
	trimBlank := func(ls []string) []string {
		for len(ls) > 0 && strings.TrimSpace(core.StripAnsi(ls[0])) == "" {
			ls = ls[1:]
		}
		for len(ls) > 0 && strings.TrimSpace(core.StripAnsi(ls[len(ls)-1])) == "" {
			ls = ls[:len(ls)-1]
		}
		return ls
	}
	capLines := func(ls []string, max int) []string {
		if len(ls) > max {
			return append(ls[:max-1:max-1], h.theme.DimStyle.Render("…"))
		}
		return ls
	}

	var body []string
	userLines := capLines(trimBlank(mdAt(user, int64(inner))), railUserMaxL)
	body = append(body, userLines...)

	if asst != nil {
		asstCopy := *asst
		asstLines := capLines(trimBlank(mdAt(asstCopy, int64(inner))), railAsstMaxL)
		body = append(body, "")
		body = append(body, asstLines...)
	} else {
		body = append(body, "", h.theme.DimStyle.Render("暂无助手正文"))
	}
	if t.runTo > t.runFrom {
		body = append(body, h.theme.DimStyle.Render(
			"… 此处合并了多次输入"))
	}
	if len(body) > railPopupMaxH-2 {
		body = body[:railPopupMaxH-2]
	}
	if len(body) == 0 {
		return nil, 0
	}

	border := h.theme.DimStyle
	out := make([]string, 0, len(body)+2)
	out = append(out, border.Render("╭"+strings.Repeat("─", boxW-2)+"╮"))
	for _, b := range body {
		vw := core.VisibleWidth(b)
		if vw > int64(inner) {
			b = core.TruncateToWidth(b, int64(inner), "")
			vw = int64(inner)
		}
		out = append(out, border.Render("│ ")+b+strings.Repeat(" ", int(int64(inner)-vw))+border.Render(" │"))
	}
	out = append(out, border.Render("╰"+strings.Repeat("─", boxW-2)+"╯"))
	return out, int64(boxW)
}
