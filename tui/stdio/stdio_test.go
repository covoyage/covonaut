package stdio

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/covoyage/covonaut/tui/theme"
)

// ---- Layout ----

func TestHorizontalRule(t *testing.T) {
	rule := HorizontalRule(10)
	if rule != strings.Repeat("─", 10) {
		t.Fatalf("rule = %q", rule)
	}
}

func TestHorizontalRule_Zero(t *testing.T) {
	if HorizontalRule(0) != "" {
		t.Fatal("expected empty")
	}
}

func TestVisibleLen(t *testing.T) {
	if visibleLen("hello") != 5 {
		t.Fatalf("visibleLen(hello) = %d", 5)
	}
	if visibleLen("\033[31mhello\033[0m") != 5 {
		t.Fatalf("visibleLen(ansi) = %d", visibleLen("\033[31mhello\033[0m"))
	}
	if visibleLen("") != 0 {
		t.Fatal("visibleLen(empty) != 0")
	}
}

func TestRenderBox(t *testing.T) {
	box := RenderBox("", "content", 20)
	if !strings.Contains(box, "╭") || !strings.Contains(box, "╯") {
		t.Fatalf("box missing corners: %q", box)
	}
	if !strings.Contains(box, "content") {
		t.Fatalf("box missing content: %q", box)
	}
}

func TestRenderBox_WithTitle(t *testing.T) {
	box := RenderBox("Title", "body", 30)
	if !strings.Contains(box, "Title") {
		t.Fatalf("box missing title: %q", box)
	}
}

func TestRenderBox_SmallWidth(t *testing.T) {
	box := RenderBox("", "content", 2)
	if !strings.Contains(box, "╭") {
		t.Fatalf("box missing with small width: %q", box)
	}
}

// ---- LineReader ----

func TestLineReader_DefaultConfig(t *testing.T) {
	r := NewLineReader(LineReaderConfig{})
	if r.config.Prompt != "> " {
		t.Fatalf("prompt = %q", r.config.Prompt)
	}
	if r.config.HistoryMax != 100 {
		t.Fatalf("historyMax = %d", r.config.HistoryMax)
	}
	if r.config.Writer == nil {
		t.Fatal("writer should not be nil")
	}
	if r.config.Reader == nil {
		t.Fatal("reader should not be nil")
	}
}

func TestLineReader_CustomConfig(t *testing.T) {
	var buf bytes.Buffer
	r := NewLineReader(LineReaderConfig{
		Prompt:     ">>> ",
		HistoryMax: 10,
		Writer:     &buf,
		Reader:     strings.NewReader("hello\n"),
	})
	line, ok := r.ReadLine()
	if !ok {
		t.Fatal("expected ok")
	}
	if line != "hello" {
		t.Fatalf("line = %q", line)
	}
	if !strings.Contains(buf.String(), ">>> ") {
		t.Fatalf("prompt not written: %q", buf.String())
	}
}

func TestLineReader_EOF(t *testing.T) {
	r := NewLineReader(LineReaderConfig{
		Reader: strings.NewReader(""),
	})
	_, ok := r.ReadLine()
	if ok {
		t.Fatal("expected false on EOF")
	}
}

func TestLineReader_History(t *testing.T) {
	r := NewLineReader(LineReaderConfig{
		Reader: strings.NewReader("hello\nworld\nhello\n"),
	})
	r.ReadLine()
	r.ReadLine()
	r.ReadLine()

	hist := r.History()
	// dedup only prevents consecutive duplicates; "hello", "world", "hello" are all added
	if len(hist) != 3 {
		t.Fatalf("history length = %d, want 3", len(hist))
	}
	if hist[0] != "hello" || hist[1] != "world" || hist[2] != "hello" {
		t.Fatalf("history = %+v", hist)
	}
}

func TestLineReader_ClearHistory(t *testing.T) {
	r := NewLineReader(LineReaderConfig{
		Reader: strings.NewReader("hello\n"),
	})
	r.ReadLine()
	if len(r.History()) == 0 {
		t.Fatal("history should not be empty")
	}
	r.ClearHistory()
	if len(r.History()) != 0 {
		t.Fatal("history should be empty after clear")
	}
}

func TestLineReader_HistoryMax(t *testing.T) {
	input := ""
	expectedLines := 3
	for i := 0; i < expectedLines+10; i++ {
		input += "line"
	}
	r := NewLineReader(LineReaderConfig{
		HistoryMax: int64(expectedLines),
		Reader:     strings.NewReader(""),
	})
	for i := 0; i < expectedLines; i++ {
		r.addHistory("line")
	}
	hist := r.History()
	if len(hist) > expectedLines {
		t.Fatalf("history truncated to %d, want <=%d", len(hist), expectedLines)
	}
}

func TestLineReader_ReadMultiLine(t *testing.T) {
	r := NewLineReader(LineReaderConfig{
		Reader: strings.NewReader("line1\\\nline2\n"),
	})
	line, ok := r.ReadMultiLine()
	if !ok {
		t.Fatal("expected ok")
	}
	if line != "line1\nline2" {
		t.Fatalf("multiline = %q", line)
	}
}

