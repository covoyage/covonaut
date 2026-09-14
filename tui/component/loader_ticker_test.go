package component

import (
	"strings"
	"testing"
	"time"

	core "github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/theme"
)

func newRunningLoader(message, ticker string) *Loader {
	l := NewLoader(nil, message)
	l.Start()
	l.SetTicker(ticker)
	return l
}

// TestLoaderTickerSameLine 验证副文案与状态同行渲染，含分隔符。
func TestLoaderTickerSameLine(t *testing.T) {
	theme.ForceColor(false)
	t.Cleanup(func() { theme.ForceColor(false) })

	l := newRunningLoader("继续中...", "正在思考")
	lines := l.Render(80)
	if len(lines) != 1 {
		t.Fatalf("loader must render exactly one line, got %d", len(lines))
	}
	plain := core.StripAnsi(lines[0])
	if !strings.Contains(plain, "继续中...") || !strings.Contains(plain, "正在思考") {
		t.Fatalf("status and ticker must share the line: %q", plain)
	}
	if !strings.Contains(plain, "│") {
		t.Fatalf("separator missing: %q", plain)
	}
	if !strings.HasSuffix(strings.TrimRight(plain, " "), "正在思考") {
		t.Fatalf("ticker must be at line tail (newest content): %q", plain)
	}
}

// TestLoaderTickerTailWindow 验证超长副文案只显示尾部窗口，左侧 …
// 起头，行宽不超限。
func TestLoaderTickerTailWindow(t *testing.T) {
	theme.ForceColor(false)
	t.Cleanup(func() { theme.ForceColor(false) })

	long := strings.Repeat("a", 200)
	l := newRunningLoader("running", long)
	lines := l.Render(60)
	plain := strings.TrimRight(core.StripAnsi(lines[0]), " ")
	if strings.Contains(plain, strings.Repeat("a", 60)) {
		t.Fatalf("ticker head must be dropped, only tail shown: %q", plain)
	}
	// 取分隔符之后的部分检查省略号。
	sep := strings.Index(plain, "│")
	if sep < 0 {
		t.Fatalf("separator missing: %q", plain)
	}
	tailPart := strings.TrimLeft(plain[sep+len("│"):], " ")
	if !strings.HasPrefix(tailPart, "…") {
		t.Fatalf("tail window must start with ellipsis: %q", plain)
	}
	if vw := core.VisibleWidth(plain); vw > 60 {
		t.Fatalf("line width %d exceeds 60", vw)
	}
}

// TestLoaderTickerCJK 验证 CJK 尾部窗口按列切取，不产生半字。
func TestLoaderTickerCJK(t *testing.T) {
	theme.ForceColor(false)
	t.Cleanup(func() { theme.ForceColor(false) })

	long := strings.Repeat("思", 100)
	l := newRunningLoader("running", long)
	lines := l.Render(40)
	plain := strings.TrimRight(core.StripAnsi(lines[0]), " ")
	sep := strings.Index(plain, "│")
	if sep < 0 {
		t.Fatalf("separator missing: %q", plain)
	}
	// 尾部窗口（省略号之后）不应出现空格洞——半字被 SliceByColumn
	// 换成的空格只允许紧跟省略号一处。
	tail := strings.TrimLeft(plain[sep+len("│"):], " …")
	if strings.Contains(tail, " ") {
		t.Fatalf("unexpected space inside CJK tail window: %q", tail)
	}
}

// TestLoaderTickerEmptyDegrades 验证空副文案退化为原有单段渲染。
func TestLoaderTickerEmptyDegrades(t *testing.T) {
	theme.ForceColor(false)
	t.Cleanup(func() { theme.ForceColor(false) })

	l := newRunningLoader("继续中...", "")
	lines := l.Render(80)
	plain := core.StripAnsi(lines[0])
	if strings.Contains(plain, "│") {
		t.Fatalf("separator must be absent without ticker: %q", plain)
	}
}

// TestLoaderTickerThrottle 验证尾窗按 tickerSyncInterval 节流推进：
// 窗口内的连续更新不立即上屏，滚动速度与 delta 到达速率解耦。
func TestLoaderTickerThrottle(t *testing.T) {
	theme.ForceColor(false)
	t.Cleanup(func() { theme.ForceColor(false) })

	l := newRunningLoader("running", "first")
	if got := l.currentTicker(); got != "first" {
		t.Fatalf("first sync must be immediate, got %q", got)
	}
	l.SetTicker("second")
	if got := l.currentTicker(); got != "first" {
		t.Fatalf("update within interval must be throttled, got %q", got)
	}
	time.Sleep(tickerSyncInterval + 20*time.Millisecond)
	if got := l.currentTicker(); got != "second" {
		t.Fatalf("update after interval must sync, got %q", got)
	}
	// 清除立即生效，不受节流约束。
	l.SetTicker("")
	if got := l.currentTicker(); got != "" {
		t.Fatalf("clear must be immediate, got %q", got)
	}
}

// TestLoaderTickerNarrowDrops 验证状态文案过长、剩余空间不足时丢弃
// 副文案，行不超宽。
func TestLoaderTickerNarrowDrops(t *testing.T) {
	theme.ForceColor(false)
	t.Cleanup(func() { theme.ForceColor(false) })

	l := newRunningLoader(strings.Repeat("m", 50), "thinking tail")
	lines := l.Render(40)
	plain := core.StripAnsi(lines[0])
	if strings.Contains(plain, "thinking") {
		t.Fatalf("ticker must be dropped when space is insufficient: %q", plain)
	}
	if vw := core.VisibleWidth(strings.TrimRight(plain, " ")); vw > 40 {
		t.Fatalf("line width %d exceeds 40", vw)
	}
}
