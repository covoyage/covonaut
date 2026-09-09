package chat

import (
	"strings"
	"testing"
)

func TestReasoningRendererModes(t *testing.T) {
	seg := ThinkingSegment{Text: strings.Repeat("line\n", 12) + "tail"}
	msg := ChatMessage{ThinkingSegments: []ThinkingSegment{seg}}
	r := &DefaultReasoningRenderer{Show: true}

	r.Mode = "hide"
	if got := r.RenderThinking(msg, 80); len(got) != 0 {
		t.Fatalf("hide should emit nothing, got %v", got)
	}

	r.Mode = "summary"
	summary := strings.Join(r.RenderThinking(msg, 80), "\n")
	if !strings.Contains(summary, "Thinking") || strings.Contains(summary, "tail") {
		t.Fatalf("summary should be one line, got %q", summary)
	}

	r.Mode = "truncated"
	trunc := strings.Join(r.RenderThinking(msg, 80), "\n")
	if !strings.Contains(trunc, "…") {
		t.Fatalf("truncated should show ellipsis, got %q", trunc)
	}
	if strings.Contains(trunc, "tail") {
		t.Fatalf("truncated should not include the tail, got %q", trunc)
	}

	r.Mode = "full"
	full := strings.Join(r.RenderThinking(msg, 80), "\n")
	if !strings.Contains(full, "tail") {
		t.Fatalf("full should include tail, got %q", full)
	}

	r.Show = false
	r.Mode = "full"
	if got := r.RenderThinking(msg, 80); len(got) != 0 {
		t.Fatalf("Show=false should hide, got %v", got)
	}
}

func TestReasoningRendererHidesPendingWhenStreamOff(t *testing.T) {
	msg := ChatMessage{
		Pending:          true,
		ThinkingSegments: []ThinkingSegment{{Text: "secret thought"}},
	}
	r := &DefaultReasoningRenderer{Show: true, Stream: false, Mode: "full"}
	if got := r.RenderThinking(msg, 80); len(got) != 0 {
		t.Fatalf("stream off should hide pending thinking, got %v", got)
	}
	msg.Pending = false
	if got := r.RenderThinking(msg, 80); len(got) == 0 {
		t.Fatal("completed thinking should still render")
	}
}