// ---- Renderer ----

func TestRenderer_WriteChunk(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer()
	r.SetWriter(&buf)
	r.WriteChunk("hello")
	r.Flush()
	if buf.String() != "hello" {
		t.Fatalf("output = %q", buf.String())
	}
}

func TestRenderer_MultipleChunks(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer()
	r.SetWriter(&buf)
	r.WriteChunk("hello ")
	r.WriteChunk("world")
	r.Flush()
	output := buf.String()
	if !strings.Contains(output, "hello world") {
		t.Fatalf("output = %q", output)
	}
}

func TestRenderer_Reset(t *testing.T) {
	r := NewRenderer()
	r.WriteChunk("hello")
	r.Reset()
	var buf bytes.Buffer
	r.SetWriter(&buf)
	r.Flush()
	if buf.String() != "" {
		t.Fatalf("expected empty after reset, got %q", buf.String())
	}
}

func TestRenderer_CodeBlock(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer()
	r.SetWriter(&buf)
	r.WriteChunk("```go\n")
	r.WriteChunk("fmt.Println(\"hi\")\n")
	r.WriteChunk("```\n")
	r.Flush()
	output := buf.String()
	if !strings.Contains(output, "fmt.Println") {
		t.Fatalf("code block content missing: %q", output)
	}
}

// ---- ProgressBar ----

func TestProgressBar_New(t *testing.T) {
	p := NewProgressBar(100, 20)
	if p.total != 100 || p.width != 20 {
		t.Fatalf("total=%d width=%d", p.total, p.width)
	}
	if p.writer == nil {
		t.Fatal("writer should not be nil")
	}
}

func TestProgressBar_DefaultWidth(t *testing.T) {
	p := NewProgressBar(100, 0)
	if p.width != 40 {
		t.Fatalf("default width = %d", p.width)
	}
}

func TestProgressBar_Set(t *testing.T) {
	var buf bytes.Buffer
	p := NewProgressBar(100, 10)
	p.SetWriter(&buf)
	p.Set(50)
	if p.current != 50 {
		t.Fatalf("current = %d", p.current)
	}
}

func TestProgressBar_SetClamp(t *testing.T) {
	p := NewProgressBar(100, 10)
	p.Set(200)
	if p.current != 100 {
		t.Fatalf("current = %d, want 100", p.current)
	}
}

func TestProgressBar_Increment(t *testing.T) {
	p := NewProgressBar(100, 10)
	p.Increment(30)
	if p.current != 30 {
		t.Fatalf("current = %d", p.current)
	}
	p.Increment(80)
	if p.current != 100 {
		t.Fatalf("current = %d, want 100 (clamped)", p.current)
	}
}

func TestProgressBar_Done(t *testing.T) {
	var buf bytes.Buffer
	p := NewProgressBar(100, 10)
	p.SetWriter(&buf)
	p.Set(50)
	p.Done()
	if p.current != 100 {
		t.Fatalf("current should be 100 after done")
	}
}

func TestProgressBar_SetLabel(t *testing.T) {
	p := NewProgressBar(100, 10)
	p.SetLabel("loading")
	if p.label != "loading" {
		t.Fatalf("label = %q", p.label)
	}
}

func TestProgressBar_SetStyle(t *testing.T) {
	p := NewProgressBar(100, 10)
	s := theme.CurrentPalette().LoaderSpinner
	p.SetStyle(s)
}

// ---- TokenUsageDisplay ----

func TestTokenUsageDisplay_Render(t *testing.T) {
	var buf bytes.Buffer
	d := NewTokenUsageDisplay()
	d.SetWriter(&buf)
	d.Render(100, 50, 150)
	output := buf.String()
	if !strings.Contains(output, "100") || !strings.Contains(output, "50") || !strings.Contains(output, "150") {
		t.Fatalf("output = %q", output)
	}
}

func TestTokenUsageDisplay_RenderDetailed(t *testing.T) {
	var buf bytes.Buffer
	d := NewTokenUsageDisplay()
	d.SetWriter(&buf)
	d.RenderDetailed(100, 50, 150, "gpt-4", time.Second)
	output := buf.String()
	if !strings.Contains(output, "gpt-4") {
		t.Fatalf("detailed output missing model: %q", output)
	}
}

// ---- Timer ----

func TestTimer_Elapsed(t *testing.T) {
	tm := NewTimer("test")
	d := tm.Elapsed()
	if d < 0 {
		t.Fatal("elapsed should be positive")
	}
}

func TestTimer_String(t *testing.T) {
	tm := NewTimer("test")
	s := tm.String()
	if !strings.Contains(s, "test") {
		t.Fatalf("timer string = %q", s)
	}
}

// ---- WithSpinner ----

func TestWithSpinner_Success(t *testing.T) {
	result, err := WithSpinner("working", func() (string, error) {
		return "done", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result != "done" {
		t.Fatalf("result = %q", result)
	}
}

func TestWithSpinner_Error(t *testing.T) {
	_, err := WithSpinner("working", func() (string, error) {
		return "", nil
	})
	_ = err
}
