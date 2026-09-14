package chat

import (
	"strings"
	"testing"

	core "github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/theme"
)

// railTestHistory 构造一个开启 rail 的 ChatHistory：
// 1 条用户输入 + 1 条多行助手回复 + 1 条用户输入 + 短回复。
// 助手正文用硬换行段落（\n\n），避免 markdown 把文本重排成整宽行
// 触发"内容占据右缘、tick 让位"的悬浮规则，干扰断言。
func railTestHistory(t *testing.T, width, maxRows int64) *ChatHistory {
	t.Helper()
	h := NewChatHistory()
	h.SetRailEnabled(true)
	h.SetMaxRows(maxRows)
	asst := make([]string, totalTestLines)
	for i := range asst {
		asst[i] = "assistant line"
	}
	h.Append(ChatMessage{Role: RoleUser, Text: "first input"})
	h.Append(ChatMessage{Role: RoleAssistant, Text: strings.Join(asst, "\n\n")})
	h.Append(ChatMessage{Role: RoleUser, Text: "second input"})
	h.Append(ChatMessage{Role: RoleAssistant, Text: "short reply"})
	h.Render(width)
	return h
}

const (
	railTestWidth   = int64(100)
	railTestMaxRows = int64(12)
	totalTestLines  = 30
)

// TestRailHorizontalMargin 验证内容边距：内容行左右内缩（左边距前缀、
// 内容按 width-2*m 换行），而 rail tick 仍贴窗口右缘、命中测试用全宽。
func TestRailHorizontalMargin(t *testing.T) {
	h := railTestHistory(t, railTestWidth, railTestMaxRows)
	h.SetHorizontalMargin(2)
	lines := h.Render(railTestWidth)

	h.mu.Lock()
	tickN := len(h.rail.ticks)
	firstRow := -1
	if tickN > 0 {
		firstRow = h.rail.ticks[0].row
	}
	h.mu.Unlock()
	if tickN == 0 {
		t.Fatalf("no ticks with margin set")
	}

	// 内容行：非空行剥 ANSI 后应以 2 格左边距开头。
	contentIndented := false
	for _, ln := range lines {
		plain := stripANSI(ln)
		if strings.TrimSpace(plain) == "" {
			continue
		}
		if strings.HasPrefix(plain, "  ") {
			contentIndented = true
		}
		break
	}
	if !contentIndented {
		t.Fatalf("content not indented by margin after SetHorizontalMargin(2)")
	}

	// rail tick：仍落在窗口最右缘（行尾 railDrawCols 列内）。
	tickAtEdge := false
	for r, ln := range lines {
		if r != firstRow {
			continue
		}
		plain := stripANSI(ln)
		tail := plain
		if len(tail) > int(railDrawCols) {
			tail = tail[len(tail)-int(railDrawCols):]
		}
		tickAtEdge = strings.Contains(tail, "─")
	}
	if !tickAtEdge {
		t.Fatalf("tick not at window right edge with margin set")
	}

	// 命中测试：hover 用屏幕全宽坐标（railCol = 全宽 - railHitCols）。
	h.Update(core.MouseMsg{Action: core.MouseMotion, Row: int64(firstRow), Col: railCol()})
	h.mu.Lock()
	hover := h.rail.hover
	h.mu.Unlock()
	if hover != 0 {
		t.Fatalf("rail hover with screen col not captured (hover=%d)", hover)
	}
}

func railCol() int64 { return railTestWidth - railHitCols }

// TestRailSpreadRows 验证居中聚拢分布：少量输入聚在中间，增多后向两边
// 扩散，超出视口高度后压缩到全高。
func TestRailSpreadRows(t *testing.T) {
	// 单个输入 → 正中间。
	rows := railSpreadRows(1, 21)
	if rows[0] != 10 {
		t.Fatalf("single tick row = %d, want 10 (center)", rows[0])
	}
	// 三个输入 → 连续居中。
	rows = railSpreadRows(3, 21)
	want := []int{9, 10, 11}
	for i, r := range want {
		if rows[i] != r {
			t.Fatalf("rows = %v, want %v", rows, want)
		}
	}
	// 增多 → span 变大且仍居中（首尾到边距离相等）。
	rows = railSpreadRows(11, 21)
	if rows[0] != 5 || rows[len(rows)-1] != 15 {
		t.Fatalf("spread rows head=%d tail=%d, want 5..15", rows[0], rows[len(rows)-1])
	}
	// 超出视口 → 压缩到全高，且单调不减。
	rows = railSpreadRows(40, 21)
	if rows[0] != 0 || rows[len(rows)-1] != 20 {
		t.Fatalf("compressed rows head=%d tail=%d, want 0..20", rows[0], rows[len(rows)-1])
	}
	for i := 1; i < len(rows); i++ {
		if rows[i] < rows[i-1] {
			t.Fatalf("rows not monotonic: %v", rows)
		}
	}
}

// stripANSI 去掉 SGR 序列，便于按可见字符断言。
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] == ';' || (s[j] >= '0' && s[j] <= '9')) {
				j++
			}
			if j < len(s) && s[j] == 'm' {
				i = j + 1
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// TestRailTicksRendered 验证 Render 后右缘出现 tick，且悬停时当前 tick
// 最长（占满 railDrawCols），相邻 tick 明显更短。
func TestRailTicksRendered(t *testing.T) {
	h := railTestHistory(t, railTestWidth, railTestMaxRows)
	lines := h.applyRailOverlay(h.Render(railTestWidth), railTestWidth)

	ticks := 0
	for _, ln := range lines {
		if idx := strings.Index(ln, "─"); idx >= 0 && idx >= int(railTestWidth)-railDrawCols-2 {
			ticks++
		}
	}
	if ticks == 0 {
		t.Fatalf("no rail ticks rendered in %d lines", len(lines))
	}

	// 悬停：motion 到第一个 tick 行。tick 段位于行尾 railDrawCols 列，
	// 剥掉 ANSI 后按行尾后缀断言（行内容可能自带 markdown 分隔线，
	// 不能全行数 "─"）。相邻行是否画 tick 取决于该行内容是否顶到右缘
	// （悬浮让位规则），因此长度梯度在 railTickSeg 单元级断言。
	h2 := railTestHistory(t, railTestWidth, railTestMaxRows)
	h2.mu.Lock()
	firstRow := h2.rail.ticks[0].row
	h2.mu.Unlock()
	h2.Update(core.MouseMsg{Action: core.MouseMotion, Row: int64(firstRow), Col: railCol()})

	// 动画：第一帧悬停 tick 还未到全长，逐帧推进后收敛到 6 格。
	lines2 := h2.applyRailOverlay(h2.Render(railTestWidth), railTestWidth)
	tailOf := func(ls []string, row int) string {
		return strings.TrimRight(stripANSI(ls[row]), " ")
	}
	if strings.HasSuffix(tailOf(lines2, firstRow), strings.Repeat("─", railDrawCols)) {
		t.Fatalf("hovered tick should start short and animate to full length")
	}
	lines2 = railDriveFrames(h2, 15)
	hoveredOK := strings.HasSuffix(tailOf(lines2, firstRow), strings.Repeat("─", railDrawCols))
	if !hoveredOK {
		t.Fatalf("hovered tick not converged to full %d-cell length after animation", railDrawCols)
	}

	// 离开 rail 列后悬停清除。
	h2.Update(core.MouseMsg{Action: core.MouseMotion, Row: 3, Col: 10})
	if h2.rail.hover != -1 {
		t.Fatalf("hover should clear after leaving rail column, got %d", h2.rail.hover)
	}
}

// TestRailClickJumps 验证点击 tick 跳转到对应输入位置。
func TestRailClickJumps(t *testing.T) {
	h := railTestHistory(t, railTestWidth, railTestMaxRows)
	if h.Offset() != 0 {
		t.Fatalf("test expects tail view, offset = %d", h.Offset())
	}
	h.mu.Lock()
	if len(h.rail.ticks) == 0 {
		h.mu.Unlock()
		t.Fatalf("no ticks computed")
	}
	target := h.rail.ticks[0] // 第一条输入
	h.mu.Unlock()

	h.Update(core.MouseMsg{Action: core.MousePress, Row: int64(target.row), Col: railCol()})
	if h.rail.press < 0 {
		t.Fatalf("press in rail column not captured (press=%d)", h.rail.press)
	}
	h.Update(core.MouseMsg{Action: core.MouseRelease, Row: int64(target.row), Col: railCol()})
	if h.Offset() == 0 {
		t.Fatalf("click did not jump; still at tail")
	}
	// 跳转后目标行应落在视口内（JumpToAbsoluteLine 把行号放到视口上部）。
	h.mu.Lock()
	total := h.cachedTotal
	h.mu.Unlock()
	viewStart := total - h.Offset() - railTestMaxRows
	if viewStart > target.startLine || target.startLine >= viewStart+railTestMaxRows {
		t.Fatalf("jumped to wrong window: viewStart=%d want contains %d", viewStart, target.startLine)
	}
}

// TestRailPressDoesNotSelect 验证 rail 列上的点击不会启动文本选区。
func TestRailPressDoesNotSelect(t *testing.T) {
	h := railTestHistory(t, railTestWidth, railTestMaxRows)
	h.Update(core.MouseMsg{Action: core.MousePress, Row: 1, Col: railCol()})
	h.Update(core.MouseMsg{Action: core.MouseRelease, Row: 1, Col: railCol()})
	if h.selActive {
		t.Fatalf("rail press started a text selection")
	}
	// 对照组：非 rail 列的点击仍然启动选区。
	h.Update(core.MouseMsg{Action: core.MousePress, Row: 1, Col: 10})
	if !h.selActive {
		t.Fatalf("press outside rail should start selection")
	}
}

// TestRailTickSegGradient 验证目标长度梯度：普通 1 格 < 相邻 3 格 <
// 悬停 6 格，以及 railTickSeg 对任意 cells 的渲染宽度。
func TestRailTickSegGradient(t *testing.T) {
	if railTargetCells(0) != 1 || railTargetCells(1) != 3 || railTargetCells(2) != railDrawCols {
		t.Fatalf("target cells = %d/%d/%d, want 1/3/%d",
			railTargetCells(0), railTargetCells(1), railTargetCells(2), railDrawCols)
	}
	dim := DefaultChatHistoryTheme().DimStyle
	dashes := func(cells int) int {
		return strings.Count(stripANSI(railTickSeg(dim, cells)), "─")
	}
	for cells := 1; cells <= railDrawCols; cells++ {
		if got := dashes(cells); got != cells {
			t.Fatalf("railTickSeg(cells=%d) rendered %d dashes", cells, got)
		}
	}
	// 越界钳制。
	if got := dashes(0); got != 1 {
		t.Fatalf("railTickSeg(cells=0) = %d dashes, want clamped to 1", got)
	}
	if got := dashes(railDrawCols + 3); got != railDrawCols {
		t.Fatalf("railTickSeg overflow not clamped: %d", got)
	}
}

// TestRailEaseLen 验证缓动函数：单调逼近目标、不超调，dt 超过一帧按一帧计。
func TestRailEaseLen(t *testing.T) {
	frame := float64(railAnimInterval.Milliseconds())
	one := railEaseLen(1, 6, frame)
	if one <= 1 || one >= 6 {
		t.Fatalf("one frame from 1 toward 6 = %v, want strictly between", one)
	}
	two := railEaseLen(one, 6, frame)
	if two <= one || two >= 6 {
		t.Fatalf("second frame not monotonic: %v -> %v", one, two)
	}
	if got := railEaseLen(1, 6, 10_000); got != one {
		t.Fatalf("dt clamping broken: huge dt gave %v, want %v", got, one)
	}
	if got := railEaseLen(3, 6, 0); got != 3 {
		t.Fatalf("dt=0 should keep cur, got %v", got)
	}
}

// railDriveFrames 连续推进 n 帧动画（applyRailOverlay 每调用一次推进
// railAnimInterval 毫秒的缓动），返回最后一帧的渲染行。
func railDriveFrames(h *ChatHistory, n int) []string {
	var lines []string
	for i := 0; i < n; i++ {
		lines = h.applyRailOverlay(h.Render(railTestWidth), railTestWidth)
	}
	return lines
}

// TestRailFloatsOverBlankOnly 验证悬浮语义：tick 只落在右缘空白处，
// 内容顶到右缘的行不画 tick，内容也不被裁切。
func TestRailFloatsOverBlankOnly(t *testing.T) {
	if !railTailBlank(strings.Repeat(" ", int(railTestWidth)), railTestWidth) {
		t.Fatalf("blank row should be drawable")
	}
	if railTailBlank(strings.Repeat("x", int(railTestWidth)), railTestWidth) {
		t.Fatalf("full-width content row should not be drawable")
	}
	if !railTailBlank("hello", 20) {
		t.Fatalf("short row padded with spaces should be drawable")
	}

	// 集成：内容占满整宽的行，tick 不落笔且内容保持原样。
	h := railTestHistory(t, railTestWidth, railTestMaxRows)
	h.mu.Lock()
	firstRow := h.rail.ticks[0].row
	h.mu.Unlock()
	lines := h.Render(railTestWidth)
	// 把 tick 行的内容替换为整宽文本（模拟内容顶到右缘）。
	lines[firstRow] = strings.Repeat("内容占满", int(railTestWidth)/4)
	out := h.applyRailOverlay(lines, railTestWidth)
	plain := stripANSI(out[firstRow])
	if !strings.HasSuffix(plain, strings.Repeat("内容占满", int(railTestWidth)/4)) {
		t.Fatalf("full-width content was truncated by rail: %q", plain[:60])
	}
	if strings.Count(plain, "─") != 0 {
		t.Fatalf("tick must not be drawn over full-width content")
	}
}

// TestRailHoverPopup 验证悬停时浮层被绘制（含用户输入预览），且被浮层
// 覆盖的悬停 tick 仍然渲染（回归：浮层截行导致悬停横条消失）。
func TestRailHoverPopup(t *testing.T) {
	h := railTestHistory(t, railTestWidth, railTestMaxRows)
	h.Update(core.MouseMsg{Action: core.MouseMotion, Row: int64(h.rail.ticks[0].row), Col: railCol()})
	lines := railDriveFrames(h, 15)

	found := false
	hoverTickKept := false
	for _, ln := range lines {
		if strings.Contains(ln, "first input") && strings.Contains(ln, "│") {
			found = true
		}
		if strings.Count(ln, "────") >= 1 {
			hoverTickKept = true
		}
	}
	if !found {
		t.Fatalf("hover popup with user preview not rendered")
	}
	if !hoverTickKept {
		t.Fatalf("hovered tick disappeared while popup shown")
	}
}

// TestRailPopupSlideIn 验证浮层滑入动画：第一帧只露出右侧一小段，
// 逐帧推进后完全展开（左边框出现在最终位置）。
func TestRailPopupSlideIn(t *testing.T) {
	h := railTestHistory(t, railTestWidth, railTestMaxRows)
	h.mu.Lock()
	row := h.rail.ticks[0].row
	h.mu.Unlock()
	h.Update(core.MouseMsg{Action: core.MouseMotion, Row: int64(row), Col: railCol()})

	fullBorder := "╭" + strings.Repeat("─", int(railPopupMaxW)-2) + "╮"
	hasFullBorder := func(ls []string) bool {
		for _, ln := range ls {
			if strings.Contains(ln, fullBorder) {
				return true
			}
		}
		return false
	}
	// 第一帧：滑入刚开始，浮层不应已完全展开。
	first := h.applyRailOverlay(h.Render(railTestWidth), railTestWidth)
	if hasFullBorder(first) {
		t.Fatalf("popup should start partially hidden, but fully drawn on first frame")
	}
	// 推进到收敛：浮层完全展开。
	last := railDriveFrames(h, 20)
	if !hasFullBorder(last) {
		t.Fatalf("popup not fully expanded after animation frames")
	}
	// 滑出：离开 rail 列后浮层收回到不可见。
	h.Update(core.MouseMsg{Action: core.MouseMotion, Row: 3, Col: 10})
	out := railDriveFrames(h, 20)
	if hasFullBorder(out) {
		t.Fatalf("popup should slide out after leaving rail column")
	}
	for _, ln := range out {
		if strings.Contains(stripANSI(ln), "first input") {
			t.Fatalf("popup content still visible after slide-out")
		}
	}
}

// TestRailPopupShadow 验证浮层投影：完全展开后，浮层每一行右缘外 1 列
// 与底部 1 行应出现投影背景色（256 色 235）。需强制开色（无 TTY 环境
// 下 Style.Render 退化为纯文本）。
func TestRailPopupShadow(t *testing.T) {
	theme.ForceColor(true)
	t.Cleanup(func() { theme.ForceColor(false) })

	h := railTestHistory(t, railTestWidth, railTestMaxRows)
	h.mu.Lock()
	row := h.rail.ticks[0].row
	h.mu.Unlock()
	h.Update(core.MouseMsg{Action: core.MouseMotion, Row: int64(row), Col: railCol()})
	lines := railDriveFrames(h, 20)

	h.mu.Lock()
	popupH := h.rail.popupH
	h.mu.Unlock()
	if popupH == 0 {
		t.Fatalf("popup not expanded; cannot assert shadow")
	}

	shadowRows := 0
	for _, ln := range lines {
		if strings.Contains(ln, "\x1b[48;5;235m") {
			shadowRows++
		}
	}
	// 右缘投影 popupH 行 + 底部投影 1 行。
	if shadowRows != popupH+1 {
		t.Fatalf("shadow rows = %d, want %d (popup %d rows + bottom 1)", shadowRows, popupH+1, popupH)
	}
}

// TestRailTickHeldWhileTurnRunning 验证：turn 进行中（有 Pending 的流式
// 消息）最后一条输入不产生横条；任务结束或中止（Finalize 清 Pending）后
// 下一次渲染才出现。
func TestRailTickHeldWhileTurnRunning(t *testing.T) {
	h := NewChatHistory()
	h.SetRailEnabled(true)
	h.SetMaxRows(railTestMaxRows)

	h.Append(ChatMessage{Role: RoleUser, Text: "first input"})
	asstID := h.Append(ChatMessage{Role: RoleAssistant, Text: "streaming reply", Pending: true})
	h.Render(railTestWidth)

	h.mu.Lock()
	n := len(h.rail.ticks)
	h.mu.Unlock()
	if n != 0 {
		t.Fatalf("tick shown while first turn still running: %d ticks", n)
	}

	// 任务结束（或中止）：Finalize 清 Pending → 横条出现。
	h.Finalize(asstID)
	h.Render(railTestWidth)
	h.mu.Lock()
	n = len(h.rail.ticks)
	h.mu.Unlock()
	if n != 1 {
		t.Fatalf("ticks after finalize = %d, want 1", n)
	}

	// 第二个 turn 运行中：只有第一个输入有横条。
	h.Append(ChatMessage{Role: RoleUser, Text: "second input"})
	asst2 := h.Append(ChatMessage{Role: RoleAssistant, Text: "another reply", Pending: true})
	h.Render(railTestWidth)
	h.mu.Lock()
	n = len(h.rail.ticks)
	h.mu.Unlock()
	if n != 1 {
		t.Fatalf("ticks while second turn running = %d, want 1 (first input only)", n)
	}

	// 第二个 turn 结束：两个横条都出现。
	h.Finalize(asst2)
	h.Render(railTestWidth)
	h.mu.Lock()
	n = len(h.rail.ticks)
	h.mu.Unlock()
	if n != 2 {
		t.Fatalf("ticks after second finalize = %d, want 2", n)
	}
}

// TestRailTickHeldDuringThinking 验证 thinking 空窗：用户消息已 Append
// 但 assistant 消息尚未进入 history 时（没有任何 Pending 消息可查），
// 只要宿主上报了 turn 运行态，最后一条输入仍不得出现横条。
func TestRailTickHeldDuringThinking(t *testing.T) {
	h := NewChatHistory()
	h.SetRailEnabled(true)
	h.SetMaxRows(railTestMaxRows)

	h.Append(ChatMessage{Role: RoleUser, Text: "first input"})
	h.SetTurnRunning(true)
	h.Render(railTestWidth)
	h.mu.Lock()
	n := len(h.rail.ticks)
	h.mu.Unlock()
	if n != 0 {
		t.Fatalf("tick shown during thinking phase (no assistant msg yet): %d ticks", n)
	}

	// turn 结束（ChatApp.Idle → SetTurnRunning(false)）：横条出现。
	h.SetTurnRunning(false)
	h.Render(railTestWidth)
	h.mu.Lock()
	n = len(h.rail.ticks)
	h.mu.Unlock()
	if n != 1 {
		t.Fatalf("ticks after idle = %d, want 1", n)
	}
}

// TestRailPopupHoverSticky 验证鼠标移入浮层区域后浮层保持展开（这是
// 移入浮层选择文本的前提），且浮层区域内的 motion 不会启动历史选区。
func TestRailPopupHoverSticky(t *testing.T) {
	h := railTestHistory(t, railTestWidth, railTestMaxRows)
	h.mu.Lock()
	row := h.rail.ticks[0].row
	h.mu.Unlock()
	h.Update(core.MouseMsg{Action: core.MouseMotion, Row: int64(row), Col: railCol()})
	railDriveFrames(h, 20) // 滑入完成

	h.mu.Lock()
	top, left, popH, boxW := h.rail.popupTop, h.rail.popupLeft, h.rail.popupH, h.rail.popupBoxW
	h.mu.Unlock()
	if popH == 0 || boxW == 0 {
		t.Fatalf("popup geometry not captured")
	}

	// 移到浮层中心：浮层必须保持展开。
	h.Update(core.MouseMsg{Action: core.MouseMotion, Row: int64(top+popH/2), Col: left + boxW/2})
	out := railDriveFrames(h, 5)
	stillVisible := false
	for _, ln := range out {
		if strings.Contains(stripANSI(ln), "first input") && strings.Contains(stripANSI(ln), "│") {
			stillVisible = true
		}
	}
	if !stillVisible {
		t.Fatalf("popup disappeared after moving mouse into it")
	}
	if h.selActive {
		t.Fatalf("motion over popup must not start a history selection")
	}
}

// TestRailPopupSelectAndCopy 验证浮层内 press-drag-release 选中文本：
// 高亮渲染 + GetSelectedText 返回浮层文本（Cmd+C 复制路径的数据源）。
func TestRailPopupSelectAndCopy(t *testing.T) {
	h := railTestHistory(t, railTestWidth, railTestMaxRows)
	h.mu.Lock()
	row := h.rail.ticks[0].row
	h.mu.Unlock()
	h.Update(core.MouseMsg{Action: core.MouseMotion, Row: int64(row), Col: railCol()})
	railDriveFrames(h, 20)

	h.mu.Lock()
	top, left, popH := h.rail.popupTop, h.rail.popupLeft, h.rail.popupH
	h.mu.Unlock()
	if top+1 >= popH {
		t.Fatalf("popup too short for selection test: %d rows", popH)
	}

	// 在浮层第 1 行（用户输入预览行）上拖拽选中输入文本：
	// 局部列 0/1 是边框和空格，从第 2 列（"f"）拖到第 14 列。
	h.Update(core.MouseMsg{Action: core.MousePress, Row: int64(top + 1), Col: left + 2})
	if !h.rail.popupDragging {
		t.Fatalf("press inside popup did not start popup selection")
	}
	h.Update(core.MouseMsg{Action: core.MouseMotion, Row: int64(top + 1), Col: left + 14})
	h.Update(core.MouseMsg{Action: core.MouseRelease, Row: int64(top + 1), Col: left + 14})
	if h.rail.popupDragging {
		t.Fatalf("popup selection still dragging after release")
	}

	// 高亮：选区行应带选区背景转义。
	lines := railDriveFrames(h, 1)
	highlighted := false
	for _, ln := range lines {
		if strings.Contains(ln, "48;5;") {
			highlighted = true
		}
	}
	if !highlighted {
		t.Fatalf("popup selection not highlighted")
	}

	// 复制：GetSelectedText 应返回浮层文本而非底层内容。
	sel := h.GetSelectedText()
	if sel == "" {
		t.Fatalf("GetSelectedText returned empty for popup selection")
	}
	if !strings.Contains(sel, "first") {
		t.Fatalf("popup selection text = %q, want user input preview", sel)
	}

	// 点击浮层外清除选区。
	h.Update(core.MouseMsg{Action: core.MousePress, Row: 3, Col: 10})
	h.Update(core.MouseMsg{Action: core.MouseRelease, Row: 3, Col: 10})
	h.ClearSelection()
	if h.GetSelectedText() != "" {
		t.Fatalf("popup selection survived ClearSelection")
	}
}

// TestRailPopupMarkdown 验证浮层内容走 markdown 渲染：粗体/行内代码
// 标记被解析为样式（开色后断言 SGR），字面 "**" 和反引号不再出现。
func TestRailPopupMarkdown(t *testing.T) {
	theme.ForceColor(true)
	t.Cleanup(func() { theme.ForceColor(false) })

	h := NewChatHistory()
	h.SetRailEnabled(true)
	h.SetMaxRows(railTestMaxRows)
	h.Append(ChatMessage{Role: RoleUser, Text: "**bold** input"})
	h.Append(ChatMessage{Role: RoleAssistant, Text: "reply with `code` span"})
	h.Render(railTestWidth)
	h.mu.Lock()
	row := h.rail.ticks[0].row
	h.mu.Unlock()
	h.Update(core.MouseMsg{Action: core.MouseMotion, Row: int64(row), Col: railCol()})

	lines := railDriveFrames(h, 15)
	boldStyled, literalAsterisks, codeSpan := false, false, false
	for _, ln := range lines {
		plain := stripANSI(ln)
		if strings.Contains(plain, "bold input") {
			if strings.Contains(ln, "\x1b[1m") {
				boldStyled = true
			}
			if strings.Contains(plain, "**") {
				literalAsterisks = true
			}
		}
		if strings.Contains(plain, "code span") && strings.Contains(plain, "`") {
			codeSpan = true
		}
	}
	if !boldStyled {
		t.Fatalf("markdown bold not rendered as SGR style in popup")
	}
	if literalAsterisks {
		t.Fatalf("literal '**' leaked into popup; markdown not parsed")
	}
	if codeSpan {
		t.Fatalf("literal backtick leaked into popup; markdown not parsed")
	}
}

// TestRailTailBlankRun 验证右缘连续空白列数的计算：整宽分割线在内容
// 边距内留出空白（margin < railDrawCols 时判定窗被内容侵入但仍有空白），
// 无边距的整宽行空白为 0，短行尾部全空白。
func TestRailTailBlankRun(t *testing.T) {
	w := int64(100)
	// 内容边距 4：分割线横跨列 4..95，右缘空白 4 列。
	sep := strings.Repeat(" ", 4) + strings.Repeat("─", 92)
	if got := railTailBlankRun(sep, w, railDrawCols); got != 4 {
		t.Fatalf("divider with margin 4: blank run = %d, want 4", got)
	}
	// 无边距：分割线顶到右缘，空白 0。
	if got := railTailBlankRun(strings.Repeat("─", 100), w, railDrawCols); got != 0 {
		t.Fatalf("full-width divider: blank run = %d, want 0", got)
	}
	// 空行 / 短行：整个判定窗都空白。
	if got := railTailBlankRun("", w, railDrawCols); got != railDrawCols {
		t.Fatalf("blank row: blank run = %d, want %d", got, railDrawCols)
	}
	if got := railTailBlankRun("hello", w, railDrawCols); got != railDrawCols {
		t.Fatalf("short row: blank run = %d, want %d", got, railDrawCols)
	}
	// 宽字符紧贴判定窗：右半占的列不算空白。
	// 列 93 起一个宽字符（占 93、94 两列），判定窗 94..99 中 94 是
	// continuation → 连续空白只有 95..99 共 5 列。
	line := strings.Repeat("x", 93) + "宽" + strings.Repeat(" ", 5)
	if got := railTailBlankRun(line, w, railDrawCols); got != 5 {
		t.Fatalf("wide char at window edge: blank run = %d, want 5", got)
	}
}

// TestRailTickOnDividerRow 回归：tick 行与 turn 分割线重合时横条不得
// 消失——内容边距（4 列）小于 railDrawCols（6 列），分割线伸进判定窗
// 但右缘仍有空白，普通 tick（1 格）应照常落笔且不裁切分割线；悬停
// 变长时收缩到可用空白（4 格），不入侵分割线。
func TestRailTickOnDividerRow(t *testing.T) {
	const w = int64(100)
	const m = int64(4)
	h := NewChatHistory()
	h.SetRailEnabled(true)
	h.Append(ChatMessage{Role: RoleUser, Text: "hi"})
	h.Append(ChatMessage{Role: RoleAssistant, Text: "reply"})
	h.Render(w) // 建 cachedMsgRanges，让 ticks 可计算

	// 伪造一个视口：tick 行（正中间）放一条内容边距 4 的整宽分割线，
	// 其余行为空。paint 与 content 同源，模拟 Render 的真实路径。
	h.mu.Lock()
	viewRows := 11
	mid := viewRows / 2
	h.railComputeTicksLocked(int64(viewRows))
	tickN := len(h.rail.ticks)
	h.mu.Unlock()
	if tickN == 0 {
		t.Fatal("no ticks computed")
	}

	sep := strings.Repeat(" ", int(m)) + strings.Repeat("─", int(w-2*m))
	content := make([]string, viewRows)
	content[mid] = sep
	paint := make([]string, viewRows)
	copy(paint, content)
	out := h.applyRailOverlayWith(paint, content, w, false)

	plain := stripANSI(out[mid])
	if got := strings.Count(plain, "─"); got != int(w-2*m)+1 {
		t.Fatalf("divider row: %d dashes, want %d (divider intact + 1-cell tick)", got, w-2*m+1)
	}
	if !strings.HasSuffix(plain, "─") {
		t.Fatalf("tick missing at right edge on divider row: %q", plain)
	}

	// 悬停：tick 收缩到可用空白 4 格，分割线仍完整。advance=false 复用
	// 当前动画状态，直接把动画长度置为目标全长（6 格），验证绘制时的
	// 可用空白钳制。
	h.Update(core.MouseMsg{Action: core.MouseMotion, Row: int64(mid), Col: w - railHitCols})
	h.mu.Lock()
	h.rail.animLens = []float64{railDrawCols}
	h.mu.Unlock()
	content2 := make([]string, viewRows)
	content2[mid] = sep
	paint2 := make([]string, viewRows)
	copy(paint2, content2)
	out2 := h.applyRailOverlayWith(paint2, content2, w, false)
	plain2 := stripANSI(out2[mid])
	if got := strings.Count(plain2, "─"); got != int(w-2*m)+railDrawCols-2 {
		t.Fatalf("hovered divider row: %d dashes, want divider %d + clamped tick %d",
			got, w-2*m, railDrawCols-2)
	}
	if !strings.HasSuffix(plain2, "────") {
		t.Fatalf("hovered tick not clamped to available blank run: %q", plain2)
	}
}

// TestRailTopLayerPaintsOnBlankCanvas 验证顶层图层：RenderRailLayer 在空白
// 画布上只画 tick（与底图帧的 tick 行一致），tick 之外的位置全部保持空白
// 透出下层（overlay 或底图）。
func TestRailTopLayerPaintsOnBlankCanvas(t *testing.T) {
	h := railTestHistory(t, railTestWidth, railTestMaxRows)
	base := h.Render(railTestWidth)

	h.mu.Lock()
	tickRows := make(map[int]bool)
	for _, tk := range h.rail.ticks {
		tickRows[tk.row] = true
	}
	h.mu.Unlock()
	if len(tickRows) == 0 {
		t.Fatal("no ticks computed")
	}

	top := h.RenderRailLayer(len(base), railTestWidth)
	if top == nil {
		t.Fatal("RenderRailLayer returned nil")
	}
	if len(top) != len(base) {
		t.Fatalf("top layer rows = %d, want %d", len(top), len(base))
	}
	// 期望集合：tick 行中右缘尚有连续空白的行才画 tick（与底图同一
	// 让位规则，按实际空白收缩，不要求整个绘制区全空）。
	// 注意不能靠「行尾含 ─」识别底图 tick——用户输入分隔线本身就是
	// 全宽横线。
	basePainted := make(map[int]bool)
	h.mu.Lock()
	for _, tk := range h.rail.ticks {
		if tk.row < len(h.rail.preLines) && railTailBlankRun(h.rail.preLines[tk.row], railTestWidth, railDrawCols) > 0 {
			basePainted[tk.row] = true
		}
	}
	h.mu.Unlock()
	for r, ln := range top {
		plain := stripANSI(ln)
		if len(plain) < int(railDrawCols) {
			// 画布行不强制 pad：全空行以空串表示。
			if strings.TrimSpace(plain) != "" {
				t.Fatalf("short non-blank top row %d: %q", r, plain)
			}
			if basePainted[r] {
				t.Fatalf("tick row %d blank in top layer", r)
			}
			continue
		}
		tail := plain[len(plain)-int(railDrawCols):]
		head := plain[:len(plain)-int(railDrawCols)]
		if basePainted[r] {
			if strings.TrimSpace(tail) == "" {
				t.Fatalf("tick row %d missing tick in top layer", r)
			}
			if strings.TrimSpace(head) != "" {
				t.Fatalf("top layer row %d paints outside rail columns: %q", r, plain)
			}
		} else if strings.TrimSpace(plain) != "" {
			t.Fatalf("row %d painted in top layer but not in base: %q", r, plain)
		}
	}
}

// TestRailTopLayerYieldsToContent 验证顶层图层的让位规则与底图一致：
// 内容顶到右缘的行，tick 在顶层同样不落笔。
func TestRailTopLayerYieldsToContent(t *testing.T) {
	h := railTestHistory(t, railTestWidth, railTestMaxRows)
	h.Append(ChatMessage{Role: RoleUser, Text: "wide"})
	// 造一条右缘满宽的内容行：整宽段落让 markdown 渲染成占满行宽的文本。
	var sb strings.Builder
	for i := 0; i < 30; i++ {
		sb.WriteString("word ")
	}
	h.Append(ChatMessage{Role: RoleAssistant, Text: sb.String()})
	base := h.Render(railTestWidth)

	h.mu.Lock()
	var tickRows []int
	for _, tk := range h.rail.ticks {
		tickRows = append(tickRows, tk.row)
	}
	h.mu.Unlock()

	top := h.RenderRailLayer(len(base), railTestWidth)
	if top == nil {
		t.Fatal("RenderRailLayer returned nil")
	}
	// 顶层画了 tick 的行必须是底图右缘为空白的行。
	for _, r := range tickRows {
		if r >= len(top) {
			continue
		}
		tail := stripANSI(top[r])
		if len(tail) > int(railDrawCols) {
			tail = tail[len(tail)-int(railDrawCols):]
		}
		if strings.TrimSpace(tail) == "" {
			continue
		}
		baseTail := stripANSI(base[r])
		if len(baseTail) > int(railDrawCols) {
			baseTail = baseTail[len(baseTail)-int(railDrawCols):]
		}
		if strings.TrimSpace(baseTail) != "" && !strings.Contains(baseTail, "─") {
			t.Fatalf("top layer tick at row %d overwrites content tail %q", r, baseTail)
		}
	}
}
